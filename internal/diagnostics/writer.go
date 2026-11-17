package diagnostics

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luispabon/steiner/internal/filelock"
)

// maxStreamBytes caps one generation of a stream file before it rotates.
const maxStreamBytes = 20 * 1024 * 1024 // 20MB

// maxStreamGenerations bounds how many rotated generations
// (<file>.1 .. <file>.N) are kept alongside the active file.
const maxStreamGenerations = 3

// seq is the process-wide record counter. It is global rather than per-stream
// so records from different files can be ordered against each other.
var seq atomic.Int64

// Writer appends diagnostics records to one JSONL file per enabled stream.
// Safe for concurrent use. A nil *Writer is a valid no-op writer, so callers
// never need a nil check.
type Writer struct {
	mu       sync.Mutex
	dir      string
	streams  Streams
	files    map[Kind]*stream
	warned   bool
	lockWarn bool
	closed   bool
	captureB bool
	runID    string
	buildSHA string
	dirty    bool
}

// New opens a Writer over opts.Dir, creating the directory (0o700) and one
// 0o600 file per enabled stream. Records older than opts.RetentionDays are
// pruned from existing files before they are opened. Returns a nil Writer,
// whose methods are no-ops, when no stream is enabled.
func New(opts Options) (*Writer, error) {
	if !opts.Streams.any() {
		return nil, nil
	}
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		return nil, fmt.Errorf("diagnostics directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create diagnostics directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure diagnostics directory: %w", err)
	}
	w := &Writer{
		dir:      dir,
		streams:  opts.Streams,
		files:    make(map[Kind]*stream, len(Kinds())),
		captureB: opts.CaptureBodies,
		runID:    opts.RunID,
		buildSHA: opts.BuildSHA,
		dirty:    opts.Dirty,
	}
	if w.runID == "" {
		w.runID = newRunID()
	}
	var cutoff time.Time
	if opts.RetentionDays > 0 {
		cutoff = time.Now().UTC().AddDate(0, 0, -opts.RetentionDays)
	}
	for _, kind := range Kinds() {
		if !opts.Streams.enabled(kind) {
			continue
		}
		if err := w.openStream(kind, cutoff); err != nil {
			_ = w.Close()
			return nil, err
		}
	}
	return w, nil
}

// openStream opens the file for kind after applying retention while holding the
// stream's cross-process lock, so a concurrent writer cannot append through a
// descriptor this process is about to replace by rename.
func (w *Writer) openStream(kind Kind, cutoff time.Time) error {
	path := filepath.Join(w.dir, kind.fileName())
	lock, err := lockStream(path)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }() // best-effort: closing the descriptor releases the lock
	w.warnLockUnsupported(lock)

	if !cutoff.IsZero() {
		if err := pruneStaleGenerations(path, cutoff); err != nil {
			return err
		}
	}
	s := &stream{path: path}
	if err := s.reopen(); err != nil {
		return err
	}
	w.files[kind] = s
	return nil
}

func newRunID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Enabled reports whether records of kind are recorded. Callers use it to skip
// assembling a payload they would only throw away. False on a nil receiver.
func (w *Writer) Enabled(kind Kind) bool {
	if w == nil {
		return false
	}
	return w.streams.enabled(kind)
}

// CaptureBodies reports whether payloads may carry full message, tool and
// block content. False on a nil receiver.
func (w *Writer) CaptureBodies() bool {
	if w == nil {
		return false
	}
	return w.captureB
}

// RunID returns the identifier stamped onto every record this writer emits.
// Empty on a nil receiver.
func (w *Writer) RunID() string {
	if w == nil {
		return ""
	}
	return w.runID
}

// Write appends rec to its stream's file. Timestamp, Seq and the build
// identity fields are filled in when unset. Best-effort: a write failure is
// reported once per writer and never returned. No-op on a nil receiver or a
// disabled stream.
func (w *Writer) Write(rec Record) {
	if w == nil || !w.streams.enabled(rec.Kind) {
		return
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	rec.Seq = seq.Add(1)
	if rec.RunID == "" {
		rec.RunID = w.runID
	}
	if rec.BuildSHA == "" {
		rec.BuildSHA = w.buildSHA
	}
	if !rec.Dirty {
		rec.Dirty = w.dirty
	}

	data, err := json.Marshal(rec)
	if err != nil {
		w.mu.Lock()
		w.warn("marshal diagnostics record", err)
		w.mu.Unlock()
		return
	}
	data = append(data, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return
	}
	s := w.files[rec.Kind]
	if s == nil {
		return
	}
	if err := w.appendRecord(s, data); err != nil {
		w.warn("append diagnostics record", err)
	}
}

// appendRecord appends one encoded record to s under the stream's cross-process
// lock. Revalidating first keeps a descriptor replaced by another process's
// retention or rotation out of the write path, and refreshes the size used for
// the rotation decision. Caller must hold the writer's mutex.
func (w *Writer) appendRecord(s *stream, data []byte) error {
	lock, err := lockStream(s.path)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }() // best-effort: closing the descriptor releases the lock
	w.warnLockUnsupported(lock)

	if err := s.revalidate(); err != nil {
		return err
	}
	if s.written+int64(len(data)) > maxStreamBytes {
		if err := rotateStream(s); err != nil {
			return err
		}
	}
	n, err := s.file.Write(data)
	if err != nil {
		return fmt.Errorf("write diagnostics record: %w", err)
	}
	s.written += int64(n)
	return nil
}

// warn logs the first failure this writer hits and stays quiet afterwards, so
// a broken diagnostics directory cannot flood the log. Caller must hold mu.
func (w *Writer) warn(action string, err error) {
	if w.warned {
		return
	}
	w.warned = true
	slog.Warn("diagnostics writer degraded", "action", action, "error", err, "dir", w.dir)
}

// warnLockUnsupported reports, once per writer, that the stream lock degraded
// to no cross-process exclusion. Caller must hold the writer's mutex, or call
// it before the writer is shared.
func (w *Writer) warnLockUnsupported(lock *filelock.Lock) {
	if w.lockWarn || !lock.Unsupported() {
		return
	}
	w.lockWarn = true
	if msg := lock.Warning(); msg != "" {
		slog.Warn("diagnostics writer degraded", "action", "lock diagnostics stream", "error", msg, "dir", w.dir)
	}
}

// Close closes every open stream file. No-op on a nil receiver. Records written
// after Close are dropped rather than reopening a file nothing will close
// again.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	var firstErr error
	for kind, s := range w.files {
		if s == nil {
			continue
		}
		if err := s.closeFile(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close diagnostics stream %s: %w", kind, err)
		}
	}
	return firstErr
}
