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

// appendLockTimeout bounds one stream lock attempt on the append path. Each
// record is best effort, so a stream held by another process drops the record
// instead of charging the caller a longer wait.
const appendLockTimeout = 20 * time.Millisecond

// lockBackoff is how long a stream skips the lock after a failed attempt, so a
// burst of queued records costs one bounded wait instead of one wait per
// record.
const lockBackoff = 250 * time.Millisecond

// renameFile is os.Rename, replaced in tests to exercise a rotation whose
// generation shift succeeds but whose active rename fails.
var renameFile = os.Rename

// stream is one open per-kind file.
type stream struct {
	file    *os.File
	path    string
	written int64
	// shifted records that the generation shift already ran for the pending
	// rotation. Repeating it while only the active rename keeps failing would
	// discard another generation on every attempt.
	shifted bool
	// lockSkipUntil ends the backoff window after a failed lock attempt.
	lockSkipUntil time.Time
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

// lockStream takes the stream's cross-process lock for at most timeout. A nil
// result means the lock could not be taken: the caller must skip the locked
// work rather than write without exclusion, because lockStream already reported
// the problem once through warnLock. A lock the platform or filesystem cannot
// provide is a real Lock value reporting Unsupported, so appends proceed
// unlocked with the same one-time warning.
func (w *Writer) lockStream(s *stream, timeout time.Duration) *filelock.Lock {
	now := time.Now()
	if now.Before(s.lockSkipUntil) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	lock, err := filelock.Acquire(ctx, s.path+lockSuffix)
	if err != nil {
		s.lockSkipUntil = now.Add(lockBackoff)
		w.warnLock("lock diagnostics stream", err.Error())
		return nil
	}
	s.lockSkipUntil = time.Time{}
	if lock.Unsupported() {
		if msg := lock.Warning(); msg != "" {
			w.warnLock("lock diagnostics stream", msg)
		}
	}
	return lock
}

// reopen opens the active path and records its current size, so the rotation
// decision accounts for bytes other processes appended.
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
// no reader can see. A nil descriptor, as left behind by a failed rotation, is
// reopened here too, so the next write recovers with the staged generation
// shift intact.
func (s *stream) revalidate() error {
	pathInfo, err := os.Stat(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat diagnostics stream: %w", err)
	}
	if s.file != nil && err == nil {
		if fdInfo, fdErr := s.file.Stat(); fdErr == nil && os.SameFile(pathInfo, fdInfo) {
			s.written = fdInfo.Size()
			return nil
		}
		// Another process replaced the active file, so its rotation already
		// shifted the generations this writer had staged.
		s.shifted = false
	}
	if err := s.closeFile(); err != nil {
		return err
	}
	return s.reopen()
}

// rotateStream shifts existing generations, renames the active file to
// <file>.1 and opens a fresh one. The descriptor is closed before the rename
// because a descriptor to the renamed file would keep writing into generation
// 1. The shift runs once per pending rotation: when only the active rename
// fails, a retry repeats just that rename, so repeated failures cannot discard
// a generation each time. On failure the stream is left closed, so the next
// write revalidates and reopens the active path instead of dropping records for
// the rest of the run.
func rotateStream(s *stream) error {
	if err := s.closeFile(); err != nil {
		return fmt.Errorf("rotate diagnostics stream: %w", err)
	}
	if !s.shifted {
		for i := maxStreamGenerations; i >= 1; i-- {
			src := fmt.Sprintf("%s.%d", s.path, i)
			if i == maxStreamGenerations {
				_ = os.Remove(src) // best-effort: the oldest generation is dropped
				continue
			}
			if _, err := os.Stat(src); err == nil {
				_ = renameFile(src, fmt.Sprintf("%s.%d", s.path, i+1)) // best-effort: a stuck generation must not block rotation
			}
		}
		// Record the shift even when a step failed: repeating it on every retry
		// would discard another generation each time the active rename fails.
		s.shifted = true
	}
	if err := renameFile(s.path, s.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rotate diagnostics stream: %w", err)
	}
	s.shifted = false
	return s.reopen()
}
