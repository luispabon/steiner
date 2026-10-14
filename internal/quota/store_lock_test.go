package quota

import (
	"os"
	"testing"
)

// requireFileLocking skips the test when the platform cannot take the flock
// that Append relies on.
func requireFileLocking(t *testing.T) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "probe")
	if err != nil {
		t.Fatalf("create probe file: %v", err)
	}
	defer f.Close() //nolint:errcheck // Probe handle only; nothing is written.
	if err := lockFile(f.Fd()); err != nil {
		t.Skipf("file locking unavailable: %v", err)
	}
	_ = unlockFile(f.Fd())
}
