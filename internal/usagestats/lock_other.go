//go:build !unix

package usagestats

import "errors"

// errLockingUnsupported is returned by the non-unix locker so persistence fails
// safely instead of silently succeeding without cross-process locking.
var errLockingUnsupported = errors.New("usage stats file locking is unsupported on this platform")

// otherFileLocker is the fallback locker for platforms without a flock
// equivalent wired up (i.e. not unix). It cannot provide cross-process locking,
// so its lock reports an explicit error: a caller that ignored it would persist
// aggregate stats unlocked and risk clobbering concurrent writers. Persistence
// callers treat that error as "skip this write" rather than failing the run.
type otherFileLocker struct{}

func newFileLocker() fileLocker {
	return &otherFileLocker{}
}

func (*otherFileLocker) lock(uintptr) error {
	return errLockingUnsupported
}

func (*otherFileLocker) unlock(uintptr) error {
	return nil
}
