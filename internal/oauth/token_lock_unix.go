//go:build unix && !aix

package oauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// tokenLock holds the flock primitive, which tests replace to exercise
// filesystems that report flock as unsupported.
type tokenLock struct {
	flock func(fd, how int) error
}

// tokenLockRetryInterval is the pause between non-blocking flock attempts while
// another process holds the lock.
const tokenLockRetryInterval = 10 * time.Millisecond

// unsupportedFlock reports whether err means this platform or filesystem cannot
// hold an flock, for example a network filesystem without lock support.
func unsupportedFlock(err error) bool {
	return errors.Is(err, unix.ENOLCK) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP)
}

// acquireTokenLock takes an advisory exclusive flock on path+".lock", creating
// the lock file 0600 when needed. Acquisition never blocks in the kernel: it
// retries a non-blocking flock until it succeeds or ctx is done, so ctx's
// deadline bounds the wait. A blocking flock would instead leave the goroutine
// parked in the kernel past the refresh deadline. The returned release function
// is never nil on success.
//
// A filesystem that reports flock as unsupported proceeds without exclusion, the
// same fail-open behaviour token_lock_other.go documents for non-Unix
// platforms.
func acquireTokenLock(ctx context.Context, path string) (func(), error) {
	return tokenLock{flock: unix.Flock}.acquire(ctx, path)
}

func (l tokenLock) acquire(ctx context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open token lock file: %w", err)
	}
	fd := int(file.Fd())
	for {
		err := l.flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() {
				// Closing the fd also releases the flock, so a failed LOCK_UN is harmless.
				_ = l.flock(fd, unix.LOCK_UN)
				// The release func has no error return, so a close failure has nowhere to go.
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			if unsupportedFlock(err) {
				// No advisory lock is available here; the refresh still proceeds and
				// nothing excludes another process, the same limitation non-Unix
				// platforms have.
				return func() { _ = file.Close() }, nil
			}
			_ = file.Close()
			return nil, fmt.Errorf("lock token file: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("wait for token lock: %w", ctx.Err())
		case <-time.After(tokenLockRetryInterval):
		}
	}
}
