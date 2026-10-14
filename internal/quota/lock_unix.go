//go:build unix

package quota

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// lockFile takes an exclusive flock on fd, blocking until it is available.
func lockFile(fd uintptr) error {
	if err := unix.Flock(int(fd), unix.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	return nil
}

// unlockFile releases the flock held on fd.
func unlockFile(fd uintptr) error {
	if err := unix.Flock(int(fd), unix.LOCK_UN); err != nil {
		return fmt.Errorf("funlock: %w", err)
	}
	return nil
}
