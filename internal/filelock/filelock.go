// Package filelock provides exclusive cross-process advisory locks for
// steiner's on-disk stores.
//
// A lock protects a data file through a stable sibling lock file, for example
// sessions-index.json.lock. The lock file is created 0600 when missing and never
// deleted: the kernel lock binds to the inode opened at lock time, and data
// files are rewritten by rename, so the lock must live on a path whose inode
// does not change.
//
// Only unix targets other than aix take a real lock. Elsewhere, and on
// filesystems that report locking as unsupported, Acquire degrades to an
// unlocked Lock instead of failing, and the caller inspects Lock.Unsupported to
// decide whether continuing without cross-process exclusion is acceptable. The
// package never writes warnings itself.
package filelock

import (
	"fmt"
	"os"
	"sync/atomic"
)

// Lock is an exclusive cross-process lock, or a placeholder reported through
// Unsupported when the platform or filesystem could not provide one. Release it
// when the protected work finishes.
//
// Lock holds its lock file descriptor until Release, so the kernel lock lasts
// exactly as long as the Lock value. Locks are not reentrant across Lock values.
type Lock struct {
	file        *os.File
	unsupported bool
	warning     string
}

// warnedUnsupported records whether this process already reported an
// unsupported lock, so callers warn at most once.
var warnedUnsupported atomic.Bool

// Unsupported reports whether l holds no cross-process lock, so another process
// can modify the protected data concurrently.
func (l *Lock) Unsupported() bool { return l.unsupported }

// Warning returns the message a caller should route to its warning sink the
// first time this process could not lock. It is empty for a real lock and for
// later unsupported locks, so repeated store access cannot repeat the warning.
// The package never writes it: surfacing it is the caller's decision.
func (l *Lock) Warning() string { return l.warning }

// Release drops the lock and closes its file descriptor. Closing the last
// descriptor releases the kernel lock, so no explicit unlock call is needed.
// Release is a no-op after the first call, and an unsupported Lock has nothing
// to release.
func (l *Lock) Release() error {
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	if err := file.Close(); err != nil {
		return fmt.Errorf("close lock file: %w", err)
	}
	return nil
}

// unsupportedLock builds the placeholder Acquire returns when locking is
// unavailable, filling Warning only for the first such result in the process.
func unsupportedLock(path string, cause error) *Lock {
	lock := &Lock{unsupported: true}
	if warnedUnsupported.CompareAndSwap(false, true) {
		lock.warning = fmt.Sprintf("file lock %s unavailable: %v; continuing without cross-process exclusion", path, cause)
	}
	return lock
}
