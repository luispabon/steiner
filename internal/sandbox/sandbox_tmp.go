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
const tmpReprovisionMessage = "sandbox /tmp was lost and has been recreated; files written to it before the loss are gone"

// SetTmpReprovisionHandler registers a callback invoked at most once when the
// sandbox tmp directory is lost mid-session and a fresh directory is adopted in
// its place: either recreated after deletion or re-locked after a tool
// recreated the path.
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
// recovers it when it was lost. bindable is false when the path is not a
// directory this Sandbox may use, which callers must not bind. The warning is
// emitted after tmpMu is released.
func (s *Sandbox) ensureTmpDir() (bindable bool, err error) {
	s.tmpMu.Lock()
	bindable, recovered, err := s.ensureTmpDirLocked()
	tmpDir := s.tmpDir
	var notify func(string)
	if recovered && !s.tmpWarned {
		s.tmpWarned = true
		notify = s.tmpReprovisionHandler
	}
	s.tmpMu.Unlock()
	if recovered {
		slog.Warn("sandbox tmp dir re-provisioned", "path", tmpDir)
	}
	if notify != nil {
		notify(tmpReprovisionMessage)
	}
	return bindable, err
}

// ensureTmpDirLocked recovers a lost tmp directory and reports whether the
// directory may be bound. recovered is true when a fresh directory was adopted,
// which means previously written files are gone. The caller must hold tmpMu.
func (s *Sandbox) ensureTmpDirLocked() (bindable, recovered bool, err error) {
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
	case tmpDirReplaced:
		// A tool recreated the path, so its contents are new. Adopt the free
		// lock on the replacement instead of failing the session for good.
		if err := s.adoptTmpDirLockLocked(); err != nil {
			return false, false, err
		}
		return true, true, nil
	case tmpDirUnsafe:
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
	// os.SameFile compares device and inode. Reuse of an inode number after the
	// original path was deleted is a theoretical ambiguity; the lock just taken
	// on the path is the primary ownership signal, and this check only rejects a
	// path swapped between the open and the Lstat.
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
	tmpDirReplaced
	tmpDirUnsafe
)

// tmpDirStateLocked classifies s.tmpDir relative to the lock this Sandbox
// holds. The caller must hold tmpMu.
func (s *Sandbox) tmpDirStateLocked() (tmpDirState, error) {
	pathInfo, err := os.Lstat(s.tmpDir)
	if err != nil {
		if os.IsNotExist(err) {
			return tmpDirMissing, nil
		}
		return tmpDirUnsafe, fmt.Errorf("inspect sandbox tmp dir: %w", err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() {
		return tmpDirUnsafe, nil
	}
	if s.tmpLock == nil {
		// Without a lock there is no inode to compare against: an unprotected
		// or hand-built Sandbox owns any real directory at its tmpDir path.
		return tmpDirOwned, nil
	}
	lockedInfo, err := s.tmpLock.Stat()
	// Inode-number reuse after the original directory was deleted is a
	// theoretical ambiguity for os.SameFile; the held lock remains the primary
	// ownership signal.
	if err != nil || !os.SameFile(lockedInfo, pathInfo) {
		return tmpDirReplaced, nil
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
// liveness lock. A missing tmpDir is re-provisioned silently and an unlocked
// replacement is adopted first; a path this Sandbox cannot use is left
// untouched. No-op when tmpDir is empty.
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
	case tmpDirReplaced:
		if err := s.adoptTmpDirLockLocked(); err != nil {
			return err
		}
	case tmpDirUnsafe:
		slog.Warn("sandbox tmp dir reset skipped: unsafe path", "path", s.tmpDir)
		return nil
	}
	if s.root != "" && safeDirectoryPath(s.root, s.tmpDir) != nil {
		slog.Warn("sandbox tmp dir reset skipped: not a safe directory below the sandbox root", "path", s.tmpDir)
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
