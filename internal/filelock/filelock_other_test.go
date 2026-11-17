//go:build !unix || aix

package filelock

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireUnsupportedOnThisPlatform(t *testing.T) {
	resetUnsupportedWarning(t)
	path := filepath.Join(t.TempDir(), "index.json.lock")
	lock, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire() error = %v, want nil on a platform without flock", err)
	}
	if !lock.Unsupported() {
		t.Error("Unsupported() = false, want true on a platform without flock")
	}
	if got := lock.Warning(); got == "" {
		t.Error("Warning() = \"\", want a message on the first unsupported lock")
	}
	if err := lock.Release(); err != nil {
		t.Errorf("Release() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock file created on an unsupported platform: %v", err)
	}

	again, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("second Acquire() error = %v", err)
	}
	if !again.Unsupported() {
		t.Error("second Unsupported() = false, want true")
	}
	if got := again.Warning(); got != "" {
		t.Errorf("second Warning() = %q, want empty once the process already warned", got)
	}
	if err := again.Release(); err != nil {
		t.Errorf("Release() error = %v", err)
	}
}

// resetUnsupportedWarning clears the process-wide once-per-process state so a
// test can observe the first unsupported warning, and restores it afterwards.
func resetUnsupportedWarning(t *testing.T) {
	t.Helper()
	warnedUnsupported.Store(false)
	t.Cleanup(func() { warnedUnsupported.Store(false) })
}
