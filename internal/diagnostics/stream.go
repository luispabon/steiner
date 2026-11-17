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

// lockTimeout bounds one stream lock acquisition. Diagnostics are best effort:
// a writer parked behind a stuck or suspended process drops the record instead
// of stalling the caller indefinitely.
const lockTimeout = 2 * time.Second

// stream is one open per-kind file.
type stream struct {
	file    *os.File
	path    string
	written int64
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

// lockStream takes the stream's cross-process lock. The caller releases it when
// the retention, rotation or append it guards is done.
func lockStream(path string) (*filelock.Lock, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	lock, err := filelock.Acquire(ctx, path+lockSuffix)
	if err != nil {
		return nil, fmt.Errorf("lock diagnostics stream: %w", err)
	}
	return lock, nil
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
// reopened here too, so the next write recovers.
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
	}
	if err := s.closeFile(); err != nil {
		return err
	}
	return s.reopen()
}

// rotateStream shifts existing generations, renames the active file to
// <file>.1 and opens a fresh one. The descriptor is closed before the rename
// because a descriptor to the renamed file would keep writing into generation
// 1. On failure the stream is left closed, so the next write revalidates and
// reopens the active path instead of dropping records for the rest of the run.
func rotateStream(s *stream) error {
	if err := s.closeFile(); err != nil {
		return fmt.Errorf("rotate diagnostics stream: %w", err)
	}
	for i := maxStreamGenerations; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", s.path, i)
		if i == maxStreamGenerations {
			_ = os.Remove(src) // best-effort: the oldest generation is dropped
			continue
		}
		if _, err := os.Stat(src); err == nil {
			_ = os.Rename(src, fmt.Sprintf("%s.%d", s.path, i+1)) // best-effort: a stuck generation must not block rotation
		}
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rotate diagnostics stream: %w", err)
	}
	return s.reopen()
}
