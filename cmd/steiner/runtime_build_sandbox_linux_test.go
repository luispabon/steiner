//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// lockDir takes an exclusive flock on a directory inode, matching what a live
// steiner runtime holds for its sandbox tmp dir.
func lockDir(t *testing.T, dir string) *os.File {
	t.Helper()
	file, err := os.OpenFile(dir, os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		t.Fatalf("open dir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("flock %s: %v", dir, err)
	}
	return file
}

func TestCleanupSandboxTmpOrphansKeepsLockedDir(t *testing.T) {
	parent := t.TempDir()
	locked := filepath.Join(parent, "locked")
	unlocked := filepath.Join(parent, "unlocked")
	for _, dir := range []string{locked, unlocked} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		past := time.Now().Add(-72 * time.Hour)
		if err := os.Chtimes(dir, past, past); err != nil {
			t.Fatalf("chtimes %s: %v", dir, err)
		}
	}
	lockDir(t, locked)

	cleanupSandboxTmpOrphans(parent, 48*time.Hour)

	if _, err := os.Stat(locked); err != nil {
		t.Errorf("expected the flocked dir of a live runtime to survive cleanup: %v", err)
	}
	if _, err := os.Stat(unlocked); !os.IsNotExist(err) {
		t.Errorf("expected the unlocked old dir to be removed, stat err=%v", err)
	}
}
