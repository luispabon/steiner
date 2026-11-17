//go:build unix && !aix

package filelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// retryInterval is the pause between non-blocking flock attempts while another
// process holds the lock.
const retryInterval = 10 * time.Millisecond

// flockFunc is the flock primitive, replaced in tests to exercise filesystems
// that report locking as unsupported.
type flockFunc func(fd, how int) error

// Acquire takes an exclusive advisory lock on path, creating the lock file with
// 0600 permissions when it is missing. The lock file is never deleted, so every
// cooperating process locks the same inode.
//
// Acquisition retries a non-blocking flock until it succeeds or ctx is done,
// because a blocking flock would park in the kernel past ctx's deadline.
// Acquire holds the lock file descriptor until Release.
//
// When the platform or filesystem reports locking as unsupported (ENOLCK,
// ENOTSUP, EOPNOTSUPP), Acquire returns an unsupported Lock and no error. Any
// other flock failure is returned.
func Acquire(ctx context.Context, path string) (*Lock, error) {
	return acquire(ctx, path, unix.Flock)
}

func acquire(ctx context.Context, path string, flock flockFunc) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	fd := int(file.Fd())
	for {
		err := flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &Lock{file: file}, nil
		}
		switch {
		case errors.Is(err, unix.ENOLCK), errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EOPNOTSUPP):
			_ = file.Close() // best-effort: the lock is unavailable either way
			return unsupportedLock(path, err), nil
		case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EAGAIN):
			// Another process holds the lock; retry until ctx says stop.
		default:
			_ = file.Close() // best-effort close on lock failure
			return nil, fmt.Errorf("lock file: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close() // best-effort close on lock failure
			return nil, fmt.Errorf("wait for lock file: %w", ctx.Err())
		case <-time.After(retryInterval):
		}
	}
}
