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

	count, err := CleanupOrphans(baseDir, 1*time.Hour)
	if err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}
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
	count, err := CleanupOrphans("/tmp/nonexistent-orphan-test-dir-zzz", 1*time.Hour)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}
