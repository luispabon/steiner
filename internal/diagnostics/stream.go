package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/luispabon/steiner/internal/filelock"
)

// lockSuffix names the stable sibling lock file that serializes retention,
// rotation and appends for one stream across processes. The file is never
// deleted, so every writer locks the same inode even though the stream files
// themselves are replaced by rename.
const lockSuffix = ".lock"

// openLockTimeout bounds one stream lock attempt while a Writer opens. A busy
// diagnostics directory can delay startup by at most this much per stream and
// never fails it: retention is skipped when the lock cannot be taken.
const openLockTimeout = 250 * time.Millisecond

// appendLockAttempts and appendLockPause bound one append's lock attempts. Each
// attempt is nonblocking, so the first append after the lock is released takes
// it, and the tiny total keeps a burst of queued records cheap; a stream held
// for longer drops the record. The pause is enough to ride out a concurrent
// append holding the lock for one write syscall.
const (
	appendLockAttempts = 3
	appendLockPause    = 250 * time.Microsecond
)

// renameFile is os.Rename, replaced in tests to exercise a rotation whose
// generation shift succeeds but whose active rename fails.
var renameFile = os.Rename

// stream is one open per-kind file.
type stream struct {
	file    *os.File
	path    string
	written int64
	// lastFile identifies the inode this writer last saw at path, so a
	// replacement by another process is detected even while the descriptor is
	// closed after a failed rotation.
	lastFile os.FileInfo
	// droppedOldest records that the current rotation episode already removed
	// the oldest generation, so retries do not discard the generation that
	// shifted into its place.
	droppedOldest bool
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

// tryLock makes one lock attempt that waits at most timeout for the lock. A
// timeout of zero makes the attempt nonblocking. An unsupported lock is
// returned as a placeholder, so appends proceed without cross-process exclusion
// under the one-time warning.
func (w *Writer) tryLock(s *stream, timeout time.Duration) (*filelock.Lock, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	lock, err := filelock.Acquire(ctx, s.path+lockSuffix)
	if err != nil {
		return nil, err
	}
	if lock.Unsupported() {
		if msg := lock.Warning(); msg != "" {
			w.warnLock("lock diagnostics stream", msg)
		}
	}
	return lock, nil
}

// lockForOpen waits briefly for the stream lock, so a short contention window
// still lets retention run. A nil result skips retention: opening must not fail
// or stall on a busy diagnostics directory.
func (w *Writer) lockForOpen(s *stream) *filelock.Lock {
	lock, err := w.tryLock(s, openLockTimeout)
	if err != nil {
		w.warnLock("lock diagnostics stream", err.Error())
		return nil
	}
	return lock
}

// lockForAppend makes a few nonblocking attempts to ride out a concurrent
// append, then gives up. There is no backoff window, so the first append after
// the lock is released takes it. A nil result means the record is dropped
// rather than written without exclusion; the problem is reported once.
func (w *Writer) lockForAppend(s *stream) *filelock.Lock {
	var err error
	for attempt := 0; attempt < appendLockAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(appendLockPause)
		}
		var lock *filelock.Lock
		if lock, err = w.tryLock(s, 0); err == nil {
			return lock
		}
	}
	w.warnLock("lock diagnostics stream", err.Error())
	return nil
}

// reopen opens the active path and records its current size and inode, so the
// rotation decision accounts for bytes other processes appended and a later
// replacement is detectable.
func (s *stream) reopen() error {
	file, err := openStreamFile(s.path)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close() // best-effort: the descriptor is unusable without a size
		return fmt.Errorf("stat diagnostics stream: %w", err)
	}
	s.file = file
	s.written = info.Size()
	s.lastFile = info
	return nil
}

// closeFile closes and forgets s.file. Closing before a rename or reopen is
// what keeps the writer from holding a descriptor to a replaced file.
func (s *stream) closeFile() error {
	if s.file == nil {
		return nil
	}
	file := s.file
	s.file = nil
	s.written = 0
	if err := file.Close(); err != nil {
		return fmt.Errorf("close diagnostics stream: %w", err)
	}
	return nil
}

// revalidate points s.file at the file currently at s.path and refreshes the
// tracked size. Another process can replace the active file through retention
// or rotation, which leaves the open descriptor on an unlinked inode; that
// descriptor is closed and a fresh one opened, so records never land in a file
// no reader can see. The comparison uses the inode recorded by the last open,
// not the live descriptor, so a rotation staged by this writer survives its own
// failed attempt but is discarded once another process replaces the file. A nil
// descriptor, as left behind by a failed rotation, is reopened here too, so the
// next write recovers.
func (s *stream) revalidate() error {
	pathInfo, err := os.Stat(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat diagnostics stream: %w", err)
	}
	if err != nil || s.lastFile == nil || !os.SameFile(pathInfo, s.lastFile) {
		// The active file is gone or was replaced by another process, so any
		// staged rotation state describes a file that is no longer active.
		s.droppedOldest = false
	}
	if s.file != nil && err == nil {
		if fdInfo, fdErr := s.file.Stat(); fdErr == nil && os.SameFile(pathInfo, fdInfo) {
			s.written = fdInfo.Size()
			s.lastFile = fdInfo
			return nil
		}
	}
	if err := s.closeFile(); err != nil {
		return err
	}
	return s.reopen()
}

// rotateStream shifts existing generations, renames the active file to
// <file>.1 and opens a fresh one. The descriptor is closed before the rename
// because a descriptor to the renamed file would keep writing into generation
// 1. A move is only attempted onto a free generation name and the active rename
// only once generation 1 is free, so a partially completed shift never
// overwrites a generation; the oldest generation is dropped at most once per
// rotation episode (droppedOldest), so retries cannot discard another one. On
// failure the stream is left closed, so the next write revalidates and reopens
// the active path instead of dropping records for the rest of the run.
func rotateStream(s *stream) error {
	if err := s.closeFile(); err != nil {
		return fmt.Errorf("rotate diagnostics stream: %w", err)
	}
	if !s.droppedOldest {
		oldest := fmt.Sprintf("%s.%d", s.path, maxStreamGenerations)
		if err := os.Remove(oldest); err == nil || errors.Is(err, os.ErrNotExist) {
			s.droppedOldest = true
		}
	}
	for i := maxStreamGenerations - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", s.path, i)
		dst := fmt.Sprintf("%s.%d", s.path, i+1)
		if !fileExists(src) || fileExists(dst) {
			// Nothing to move, or the target still holds a generation that could
			// not move. Either way the shift is incomplete and retried later.
			continue
		}
		_ = renameFile(src, dst) // best-effort: an unexpected failure is retried on the next attempt
	}
	genOne := s.path + ".1"
	if fileExists(genOne) {
		// Renaming the active file now would overwrite generation 1.
		return fmt.Errorf("rotate diagnostics stream: %s is still in place", genOne)
	}
	if err := renameFile(s.path, genOne); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rotate diagnostics stream: %w", err)
	}
	s.droppedOldest = false
	return s.reopen()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
