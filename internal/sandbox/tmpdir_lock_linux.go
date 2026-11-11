//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// acquireTmpDirLock takes a non-blocking exclusive flock on the directory inode
// itself and returns the open file that holds it. Closing the file releases the
// lock, and the kernel drops it when the process dies, so a crashed runtime
// cannot leave its tmp directory permanently unremovable. os.OpenFile sets
// O_CLOEXEC, so wrapped commands never inherit the lock.
func acquireTmpDirLock(dir string) (*os.File, error) {
	file, err := os.OpenFile(dir, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, fmt.Errorf("open sandbox tmp dir: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		// Every failure path must release the descriptor it just opened.
		_ = file.Close()
		return nil, tmpDirLockError(err)
	}
	return file, nil
}

// tmpDirLockError classifies a flock failure. A busy lock means a live owner.
// Every other failure is reported as unsupported so a filesystem that cannot
// provide the lock degrades to unprotected sandboxing instead of failing
// runtime setup. Cleanup probes are conservative for the same reason: a
// directory whose lock cannot be probed is never removed.
func tmpDirLockError(err error) error {
	switch {
	case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN):
		return errTmpDirLockBusy
	case errors.Is(err, unix.ENOLCK), errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EOPNOTSUPP):
		return errTmpDirLockUnsupported
	default:
		return fmt.Errorf("%w: %w", errTmpDirLockUnsupported, err)
	}
}

// removeDirIfUnlocked removes dir when its liveness lock is free and it is
// still the directory that was just locked and still older than cutoff. A held
// lock means a live runtime owns dir; an unprobeable lock means liveness cannot
// be ruled out. Either way the directory is left alone.
func removeDirIfUnlocked(dir string, cutoff time.Time) bool {
	lock, err := tmpDirLockAcquire(dir)
	if err != nil {
		// ENOENT (already gone), a busy lock, and unsupported or unexpected
		// probe failures are all no-ops.
		return false
	}
	defer func() {
		// Closing the descriptor releases the flock. The verdict is already
		// recorded, so a close error has nothing left to report.
		_ = lock.Close()
	}()

	lockedInfo, err := lock.Stat()
	if err != nil || !lockedInfo.IsDir() || !lockedInfo.ModTime().Before(cutoff) {
		return false
	}
	pathInfo, err := os.Lstat(dir)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(lockedInfo, pathInfo) {
		return false
	}
	return forceRemoveAll(dir) == nil
}
