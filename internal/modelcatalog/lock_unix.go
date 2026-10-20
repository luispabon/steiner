//go:build unix

package modelcatalog

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func acquireFileLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		// The flock error is returned; a close failure here adds nothing.
		_ = file.Close()
		return nil, fmt.Errorf("flock: %w", err)
	}

	return func() {
		// Closing the fd also releases the flock, so a failed LOCK_UN is harmless.
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		// The release func has no error return, so a close failure has nowhere to go.
		_ = file.Close()
	}, nil
}
