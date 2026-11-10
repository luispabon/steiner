package sandbox

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// errSandboxClosed reports use of a Sandbox after Cleanup.
var errSandboxClosed = errors.New("sandbox is closed")

// errTmpDirNotOwned reports a tmp dir path that is not the directory this
// Sandbox locked: a symlink, a non-directory, or a replacement inode.
var errTmpDirNotOwned = errors.New("sandbox tmp dir is not owned by this session")

// tmpReprovisionMessage is the user-facing warning for a lost sandbox /tmp.
const tmpReprovisionMessage = "sandbox /tmp was missing and has been recreated empty; previously written files are lost"

// SetTmpReprovisionHandler registers a callback invoked at most once when the
// sandbox tmp directory disappears and is recreated empty mid-session.
func (s *Sandbox) SetTmpReprovisionHandler(handler func(string)) {
	s.tmpMu.Lock()
	defer s.tmpMu.Unlock()
	s.tmpReprovisionHandler = handler
}

// LockTmpDir pins the session tmp directory with a non-blocking advisory lock
// that startup orphan cleanup in other runtimes respects, so the directory is
// not deleted while this runtime is alive. It is a no-op when tmpDir is empty
// and degrades to an unprotected directory where advisory directory locks are
// unavailable.
func (s *Sandbox) LockTmpDir() error {
	s.tmpMu.Lock()
	defer s.tmpMu.Unlock()
	if s.tmpClosed {
		return errSandboxClosed
	}
	if s.tmpDir == "" || s.tmpLock != nil || s.tmpLockUnsupported {
		return nil
	}
	if err := s.adoptTmpDirLockLocked(); err != nil {
		return fmt.Errorf("lock sandbox tmp dir: %w", err)
	}
	return nil
}

// ensureTmpDir validates the session tmp directory before it is bound and
// re-provisions it when it disappeared. bindable is false when the path is not
// a directory this Sandbox owns, which callers must not bind. The reprovision
// warning is emitted after tmpMu is released.
func (s *Sandbox) ensureTmpDir() (bindable bool, err error) {
	s.tmpMu.Lock()
	bindable, reprovisioned, err := s.ensureTmpDirLocked()
	tmpDir := s.tmpDir
	var notify func(string)
	if reprovisioned && !s.tmpWarned {
		s.tmpWarned = true
		notify = s.tmpReprovisionHandler
	}
	s.tmpMu.Unlock()
	if reprovisioned {
		slog.Warn("sandbox tmp dir re-provisioned", "path", tmpDir)
	}
	if notify != nil {
		notify(tmpReprovisionMessage)
	}
	return bindable, err
}

// ensureTmpDirLocked re-provisions a missing tmp directory and reports whether
// the directory may be bound. The caller must hold tmpMu.
func (s *Sandbox) ensureTmpDirLocked() (bindable, reprovisioned bool, err error) {
	if s.tmpClosed {
		return false, false, errSandboxClosed
	}
	if s.tmpDir == "" {
		return true, false, nil
	}
	state, err := s.tmpDirStateLocked()
	if err != nil {
		return false, false, err
	}
	switch state {
	case tmpDirMissing:
		if err := s.reprovisionTmpDirLocked(); err != nil {
			return false, false, err
		}
		return true, true, nil
	case tmpDirForeign:
		// Never adopt or delete a path this session does not own.
		return false, false, nil
	default:
		return true, false, nil
	}
}

// reprovisionTmpDirLocked recreates a missing tmp directory and takes a fresh
// lock on it. Previous contents are not restored. The caller must hold tmpMu.
func (s *Sandbox) reprovisionTmpDirLocked() error {
	if err := EnsureDirectoryPath(s.root, s.tmpDir); err != nil {
		return fmt.Errorf("recreate sandbox tmp dir: %w", err)
	}
	if err := s.adoptTmpDirLockLocked(); err != nil {
		return fmt.Errorf("lock recreated sandbox tmp dir: %w", err)
	}
	return nil
}

