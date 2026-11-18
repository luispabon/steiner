package diagnostics

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// stallRetryInterval bounds how long a stalled stream waits before retrying a
// failed rotation, so recovery does not depend on how many records were dropped
// while the obstruction lasted.
const stallRetryInterval = 5 * time.Second

// timeNow is time.Now, replaced in tests so the stall retry interval is
// exercised without waiting.
var timeNow = time.Now

// seq is the process-wide record counter. It is global rather than per-stream
// so records from different files can be ordered against each other.
var seq atomic.Int64

// stream is one open per-kind file.
//
// Rotation failure is bounded. A failed rotation appends past the cap for at
// most one more cap worth of records; if the deferred retry fails too the
// stream drops records until a rotation succeeds.
type stream struct {
	file    *os.File
	path    string
	written int64
	// rotateAfter defers a failed rotation until the active file reaches this
	// size, so the generations are not shifted once per record.
	rotateAfter int64
	// rotateStalled reports that the deferred retry failed as well. Records are
	// then dropped, which keeps the file within one cap of the cap.
	rotateStalled bool
	// retryAt is the earliest time a stalled stream attempts another rotation,
	// so attempts happen at most once per stallRetryInterval rather than per
	// record, and recovery does not wait for dropped volume.
	retryAt time.Time
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
	closed   bool
	captureB bool
	runID    string
	buildSHA string
	dirty    bool
}

// New opens a Writer over opts.Dir, creating the directory (0o700) and one
// 0o600 file per enabled stream. Under the stream lock, an interrupted rotation
// is completed first, then records older than opts.RetentionDays are pruned from
// existing files and an unterminated trailing record left by a killed process is
// removed before the file is opened. Returns a nil Writer, whose methods are
// no-ops, when no stream is enabled.
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
		if err := w.prepareStream(kind, cutoff); err != nil {
			_ = w.Close()
			return nil, err
		}
	}
	return w, nil
}

// prepareStream runs the startup mutations for one stream and opens its file.
// Retention and tail repair rewrite a file another process may be appending to,
// so they run under the stream lock. An unavailable lock skips them rather than
// failing: opening for append is not protected work, and startup contention
// should not cost the process every later record.
func (w *Writer) prepareStream(kind Kind, cutoff time.Time) error {
	path := filepath.Join(w.dir, kind.fileName())
	if lock, ok := w.lockStream("lock diagnostics stream", kind); ok {
		defer func() {
			// Best-effort: the lock file stays; only the descriptor is closed.
			_ = lock.Release()
		}()
		// A failed recovery must not disable the stream: the file still opens and
		// append retries the recovery under the lock. Retention and tail repair
		// would rewrite a file the unfinished rotation still owns, so they wait.
		if err := recoverRotation(path); err != nil {
			w.warn("rotate diagnostics stream", err)
		} else {
			if !cutoff.IsZero() {
				if err := pruneStaleGenerations(path, cutoff); err != nil {
					return err
				}
			}
			if err := truncateIncompleteTail(path); err != nil {
				return err
			}
		}
	}
	file, err := openStreamFile(path)
	if err != nil {
		return err
	}
	var written int64
	if info, statErr := file.Stat(); statErr == nil {
		written = info.Size()
	}
	w.files[kind] = &stream{file: file, path: path, written: written}
	return nil
}

// tailScanBytes bounds one read while truncateIncompleteTail searches backwards
// for the last complete line.
const tailScanBytes = 4096

// truncateIncompleteTail drops a trailing fragment left by a process killed
// mid-record. Appending after an unterminated line would merge the fragment with
// the next record and corrupt both, so the file is cut back to the last
// newline. Complete lines stay, and a file that already ends with a newline is
// left alone. Caller must hold the stream lock.
func truncateIncompleteTail(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("repair diagnostics stream tail: %w", err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("repair diagnostics stream tail: %w", err)
	}
	size := info.Size()
	if size == 0 {
		return nil
	}
	buf := make([]byte, tailScanBytes)
	for end := size; end > 0; {
		start := end - tailScanBytes
		if start < 0 {
			start = 0
		}
		n := int(end - start)
		if _, readErr := file.ReadAt(buf[:n], start); readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("repair diagnostics stream tail: %w", readErr)
		}
		end = start
		idx := bytes.LastIndexByte(buf[:n], '\n')
		if idx < 0 {
			continue
		}
		keep := start + int64(idx) + 1
		if keep == size {
			return nil
		}
		if err := file.Truncate(keep); err != nil {
			return fmt.Errorf("repair diagnostics stream tail: %w", err)
		}
		return nil
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("repair diagnostics stream tail: %w", err)
	}
	return nil
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
//
// Another process can share the directory, so each record first reopens the
// active file when it no longer matches the open descriptor. The record can
// still be lost: the file can be replaced again between that check and the
// write.
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
	w.append(rec.Kind, data)
}

