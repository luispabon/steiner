//go:build unix && !aix

package filelock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestAcquireSerializesIndependentHandles also covers non-reentrancy: a second
// Lock for the same path in this process cannot pass the first.
func TestAcquireSerializesIndependentHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json.lock")
	holder, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if holder.Unsupported() {
		t.Skip("platform cannot lock files")
	}
	defer func() {
		if err := holder.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	contender, err := Acquire(ctx, path)
	if err == nil {
		if err := contender.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
		t.Fatal("Acquire() while another handle holds the lock returned no error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Acquire() error = %v, want context.DeadlineExceeded while the lock is held", err)
	}

	if err := holder.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	after, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire() after release error = %v", err)
	}
	if after.Unsupported() {
		t.Error("Acquire() after release reported an unsupported lock")
	}
	if err := after.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestAcquireWaitsForHolderRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json.lock")
	holder, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if holder.Unsupported() {
		t.Skip("platform cannot lock files")
	}

	acquired := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		lock, err := Acquire(ctx, path)
		if err != nil {
			acquired <- err
			return
		}
		acquired <- lock.Release()
	}()

	time.Sleep(50 * time.Millisecond)
	if err := holder.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := <-acquired; err != nil {
		t.Fatalf("waiting Acquire() failed: %v", err)
	}
}

func TestAcquireCreatesRestrictiveStableLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json.lock")
	lock, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if lock.Unsupported() {
		t.Skip("platform cannot lock files")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("lock file mode = %o, want 600", got)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file removed by Release: %v", err)
	}
}

func TestReleaseTwiceIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json.lock")
	lock, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("second Release() error = %v, want nil", err)
	}
}

func TestAcquireDegradesWhenLockingUnsupported(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "no locks available", err: unix.ENOLCK},
		{name: "operation not supported", err: unix.EOPNOTSUPP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "index.json.lock")
			flock := func(int, int) error { return tt.err }

			for attempt := 1; attempt <= 2; attempt++ {
				lock, err := acquire(context.Background(), path, flock)
				if err != nil {
					t.Fatalf("acquire() attempt %d error = %v, want no error for an unsupported lock", attempt, err)
				}
				if !lock.Unsupported() {
					t.Errorf("attempt %d Unsupported() = false, want true", attempt)
				}
				warning := lock.Warning()
				if warning == "" {
					t.Fatalf("attempt %d Warning() = \"\", want a message for every unsupported lock", attempt)
				}
				if !strings.Contains(warning, path) {
					t.Errorf("attempt %d Warning() = %q, want it to name %q", attempt, warning, path)
				}
				if err := lock.Release(); err != nil {
					t.Errorf("attempt %d Release() error = %v", attempt, err)
				}
			}
		})
	}
}

func TestWarningSurvivesAnUnreadUnsupportedLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json.lock")
	flock := func(int, int) error { return unix.EOPNOTSUPP }

	// First caller acquires an unsupported lock and cannot surface it.
	first, err := acquire(context.Background(), path, flock)
	if err != nil {
		t.Fatalf("first acquire() error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Errorf("first Release() error = %v", err)
	}

	second, err := acquire(context.Background(), path, flock)
	if err != nil {
		t.Fatalf("second acquire() error = %v", err)
	}
	defer func() {
		if err := second.Release(); err != nil {
			t.Errorf("second Release() error = %v", err)
		}
	}()
	if second.Warning() == "" {
		t.Error("second Warning() = \"\", want the message even though the first caller never read it")
	}
}

func TestWarningEmptyForHeldLock(t *testing.T) {
	lock, err := Acquire(context.Background(), filepath.Join(t.TempDir(), "index.json.lock"))
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() {
		if err := lock.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
	}()
	if lock.Unsupported() {
		t.Skip("platform cannot lock files")
	}
	if got := lock.Warning(); got != "" {
		t.Errorf("Warning() = %q, want empty for a held lock", got)
	}
}

func TestAcquireReturnsOtherLockErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json.lock")
	lock, err := acquire(context.Background(), path, func(int, int) error { return unix.EIO })
	if !errors.Is(err, unix.EIO) {
		t.Fatalf("acquire() error = %v, want it to wrap EIO", err)
	}
	if lock != nil {
		t.Errorf("acquire() lock = %v, want nil on error", lock)
	}
}

func TestAcquireReportsOpenErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "index.json.lock")
	lock, err := Acquire(context.Background(), path)
	if err == nil {
		t.Fatal("Acquire() error = nil, want an error for a missing parent directory")
	}
	if lock != nil {
		t.Errorf("Acquire() lock = %v, want nil on error", lock)
	}
}
