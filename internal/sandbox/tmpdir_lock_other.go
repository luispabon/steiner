//go:build !linux

package sandbox

import (
	"os"
	"time"
)

// acquireTmpDirLock reports that advisory directory locks are unavailable, so
// the tmp directory is used without liveness protection.
func acquireTmpDirLock(dir string) (*os.File, error) {
	return nil, errTmpDirLockUnsupported
}

// removeDirIfUnlocked removes nothing: without a liveness probe an old
// directory cannot be distinguished from a live one.
func removeDirIfUnlocked(dir string, cutoff time.Time) bool {
	return false
}
