//go:build !unix || aix

package filelock

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireUnsupportedOnThisPlatform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json.lock")
	for attempt := 1; attempt <= 2; attempt++ {
		lock, err := Acquire(context.Background(), path)
		if err != nil {
			t.Fatalf("attempt %d Acquire() error = %v, want nil on a platform without flock", attempt, err)
		}
		if !lock.Unsupported() {
			t.Errorf("attempt %d Unsupported() = false, want true on a platform without flock", attempt)
		}
		if got := lock.Warning(); got == "" {
			t.Errorf("attempt %d Warning() = \"\", want a message for every unsupported lock", attempt)
		}
		if err := lock.Release(); err != nil {
			t.Errorf("attempt %d Release() error = %v", attempt, err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock file created on an unsupported platform: %v", err)
	}
}
