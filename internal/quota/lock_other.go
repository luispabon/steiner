//go:build !unix

package quota

import "errors"

// errLockingUnsupported is returned on platforms without a flock equivalent.
// Append fails instead of writing unlocked, which could clobber a concurrent
// writer's snapshots.
var errLockingUnsupported = errors.New("quota store file locking is unsupported on this platform")

func lockFile(uintptr) error {
	return errLockingUnsupported
}

func unlockFile(uintptr) error {
	return nil
}
