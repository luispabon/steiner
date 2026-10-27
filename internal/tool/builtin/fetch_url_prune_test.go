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

func TestPruneToolArtifacts_MissingDir(t *testing.T) {
	workDir := t.TempDir()
	removed, err := PruneToolArtifacts([]string{workDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestPruneToolArtifacts_AgeOnly(t *testing.T) {
	workDir := t.TempDir()
	fetchedDir := filepath.Join(workDir, ".steiner", "tmp", "fetched")
	now := time.Now()

	oldFile := filepath.Join(fetchedDir, "old.txt")
	writePrunableFile(t, oldFile, 100, now.Add(-8*24*time.Hour))
	freshFile := filepath.Join(fetchedDir, "fresh.txt")
	writePrunableFile(t, freshFile, 100, now.Add(-time.Hour))

	removed, err := PruneToolArtifacts([]string{workDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertRemoved(t, oldFile)
	assertExists(t, freshFile)
}

func TestPruneToolArtifacts_AgeAndBudget(t *testing.T) {
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

	removed, err := pruneToolArtifacts([]string{workDir}, now, 250)
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

func TestPruneToolArtifacts_MinAgeFloorSurvivesBudgetEviction(t *testing.T) {
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

	removed, err := pruneToolArtifacts([]string{workDir}, now, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	assertExists(t, fileA)
	assertExists(t, fileB)
}

func TestPruneToolArtifacts_SiblingDirsUntouched(t *testing.T) {
	workDir := t.TempDir()
	now := time.Now()

	imagesFile := filepath.Join(workDir, ".steiner", "tmp", "images", "old.png")
	writePrunableFile(t, imagesFile, 100, now.Add(-30*24*time.Hour))

	worktreeFile := filepath.Join(workDir, ".steiner", "worktrees", "wt1", "old.txt")
	writePrunableFile(t, worktreeFile, 100, now.Add(-30*24*time.Hour))

	fetchedOld := filepath.Join(workDir, ".steiner", "tmp", "fetched", "old.txt")
	writePrunableFile(t, fetchedOld, 100, now.Add(-30*24*time.Hour))

	removed, err := PruneToolArtifacts([]string{workDir})
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

func TestPruneToolArtifacts_SharedBudgetAcrossDirsAndRoots(t *testing.T) {
	projectRoot := t.TempDir()
	workDir := t.TempDir()
	now := time.Now()

	fetched := filepath.Join(projectRoot, ".steiner", "tmp", "fetched", "f.bin")
	bashOld := filepath.Join(workDir, ".steiner", "tmp", "bash-output", "o.log")
	bashMid := filepath.Join(workDir, ".steiner", "tmp", "bash-output", "m.log")
	young := filepath.Join(projectRoot, ".steiner", "tmp", "bash-output", "young.log")
	stale := filepath.Join(workDir, ".steiner", "tmp", "bash-output", "stale.log")
	writePrunableFile(t, bashOld, 100, now.Add(-5*time.Hour))
	writePrunableFile(t, bashMid, 100, now.Add(-4*time.Hour))
	writePrunableFile(t, fetched, 100, now.Add(-3*time.Hour))
	writePrunableFile(t, young, 100, now.Add(-time.Minute))
	writePrunableFile(t, stale, 1, now.Add(-8*24*time.Hour))

	removed, err := pruneToolArtifacts([]string{projectRoot, workDir, projectRoot}, now, 250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 3 {
		t.Errorf("removed = %d, want 3 (stale + 2 oldest)", removed)
	}
	assertRemoved(t, stale)
	assertRemoved(t, bashOld)
	assertRemoved(t, bashMid)
	assertExists(t, fetched)
	assertExists(t, young)
}

func TestPruneToolArtifacts_DuplicateRootsCountOnce(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	a := filepath.Join(root, ".steiner", "tmp", "fetched", "a.bin")
	b := filepath.Join(root, ".steiner", "tmp", "fetched", "b.bin")
	writePrunableFile(t, a, 100, now.Add(-3*time.Hour))
	writePrunableFile(t, b, 100, now.Add(-2*time.Hour))

	// Counted twice (400 > 250) would evict; once (200) must not.
	removed, err := pruneToolArtifacts([]string{root, root + string(filepath.Separator), ""}, now, 250)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	assertExists(t, a)
	assertExists(t, b)
}

func TestPruneToolArtifacts_SymlinksNotFollowed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)

	outsideFile := filepath.Join(outside, "victim.txt")
	writePrunableFile(t, outsideFile, 100, old)

	// File symlink inside fetched pointing outside.
	fetchedDir := filepath.Join(root, ".steiner", "tmp", "fetched")
	writePrunableFile(t, filepath.Join(fetchedDir, "keep.txt"), 1, now)
	link := filepath.Join(fetchedDir, "link.txt")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// Directory symlink inside fetched pointing outside.
	if err := os.Symlink(outside, filepath.Join(fetchedDir, "dirlink")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// bash-output itself a symlink to an outside dir.
	bashDir := filepath.Join(root, ".steiner", "tmp", "bash-output")
	if err := os.Symlink(outside, bashDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	removed, err := pruneToolArtifacts([]string{root}, now, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	assertExists(t, outsideFile)
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("symlink should remain: %v", err)
	}
}

func TestPruneToolArtifacts_OtherCachesUntouched(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	images := filepath.Join(root, ".steiner", "tmp", "images", "a.png")
	wt := filepath.Join(root, ".steiner", "worktrees", "w", "a.txt")
	bash := filepath.Join(root, ".steiner", "tmp", "bash-output", "a.log")
	writePrunableFile(t, images, 10, old)
	writePrunableFile(t, wt, 10, old)
	writePrunableFile(t, bash, 10, old)

	removed, err := PruneToolArtifacts([]string{root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertRemoved(t, bash)
	assertExists(t, images)
	assertExists(t, wt)
}

func TestPruneToolArtifacts_SwapAfterCollectDoesNotRedirectDeletion(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)

	fetchedDir := filepath.Join(root, ".steiner", "tmp", "fetched")
	inCache := filepath.Join(fetchedDir, "victim.txt")
	writePrunableFile(t, inCache, 10, old)
	outsideFile := filepath.Join(outside, "victim.txt")
	writePrunableFile(t, outsideFile, 10, old)

	files, closeAll, errs := collectToolArtifacts([]string{root})
	defer closeAll()
	if len(errs) != 0 || len(files) != 1 {
		t.Fatalf("collect = %d files, errs %v; want 1 file", len(files), errs)
	}

	// Swap the cache directory for a symlink to an outside directory.
	moved := fetchedDir + ".moved"
	if err := os.Rename(fetchedDir, moved); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := os.Symlink(outside, fetchedDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	removed, evictErrs := evictArtifacts(files, now, fetchedBudgetBytes)
	if len(evictErrs) != 0 {
		t.Fatalf("unexpected errors: %v", evictErrs)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertExists(t, outsideFile)
	assertRemoved(t, filepath.Join(moved, "victim.txt"))
}

func TestPruneToolArtifacts_SymlinkedIntermediateSkippedOtherRootsPruned(t *testing.T) {
	bad := t.TempDir()
	good := t.TempDir()
	outside := t.TempDir()
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)

	// bad/.steiner/tmp is a symlink to an outside tree holding a fetched dir.
	outsideFile := filepath.Join(outside, "fetched", "victim.txt")
	writePrunableFile(t, outsideFile, 10, old)
	if err := os.MkdirAll(filepath.Join(bad, ".steiner"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(bad, ".steiner", "tmp")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	goodFile := filepath.Join(good, ".steiner", "tmp", "fetched", "old.txt")
	writePrunableFile(t, goodFile, 10, old)

	removed, err := pruneToolArtifacts([]string{bad, good}, now, fetchedBudgetBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertExists(t, outsideFile)
	assertRemoved(t, goodFile)
}

func TestPruneToolArtifacts_NestedDirsIgnoredFlatFilesPruned(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	cache := filepath.Join(root, ".steiner", "tmp", "bash-output")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	// Register unwinding before building anything so a partial build still
	// unwinds. RemoveAll recurses with one fd per level and fails under low
	// fd limits.
	t.Cleanup(func() { unwindNestedDirs(cwd, cache) })
	// The deep path may exceed PATH_MAX, so build it incrementally.
	if err := os.Chdir(cache); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(cwd) }()
	for i := 0; i < 300; i++ {
		if err := os.Mkdir("d", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir("d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("nested.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes("nested.txt", old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}

	flat := filepath.Join(cache, "flat.log")
	writePrunableFile(t, flat, 10, old)

	removed, err := PruneToolArtifacts([]string{root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (flat file only)", removed)
	}
	assertRemoved(t, flat)
	assertExists(t, filepath.Join(cache, "d"))
}

// unwindNestedDirs removes a chain of single-child "d" directories under
// cache, however deep it got, one level at a time, then restores cwd.
func unwindNestedDirs(cwd, cache string) {
	defer func() { _ = os.Chdir(cwd) }()
	if os.Chdir(cache) != nil {
		return
	}
	depth := 0
	for os.Chdir("d") == nil {
		depth++
	}
	_ = os.Remove("nested.txt")
	for ; depth > 0; depth-- {
		if os.Chdir("..") != nil {
			return
		}
		_ = os.Remove("d")
	}
}

func TestUnwindNestedDirs_PartialChain(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	if err := os.Chdir(cache); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := os.Mkdir("d", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir("d"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	unwindNestedDirs(cwd, cache)
	if _, err := os.Lstat(filepath.Join(cache, "d")); !os.IsNotExist(err) {
		t.Errorf("expected chain removed, lstat err: %v", err)
	}
	if got, _ := os.Getwd(); got != cwd {
		t.Errorf("cwd = %q, want %q", got, cwd)
	}
}

func TestPruneToolArtifacts_ZeroByteNotEvictedAndRemovedCountExact(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	dir := filepath.Join(root, ".steiner", "tmp", "fetched")
	zero := filepath.Join(dir, "zero")
	big := filepath.Join(dir, "big")
	writePrunableFile(t, zero, 0, now.Add(-5*time.Hour))
	writePrunableFile(t, big, 100, now.Add(-2*time.Hour))

	removed, err := pruneToolArtifacts([]string{root}, now, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertExists(t, zero)
	assertRemoved(t, big)

	// A file already gone by eviction time is not counted.
	files, closeAll, errs := collectToolArtifacts([]string{root})
	defer closeAll()
	if len(errs) != 0 || len(files) != 1 {
		t.Fatalf("collect = %d files, errs %v", len(files), errs)
	}
	if err := os.Remove(zero); err != nil {
		t.Fatal(err)
	}
	removed, evictErrs := evictArtifacts(files, now.Add(30*24*time.Hour), fetchedBudgetBytes)
	if len(evictErrs) != 0 || removed != 0 {
		t.Errorf("removed = %d, errs %v; want 0, none", removed, evictErrs)
	}
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
