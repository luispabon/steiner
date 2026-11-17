// Package filelock provides exclusive cross-process advisory locks for
// steiner's on-disk stores.
//
// A lock protects a data file through a stable sibling lock file, for example
// sessions-index.json.lock. The lock file is created with mode 0600 on Unix
// when missing. Windows uses the inherited directory ACL, not a restrictive ACL
// set by this package. The path is never deleted: data files are rewritten by
// rename, so the lock must live on a stable path.
//
// Unix targets other than aix use flock, and Windows uses LockFileEx. Elsewhere,
// and on filesystems that report locking as unsupported, Acquire degrades to an
// unlocked Lock instead of failing, and the caller inspects Lock.Unsupported to
// decide whether continuing without cross-process exclusion is acceptable.
//
// The package never writes warnings itself. Lock.Warning carries the message
// for every unsupported Lock, not once per process, so a caller that cannot
// surface it does not consume it for later callers; deduplicating repeated
// warnings belongs to the caller or its sink.
package filelock

import (
	"fmt"
	"os"
)

// Lock is an exclusive cross-process lock, or a placeholder reported through
// Unsupported when the platform or filesystem could not provide one. Release it
// when the protected work finishes.
//
// Lock holds its lock file descriptor until Release, so the kernel lock lasts
// exactly as long as the Lock value.
//
// A Lock is not reentrant: while one Lock holds a path, a second Acquire of the
// same path in the same process waits or fails, because flock binds to the open
// file description rather than the process. A Lock is also not safe for
// concurrent use: concurrent Release calls, or Release racing another Lock
// method, are unsupported and may report a close error, so release a Lock once,
// from the goroutine that acquired it, when the lock protects shared state.
type Lock struct {
	file        *os.File
	unlock      func() error
	unsupported bool
	warning     string
}

// Unsupported reports whether l holds no cross-process lock, so another process
// can modify the protected data concurrently.
func (l *Lock) Unsupported() bool { return l.unsupported }

// Warning returns the message a caller should route to its warning sink when
// Unsupported reports true. It is empty for a real lock.
//
// Warning is not consumed by reading it, and it repeats for every unsupported
// Lock, so a caller without a sink cannot hide the degradation from the next
// one. Callers that surface it own any deduplication.
func (l *Lock) Warning() string { return l.warning }

// Release unlocks when needed and closes its file descriptor. Closing the last
// descriptor also releases the kernel lock.
// Release is a no-op after the first call, and an unsupported Lock has nothing
// to release. Concurrent Release calls are not supported.
func (l *Lock) Release() error {
	if l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	var unlockErr error
	if l.unlock != nil {
		unlockErr = l.unlock()
		l.unlock = nil
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close lock file: %w", err)
	}
	if unlockErr != nil {
		return fmt.Errorf("unlock lock file: %w", unlockErr)
	}
	return nil
}

// unsupportedLock builds the placeholder Acquire returns when locking is
// unavailable. Every unsupported Lock carries its warning.
func unsupportedLock(path string, cause error) *Lock {
	return &Lock{
		unsupported: true,
		warning:     fmt.Sprintf("file lock %s unavailable: %v; continuing without cross-process exclusion", path, cause),
	}
}
