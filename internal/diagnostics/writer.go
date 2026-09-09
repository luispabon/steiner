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
)

// maxStreamBytes caps one generation of a stream file before it rotates.
const maxStreamBytes = 20 * 1024 * 1024 // 20MB

// maxStreamGenerations bounds how many rotated generations
// (<file>.1 .. <file>.N) are kept alongside the active file.
const maxStreamGenerations = 3

// seq is the process-wide record counter. It is global rather than per-stream
// so records from different files can be ordered against each other.
var seq atomic.Int64

// stream is one open per-kind file.
type stream struct {
	file    *os.File
	path    string
	written int64
}

// Writer appends diagnostics records to one JSONL file per enabled stream.
// Safe for concurrent use. A nil *Writer is a valid no-op writer, so callers
// never need a nil check.
type Writer struct {
	mu       sync.Mutex
	dir      string
	streams  Streams
	files    map[Kind]*stream
	warned   bool
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
		path := filepath.Join(dir, kind.fileName())
		if !cutoff.IsZero() {
			if err := pruneStaleGenerations(path, cutoff); err != nil {
				_ = w.Close()
				return nil, err
			}
		}
		file, err := openStreamFile(path)
		if err != nil {
			_ = w.Close()
			return nil, err
		}
		var written int64
		if info, statErr := file.Stat(); statErr == nil {
			written = info.Size()
		}
		w.files[kind] = &stream{file: file, path: path, written: written}
	}
	return w, nil
}

func openStreamFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open diagnostics stream: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure diagnostics stream: %w", err)
	}
	return file, nil
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

	s := w.files[rec.Kind]
	if s == nil || s.file == nil {
		return
	}
	if s.written+int64(len(data)) > maxStreamBytes {
		if err := rotate(s); err != nil {
			w.warn("rotate diagnostics stream", err)
			return
		}
	}
	n, err := s.file.Write(data)
	if err != nil {
		w.warn("write diagnostics record", err)
		return
	}
	s.written += int64(n)
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

// rotate shifts existing generations, renames the active file to <file>.1 and
// opens a fresh one. Caller must hold the writer's mutex.
func rotate(s *stream) error {
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("close diagnostics stream for rotation: %w", err)
	}
	s.file = nil
	for i := maxStreamGenerations; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", s.path, i)
		if i == maxStreamGenerations {
			_ = os.Remove(src)
			continue
		}
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, fmt.Sprintf("%s.%d", s.path, i+1))
		}
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate diagnostics stream: %w", err)
	}
	file, err := openStreamFile(s.path)
	if err != nil {
		return err
	}
	s.file = file
	s.written = 0
	return nil
}

// Close closes every open stream file. No-op on a nil receiver.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var firstErr error
	for kind, s := range w.files {
		if s == nil || s.file == nil {
			continue
		}
		if err := s.file.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close diagnostics stream %s: %w", kind, err)
		}
		s.file = nil
	}
	return firstErr
}
