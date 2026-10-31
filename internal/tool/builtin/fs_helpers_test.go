package builtin

import (
	"os"
	"runtime"
	"testing"
)

// mustMkdir creates dir and any missing parents.
func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

// mustSymlink creates link pointing at target. Windows only grants symlink
// creation with developer mode or elevation, so a refused link skips the
// calling test there instead of failing it.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unsupported: %v", err)
		}
		t.Fatal(err)
	}
}
