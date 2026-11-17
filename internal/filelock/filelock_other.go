//go:build !unix || aix

package filelock

import (
	"context"
	"errors"
)

// errUnsupportedPlatform explains why Acquire cannot lock here.
var errUnsupportedPlatform = errors.New("platform has no flock")

// Acquire reports an unsupported Lock on platforms without flock, currently
// windows, aix and other non-unix targets. It creates no lock file and never
// fails: two such processes can modify the protected data concurrently, and the
// caller decides through Lock.Unsupported whether that is acceptable.
func Acquire(_ context.Context, path string) (*Lock, error) {
	return unsupportedLock(path, errUnsupportedPlatform), nil
}
