package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOrphans(t *testing.T) {
	baseDir := t.TempDir()

	oldDir := filepath.Join(baseDir, "old")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	freshDir := filepath.Join(baseDir, "fresh")
	if err := os.Mkdir(freshDir, 0o755); err != nil {
		t.Fatalf("mkdir fresh: %v", err)
	}

	// Set oldDir modtime to 2 hours ago.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldDir, past, past); err != nil {
		t.Fatalf("Chtimes old: %v", err)
	}

	count := CleanupOrphans(baseDir, 1*time.Hour)
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("expected oldDir to be removed")
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatal("expected freshDir to still exist")
	}
}

func TestCleanupOrphans_BaseDirNotExist(t *testing.T) {
	count := CleanupOrphans("/tmp/nonexistent-orphan-test-dir-zzz", 1*time.Hour)
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

func TestCleanupOrphans_ReadOnlyFile(t *testing.T) {
	baseDir := t.TempDir()

	oldDir := filepath.Join(baseDir, "old")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	roFile := filepath.Join(oldDir, "readonly")
	if err := os.WriteFile(roFile, []byte("data"), 0o444); err != nil {
		t.Fatalf("write readonly file: %v", err)
	}
	// Remove oldDir's own write bit so unlinking roFile requires the chmod
	// step in forceRemoveAll. POSIX unlink permission is governed by the
	// containing directory's write bit, not the target file's mode: a plain
	// os.RemoveAll would fail here with oldDir non-writable.
	if err := os.Chmod(oldDir, 0o555); err != nil {
		t.Fatalf("chmod old: %v", err)
	}

	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldDir, past, past); err != nil {
		t.Fatalf("Chtimes old: %v", err)
	}

	count := CleanupOrphans(baseDir, 1*time.Hour)
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("expected oldDir (with read-only file and non-writable dir) to be removed")
	}
}
