package builtin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writePrunableFile(t *testing.T, path string, size int, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func TestPruneFetchedDir_MissingDir(t *testing.T) {
	workDir := t.TempDir()
	removed, err := PruneFetchedDir(workDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestPruneFetchedDir_AgeOnly(t *testing.T) {
	workDir := t.TempDir()
	fetchedDir := filepath.Join(workDir, ".steiner", "tmp", "fetched")
	now := time.Now()

	oldFile := filepath.Join(fetchedDir, "old.txt")
	writePrunableFile(t, oldFile, 100, now.Add(-8*24*time.Hour))
	freshFile := filepath.Join(fetchedDir, "fresh.txt")
	writePrunableFile(t, freshFile, 100, now.Add(-time.Hour))

	removed, err := PruneFetchedDir(workDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertRemoved(t, oldFile)
	assertExists(t, freshFile)
}

func TestPruneFetchedDir_AgeAndBudget(t *testing.T) {
	workDir := t.TempDir()
	fetchedDir := filepath.Join(workDir, ".steiner", "tmp", "fetched")
	now := time.Now()

	oldFile := filepath.Join(fetchedDir, "old.txt")
	writePrunableFile(t, oldFile, 10, now.Add(-8*24*time.Hour))

	// Over-budget set: three 100-byte files, all older than the 1-hour
	// eviction floor but younger than 7 days, against a 250-byte budget.
	// The oldest of the three should be evicted to bring the total to 200.
	oldestBudget := filepath.Join(fetchedDir, "budget-oldest.bin")
	writePrunableFile(t, oldestBudget, 100, now.Add(-3*time.Hour))
	midBudget := filepath.Join(fetchedDir, "budget-mid.bin")
	writePrunableFile(t, midBudget, 100, now.Add(-2*time.Hour))
	newestBudget := filepath.Join(fetchedDir, "budget-newest.bin")
	writePrunableFile(t, newestBudget, 100, now.Add(-90*time.Minute))

	removed, err := pruneFetchedDir(workDir, now, 250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (1 old + 1 budget)", removed)
	}

	assertRemoved(t, oldFile)
	assertRemoved(t, oldestBudget)
	assertExists(t, midBudget)
	assertExists(t, newestBudget)
}

func TestPruneFetchedDir_MinAgeFloorSurvivesBudgetEviction(t *testing.T) {
	workDir := t.TempDir()
	fetchedDir := filepath.Join(workDir, ".steiner", "tmp", "fetched")
	now := time.Now()

	// Two 100-byte files against a 100-byte budget: over budget together,
	// but both younger than the 1-hour floor, so neither may be removed
	// even though the directory stays over budget.
	fileA := filepath.Join(fetchedDir, "a.bin")
	writePrunableFile(t, fileA, 100, now.Add(-10*time.Minute))
	fileB := filepath.Join(fetchedDir, "b.bin")
	writePrunableFile(t, fileB, 100, now.Add(-5*time.Minute))

	removed, err := pruneFetchedDir(workDir, now, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	assertExists(t, fileA)
	assertExists(t, fileB)
}

func TestPruneFetchedDir_SiblingDirsUntouched(t *testing.T) {
	workDir := t.TempDir()
	now := time.Now()

	imagesFile := filepath.Join(workDir, ".steiner", "tmp", "images", "old.png")
	writePrunableFile(t, imagesFile, 100, now.Add(-30*24*time.Hour))

	worktreeFile := filepath.Join(workDir, ".steiner", "worktrees", "wt1", "old.txt")
	writePrunableFile(t, worktreeFile, 100, now.Add(-30*24*time.Hour))

	fetchedOld := filepath.Join(workDir, ".steiner", "tmp", "fetched", "old.txt")
	writePrunableFile(t, fetchedOld, 100, now.Add(-30*24*time.Hour))

	removed, err := PruneFetchedDir(workDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (only fetched/old.txt)", removed)
	}
	assertExists(t, imagesFile)
	assertExists(t, worktreeFile)
	assertRemoved(t, fetchedOld)
}

func assertExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist, stat error: %v", path, err)
	}
}

func assertRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s to be removed, stat error: %v", path, err)
	}
}
