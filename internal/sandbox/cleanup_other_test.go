//go:build !linux

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupOrphans_RemovesNothingWithoutLocks(t *testing.T) {
	baseDir := t.TempDir()
	oldDir := filepath.Join(baseDir, "old")
	if err := os.Mkdir(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(oldDir, past, past); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}

	if got := CleanupOrphans(baseDir, time.Hour); got != 0 {
		t.Fatalf("CleanupOrphans on a platform without advisory locks = %d, want 0", got)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("expected the old dir to remain: %v", err)
	}
}
