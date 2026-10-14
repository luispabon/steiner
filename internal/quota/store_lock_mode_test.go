package quota

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAppendTightensLockFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not enforced on Windows")
	}
	requireFileLocking(t)
	path := filepath.Join(t.TempDir(), "quota-history.json")
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatalf("create lock file: %v", err)
	}
	if err := os.Chmod(lockPath, 0o644); err != nil {
		t.Fatalf("loosen lock file: %v", err)
	}

	if err := NewStore(path).Append(sampleSnapshot("a", testBase, 10), testBase); err != nil {
		t.Fatalf("Append: %v", err)
	}
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("lock file mode = %o, want 600", perm)
	}
}
