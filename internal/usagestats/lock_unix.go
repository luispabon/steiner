//go:build unix

package usagestats

import (
	"fmt"

	"golang.org/x/sys/unix"
)

type unixFileLocker struct{}

func newFileLocker() fileLocker {
	return &unixFileLocker{}
}

func (*unixFileLocker) lock(fd uintptr) error {
	if err := unix.Flock(int(fd), unix.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	return nil
}

func (*unixFileLocker) unlock(fd uintptr) error {
	if err := unix.Flock(int(fd), unix.LOCK_UN); err != nil {
		return fmt.Errorf("funlock: %w", err)
	}
	return nil
}
