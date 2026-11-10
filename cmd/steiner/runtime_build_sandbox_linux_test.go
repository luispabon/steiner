//go:build linux

package main

import (
	"bytes"
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

func TestCreateSandboxTmpDirRetriesCollision(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, ".steiner", "tmp", "sandbox-tmp")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	// The collided ID belongs to another session: its contents must survive.
	existing := filepath.Join(parent, "0101010101010101")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatalf("mkdir existing: %v", err)
	}
	marker := filepath.Join(existing, "marker")
	if err := os.WriteFile(marker, []byte("live"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	restore := stubSandboxTmpRand(t, [][]byte{bytes.Repeat([]byte{0x01}, 8), bytes.Repeat([]byte{0x02}, 8)})
	defer restore()

	got, err := createSandboxTmpDir(parent)
	if err != nil {
		t.Fatalf("createSandboxTmpDir: %v", err)
	}
	want := filepath.Join(parent, "0202020202020202")
	if got != want {
		t.Fatalf("createSandboxTmpDir = %q, want %q", got, want)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected the collided directory to survive: %v", err)
	}
}

func TestCreateSandboxTmpDirGivesUpAfterCollisions(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, ".steiner", "tmp", "sandbox-tmp")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Mkdir(filepath.Join(parent, "0303030303030303"), 0o755); err != nil {
		t.Fatalf("mkdir colliding dir: %v", err)
	}

	restore := stubSandboxTmpRand(t, [][]byte{bytes.Repeat([]byte{0x03}, 8)})
	defer restore()

	if _, err := createSandboxTmpDir(parent); err == nil {
		t.Fatal("expected an error once every attempt collided")
	}
}

// stubSandboxTmpRand replaces the ID generator with a sequence of fixed IDs,
// repeating the last one once the sequence is exhausted.
func stubSandboxTmpRand(t *testing.T, ids [][]byte) func() {
	t.Helper()
	prev := sandboxTmpRandRead
	calls := 0
	sandboxTmpRandRead = func(b []byte) (int, error) {
		idx := calls
		if idx >= len(ids) {
			idx = len(ids) - 1
		}
		calls++
		return copy(b, ids[idx]), nil
	}
	return func() { sandboxTmpRandRead = prev }
}