// append writes one encoded record to its stream, refreshing and rotating the
// stream first. The stream lock is held for the whole mutation so a cooperating
// process cannot rotate or retain the file between the refresh and the append;
// when no usable lock is available the record is dropped instead of written
// unprotected. Caller must hold the writer's mutex.
func (w *Writer) append(kind Kind, data []byte) {
	if w.closed {
		return
	}
	s := w.files[kind]
	if s == nil {
		return
	}
	lock, ok := w.lockStream("lock diagnostics stream", kind)
	if !ok {
		return
	}
	defer func() {
		// Best-effort: the descriptor releases the kernel lock, and the record is
		// already written.
		_ = lock.Release()
	}()
	// An interrupted rotation is finished before anything else touches the
	// stream. When it cannot be, the record is dropped rather than appended to a
	// stream whose generations are in an unknown state.
	if err := recoverRotation(s.path); err != nil {
		w.warn("rotate diagnostics stream", err)
		return
	}
	if err := s.reopenIfNeeded(); err != nil {
		w.warn("refresh diagnostics stream", err)
		return
	}
	if next := int64(len(data)); s.rotationDue(timeNow(), next) {
		if err := rotateStream(s); err != nil {
			w.warn("rotate diagnostics stream", err)
		}
	}
	if s.file == nil {
		return
	}
	if s.rotateStalled {
		return
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

// reopenIfNeeded readies s for the next append. Retention and another
// process's rotation replace the active path with a new inode, leaving this
// descriptor on a file no reader will find, so the path and the open file are
// compared with os.SameFile and the stream reopened when they differ. The
// tracked size is refreshed from the active file so rotation counts records
// other writers appended. A stat error that is neither a replacement nor a
// missing file is returned unchanged, because it says nothing about the stream.
// Caller must hold the writer's mutex.
func (s *stream) reopenIfNeeded() error {
	if s.file == nil {
		return s.reopen()
	}
	open, err := s.file.Stat()
	if err != nil {
		return s.reopen()
	}
	active, err := os.Stat(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat diagnostics stream: %w", err)
		}
		s.resetRotation()
		return s.reopen()
	}
	if !os.SameFile(open, active) {
		// Another process replaced the active file, so a deferral or stall
		// recorded against the old one says nothing about this file.
		s.resetRotation()
		return s.reopen()
	}
	s.written = active.Size()
	return nil
}

// rotationDue reports whether the next record of size next should attempt a
// rotation. After a failed rotation the attempt waits for one cap worth of
// appended records; a stalled stream attempts at most one rotation per
// stallRetryInterval whatever its size, and recovery does not depend on dropped
// volume. Caller must hold the writer's mutex.
func (s *stream) rotationDue(now time.Time, next int64) bool {
	if s.rotateStalled {
		return !now.Before(s.retryAt)
	}
	return s.written+next > maxStreamBytes && s.written >= s.rotateAfter
}

// resetRotation clears the deferred rotation state. It runs after a successful
// rotation and when another process replaces or removes the active file. Caller
// must hold the writer's mutex.
func (s *stream) resetRotation() {
	s.rotateAfter = 0
	s.rotateStalled = false
	s.retryAt = time.Time{}
}

// reopen closes the current descriptor and opens the active path, taking the
// tracked size from the file it opened. Caller must hold the writer's mutex.
func (s *stream) reopen() error {
	if s.file != nil {
		// Best-effort: the old descriptor names a file no active path reaches.
		_ = s.file.Close()
		s.file = nil
	}
	file, err := openStreamFile(s.path)
	if err != nil {
		return err
	}
	s.file = file
	s.written = 0
	if info, statErr := file.Stat(); statErr == nil {
		s.written = info.Size()
	}
	return nil
}

// deferRotation keeps a failed rotation from disabling the stream or growing the
// file without bound. The first failure allows appends past the cap for one more
// cap worth of records; when the deferred retry fails too, the stream stalls and
// drops records, retrying every stallRetryInterval until a rotation succeeds.
// Caller must hold the writer's mutex.
func (s *stream) deferRotation(err error) error {
	if s.rotateAfter == 0 {
		s.rotateAfter = s.written + maxStreamBytes
	} else {
		s.rotateStalled = true
		s.retryAt = timeNow().Add(stallRetryInterval)
	}
	if reopenErr := s.reopen(); reopenErr != nil {
		return fmt.Errorf("%w; reopen diagnostics stream: %w", err, reopenErr)
	}
	return err
}

// Close closes every open stream file. No-op on a nil receiver. Records written
// after Close are dropped rather than reopening a file nothing would close.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
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