// adoptTmpDirLockLocked locks s.tmpDir, verifies the lock still names the path,
// and replaces any held lock. A platform without advisory directory locks is
// recorded as unprotected rather than rejected. The caller must hold tmpMu.
func (s *Sandbox) adoptTmpDirLockLocked() error {
	lock, err := tmpDirLockAcquire(s.tmpDir)
	if err != nil {
		if errors.Is(err, errTmpDirLockUnsupported) {
			s.tmpLockUnsupported = true
			return nil
		}
		return err
	}
	pathInfo, pathErr := os.Lstat(s.tmpDir)
	lockedInfo, statErr := lock.Stat()
	if pathErr != nil || statErr != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() || !os.SameFile(lockedInfo, pathInfo) {
		// The path is a symlink, not a directory, or a different inode than the
		// one just locked, so the lock protects nothing worth adopting.
		_ = lock.Close()
		return errTmpDirNotOwned
	}
	if s.tmpLock != nil {
		// The previously locked directory is gone; closing its fd releases the
		// stale lock.
		_ = s.tmpLock.Close()
	}
	s.tmpLock = lock
	return nil
}

// tmpDirState describes what the held lock says about s.tmpDir.
type tmpDirState int

const (
	tmpDirMissing tmpDirState = iota
	tmpDirOwned
	tmpDirForeign
)

// tmpDirStateLocked classifies s.tmpDir relative to the lock this Sandbox
// holds. The caller must hold tmpMu.
func (s *Sandbox) tmpDirStateLocked() (tmpDirState, error) {
	pathInfo, err := os.Lstat(s.tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return tmpDirMissing, nil
		}
		return tmpDirForeign, fmt.Errorf("inspect sandbox tmp dir: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() {
		return tmpDirForeign, nil
	}
	if s.tmpLock == nil {
		// Without a lock there is no inode to compare against: an unprotected
		// or hand-built Sandbox owns any real directory at its tmpDir path.
		return tmpDirOwned, nil
	}
	lockedInfo, err := s.tmpLock.Stat()
	if err != nil || !os.SameFile(lockedInfo, pathInfo) {
		return tmpDirForeign, nil
	}
	return tmpDirOwned, nil
}

// Cleanup removes the session-scoped tmp directory and releases its liveness
// lock. It is idempotent, and after it runs WrapCommandMode and ResetTmp return
// errSandboxClosed. The directory is only removed when it is a real directory
// still owned by this Sandbox, so a replacement created after the original
// disappeared is left untouched. No-op when tmpDir is empty.
func (s *Sandbox) Cleanup() error {
	s.tmpMu.Lock()
	defer s.tmpMu.Unlock()
	if s.tmpClosed {
		return nil
	}
	s.tmpClosed = true
	lock := s.tmpLock
	owned := false
	if s.tmpDir != "" && (s.root == "" || safeDirectoryPath(s.root, s.tmpDir) == nil) {
		state, err := s.tmpDirStateLocked()
		owned = err == nil && state == tmpDirOwned
	}
	s.tmpLock = nil
	if lock != nil {
		// Closing the descriptor releases the flock even when removal is skipped.
		defer func() { _ = lock.Close() }()
	}
	if !owned {
		return nil
	}
	if err := os.RemoveAll(s.tmpDir); err != nil {
		return fmt.Errorf("remove sandbox tmp dir: %w", err)
	}
	return nil
}

// ResetTmp removes all entries inside tmpDir but keeps the directory and its
// liveness lock. A missing tmpDir is re-provisioned silently; a directory this
// Sandbox does not own is left untouched. No-op when tmpDir is empty.
func (s *Sandbox) ResetTmp() error {
	s.tmpMu.Lock()
	defer s.tmpMu.Unlock()
	if s.tmpClosed {
		return errSandboxClosed
	}
	if s.tmpDir == "" {
		return nil
	}
	state, err := s.tmpDirStateLocked()
	if err != nil {
		return err
	}
	switch state {
	case tmpDirMissing:
		return s.reprovisionTmpDirLocked()
	case tmpDirForeign:
		return nil
	}
	if s.root != "" && safeDirectoryPath(s.root, s.tmpDir) != nil {
		return nil
	}
	entries, err := os.ReadDir(s.tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read sandbox tmp dir: %w", err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(s.tmpDir, e.Name())); err != nil {
			return fmt.Errorf("remove sandbox tmp entry: %w", err)
		}
	}
	return nil
}
