//go:build windows

package filelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

const retryInterval = 10 * time.Millisecond

type lockFileExFunc func(windows.Handle, uint32, uint32, uint32, uint32, *windows.Overlapped) error

// The persistent sibling lock file is created when missing with mode 0600, but
// Windows applies the inherited directory ACL. This package does not set a
// restrictive ACL. The lock file is never deleted.
//
// Acquire takes an exclusive LockFileEx lock on the fixed first byte of path.
func Acquire(ctx context.Context, path string) (*Lock, error) {
	return acquireWindows(ctx, path, windows.LockFileEx)
}

func acquireWindows(ctx context.Context, path string, lockFileEx lockFileExFunc) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	overlapped := &windows.Overlapped{}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close() // best-effort close after cancellation
			return nil, fmt.Errorf("wait for lock file: %w", err)
		}
		err := lockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, overlapped)
		if err == nil {
			return &Lock{file: file, unlock: func() error {
				return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
			}}, nil
		}
		if errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
			_ = file.Close() // best-effort close when locking is unsupported
			return unsupportedLock(path, err), nil
		}
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			select {
			case <-ctx.Done():
				_ = file.Close() // best-effort close after cancellation
				return nil, fmt.Errorf("wait for lock file: %w", ctx.Err())
			case <-time.After(retryInterval):
			}
			continue
		}
		_ = file.Close() // best-effort close after lock failure
		return nil, fmt.Errorf("lock file: %w", err)
	}
}
