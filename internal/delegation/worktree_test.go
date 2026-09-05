package delegation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestProvisionCodeWorktree_UnbornHeadRequiresCommit(t *testing.T) {
	repo := t.TempDir()
	runCmd(t, repo, "git", "init")

	_, err := ProvisionCodeWorktree(context.Background(), repo, "unborn-agent")
	if !errors.Is(err, ErrCodeWorktreeRequiresCommit) {
		t.Fatalf("error = %v, want ErrCodeWorktreeRequiresCommit", err)
	}
	if !errors.Is(err, ErrWorktreeProvisioning) {
		t.Fatalf("error = %v, want ErrWorktreeProvisioning", err)
	}
}

func TestProvisionCodeWorktree_BranchesFromCurrentHead(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Commit a marker file on a non-default branch.
	runCmd(t, repo, "git", "checkout", "-b", "feature-branch")
	markerFile := filepath.Join(repo, "marker.txt")
	if err := os.WriteFile(markerFile, []byte("marker"), 0o644); err != nil {
		t.Fatalf("write marker file: %v", err)
	}
	runCmd(t, repo, "git", "add", "marker.txt")
	runCmd(t, repo, "git", "commit", "-m", "add marker")

	// Get the current HEAD commit hash for comparison.
	headCommit := runCmdOutput(t, repo, "git", "rev-parse", "HEAD")
	headCommit = strings.TrimSpace(headCommit)

	// Provision a worktree; it should branch from feature-branch's current HEAD.
	wt, err := ProvisionCodeWorktree(ctx, repo, "test-agent-1")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree failed: %v", err)
	}

	// Verify the worktree contains the marker file (proof it branched from feature-branch).
	markerInWT := filepath.Join(wt.Path, "marker.txt")
	if _, err := os.Stat(markerInWT); err != nil {
		t.Errorf("marker.txt not found in worktree: %v", err)
	}

	// Verify the worktree is on the correct branch.
	currentBranch := runCmdOutput(t, wt.Path, "git", "branch", "--show-current")
	if got := strings.TrimSpace(currentBranch); got != wt.Branch {
		t.Errorf("worktree branch mismatch: got %q, want %q", got, wt.Branch)
	}
	// Branch should start with delegate/ and contain test-agent-1.
	if got := wt.Branch; !strings.HasPrefix(got, "delegate/") || !strings.Contains(got, "test-agent-1") {
		t.Errorf("CodeWorktree.Branch format wrong: got %q, should start with 'delegate/' and contain 'test-agent-1'", got)
	}

	// Verify the worktree's HEAD is the same as the parent's.
	wtCommit := runCmdOutput(t, wt.Path, "git", "rev-parse", "HEAD")
	wtCommit = strings.TrimSpace(wtCommit)
	if wtCommit != headCommit {
		t.Errorf("worktree HEAD commit %q does not match parent %q", wtCommit, headCommit)
	}
}

func TestProvisionCodeWorktree_ConcurrentProvisioning(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	const n = 10
	results := make([]CodeWorktree, n)
	errs := make([]error, n)
	var wg sync.WaitGroup

	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			agentID := fmt.Sprintf("concurrent-agent-%d", i)
			wt, err := ProvisionCodeWorktree(ctx, repo, agentID)
			results[i] = wt
			errs[i] = err
		}()
	}
	wg.Wait()

	// Verify all succeeded.
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent provisioning %d failed: %v", i, err)
		}
	}

	// Verify each worktree has a unique path and correct branch.
	seen := make(map[string]struct{})
	for i, wt := range results {
		if wt.Path == "" {
			t.Errorf("result %d has empty Path", i)
			continue
		}
		if _, dup := seen[wt.Path]; dup {
			t.Errorf("duplicate worktree path: %s", wt.Path)
		}
		seen[wt.Path] = struct{}{}

		agentID := fmt.Sprintf("concurrent-agent-%d", i)
		if !strings.HasPrefix(wt.Branch, "delegate/") || !strings.Contains(wt.Branch, agentID) {
			t.Errorf("result %d branch format wrong: got %q, should start with 'delegate/' and contain %q", i, wt.Branch, agentID)
		}

		// Verify the worktree directory exists.
		if stat, err := os.Stat(wt.Path); err != nil {
			t.Errorf("result %d path stat failed: %v", i, err)
		} else if !stat.IsDir() {
			t.Errorf("result %d path is not a directory", i)
		}
	}

	// Verify git worktree list shows all N worktrees.
	list, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(list) != n {
		t.Errorf("ListCodeWorktrees returned %d worktrees, want %d", len(list), n)
	}
}

func TestProvisionCodeWorktree_FailureCleanup(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Point at a non-existent directory to trigger a git error.
	badRepo := filepath.Join(repo, "nonexistent")

	wt, err := ProvisionCodeWorktree(ctx, badRepo, "test-agent")
	if err == nil {
		t.Fatalf("ProvisionCodeWorktree should have failed for non-existent repo, got worktree: %v", wt)
	}

	// Verify the error wraps ErrWorktreeProvisioning.
	if !errors.Is(err, ErrWorktreeProvisioning) {
		t.Errorf("error does not wrap ErrWorktreeProvisioning: %v", err)
	}

	// Verify no half-created worktree is left in the repo's git metadata.
	// This is only testable if we had a valid repo to begin with, but we can
	// verify against the real repo that any provisioning attempt cleanup worked.
	list, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	for _, wt := range list {
		if strings.Contains(wt.Path, "test-agent") {
			t.Errorf("half-created worktree found in list after failure: %s", wt.Path)
		}
	}
}

func TestDirtyPaths_CleanRepo(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	paths, err := DirtyPaths(ctx, repo)
	if err != nil {
		t.Fatalf("DirtyPaths failed: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("DirtyPaths returned %d paths for clean repo, want 0: %v", len(paths), paths)
	}
}

func TestDirtyPaths_ModifiedAndUntracked(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a modified file (modify the initial commit).
	initialFile := filepath.Join(repo, "initial.txt")
	if err := os.WriteFile(initialFile, []byte("modified"), 0o644); err != nil {
		t.Fatalf("write modified file: %v", err)
	}

	// Create an untracked file.
	untrackedFile := filepath.Join(repo, "untracked.txt")
	if err := os.WriteFile(untrackedFile, []byte("untracked"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}

	paths, err := DirtyPaths(ctx, repo)
	if err != nil {
		t.Fatalf("DirtyPaths failed: %v", err)
	}

	// Should have both files in the paths.
	pathSet := make(map[string]struct{})
	for _, p := range paths {
		pathSet[p] = struct{}{}
	}

	if _, ok := pathSet["initial.txt"]; !ok {
		t.Errorf("initial.txt not found in dirty paths")
	}
	if _, ok := pathSet["untracked.txt"]; !ok {
		t.Errorf("untracked.txt not found in dirty paths")
	}

	if len(paths) != 2 {
		t.Errorf("DirtyPaths returned %d paths, want 2: %v", len(paths), paths)
	}
}

func TestListCodeWorktrees_Empty(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	worktrees, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(worktrees) != 0 {
		t.Errorf("ListCodeWorktrees returned %d worktrees, want 0", len(worktrees))
	}
}

func TestListCodeWorktrees_FiltersDelegationPathAndBranch(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Provision two delegation worktrees.
	_, err1 := ProvisionCodeWorktree(ctx, repo, "agent-1")
	_, err2 := ProvisionCodeWorktree(ctx, repo, "agent-2")
	if err1 != nil || err2 != nil {
		t.Fatalf("ProvisionCodeWorktree failed: %v, %v", err1, err2)
	}

	// Create a worktree outside the delegation path for comparison.
	otherPath := filepath.Join(repo, "other-worktree")
	if err := os.MkdirAll(otherPath, 0o755); err != nil {
		t.Fatalf("create other worktree dir: %v", err)
	}
	runCmd(t, repo, "git", "worktree", "add", "-b", "other-branch", otherPath, "HEAD")

	// Create a non-delegation worktree under .steiner/worktrees (e.g., oneshot-style).
	foreignPath := filepath.Join(repo, ".steiner", "worktrees", "foreign-oneshot-run")
	if err := os.MkdirAll(foreignPath, 0o755); err != nil {
		t.Fatalf("create foreign worktree dir: %v", err)
	}
	runCmd(t, repo, "git", "worktree", "add", "-b", "oneshot-run-1", foreignPath, "HEAD")

	// ListCodeWorktrees should only return the two delegation worktrees.
	worktrees, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(worktrees) != 2 {
		t.Errorf("ListCodeWorktrees returned %d worktrees, want 2 (excluding foreign)", len(worktrees))
	}

	// Verify both returned worktrees are delegation-owned.
	for _, wt := range worktrees {
		if !strings.HasPrefix(wt.Branch, "delegate/") {
			t.Errorf("worktree branch does not start with 'delegate/': %s", wt.Branch)
		}
	}
}

func TestListProcessCodeWorktrees_FiltersForeignProcess(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()
	t.Cleanup(resetProcessHashForTesting)

	foreign, err := ProvisionCodeWorktree(ctx, repo, "foreign-agent")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree foreign failed: %v", err)
	}
	resetProcessHashForTesting()
	current, err := ProvisionCodeWorktree(ctx, repo, "current-agent")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree current failed: %v", err)
	}

	worktrees, err := ListProcessCodeWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("ListProcessCodeWorktrees failed: %v", err)
	}
	if len(worktrees) != 1 {
		t.Fatalf("ListProcessCodeWorktrees returned %d worktrees, want 1", len(worktrees))
	}
	if worktrees[0] != current {
		t.Errorf("ListProcessCodeWorktrees returned %+v, want %+v", worktrees[0], current)
	}
	if worktrees[0] == foreign {
		t.Errorf("ListProcessCodeWorktrees returned foreign worktree %+v", worktrees[0])
	}
}

func TestPruneProcessCodeWorktrees_LeavesForeignProcess(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()
	t.Cleanup(resetProcessHashForTesting)

	foreign, err := ProvisionCodeWorktree(ctx, repo, "foreign-agent")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree foreign failed: %v", err)
	}
	resetProcessHashForTesting()
	currentOne, err := ProvisionCodeWorktree(ctx, repo, "current-agent-1")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree current one failed: %v", err)
	}
	currentTwo, err := ProvisionCodeWorktree(ctx, repo, "current-agent-2")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree current two failed: %v", err)
	}

	removedCount, err := PruneProcessCodeWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("PruneProcessCodeWorktrees failed: %v", err)
	}
	if removedCount != 2 {
		t.Fatalf("PruneProcessCodeWorktrees returned %d, want 2", removedCount)
	}

	worktrees, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees after prune failed: %v", err)
	}
	if len(worktrees) != 1 {
		t.Fatalf("ListCodeWorktrees after prune returned %d worktrees, want 1", len(worktrees))
	}
	if worktrees[0] != foreign {
		t.Errorf("remaining worktree is %+v, want foreign %+v", worktrees[0], foreign)
	}

	processWorktrees, err := ListProcessCodeWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("ListProcessCodeWorktrees after prune failed: %v", err)
	}
	if len(processWorktrees) != 0 {
		t.Errorf("ListProcessCodeWorktrees after prune returned %d worktrees, want 0", len(processWorktrees))
	}

	removedCount, err = PruneProcessCodeWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("PruneProcessCodeWorktrees empty failed: %v", err)
	}
	if removedCount != 0 {
		t.Errorf("PruneProcessCodeWorktrees empty returned %d, want 0", removedCount)
	}

	for _, current := range []CodeWorktree{currentOne, currentTwo} {
		if _, err := os.Stat(current.Path); !os.IsNotExist(err) {
			t.Errorf("pruned worktree path %q still exists", current.Path)
		}
	}
}

func TestPruneProcessCodeWorktrees_ContinuesAfterPruneError(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()
	t.Cleanup(resetProcessHashForTesting)

	first, err := ProvisionCodeWorktree(ctx, repo, "first")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree first failed: %v", err)
	}

	// Put a second worktree on the first worktree's branch. Pruning the first
	// removes it, but branch deletion fails while the second worktree uses it.
	secondPath := filepath.Join(repo, ".steiner", "worktrees", getProcessHash(), "main", "second")
	if err := os.MkdirAll(filepath.Dir(secondPath), 0o755); err != nil {
		t.Fatalf("create second worktree parent: %v", err)
	}
	runCmd(t, repo, "git", "worktree", "add", "--force", secondPath, first.Branch)

	removedCount, err := PruneProcessCodeWorktrees(ctx, repo)
	if err == nil {
		t.Fatal("PruneProcessCodeWorktrees returned nil error after a prune failure")
	}
	if !strings.Contains(err.Error(), "prune process worktrees") {
		t.Fatalf("PruneProcessCodeWorktrees error = %v, want process-prune context", err)
	}
	if removedCount != 1 {
		t.Fatalf("PruneProcessCodeWorktrees returned %d, want 1 successful prune", removedCount)
	}

	worktrees, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees after prune failed: %v", err)
	}
	if len(worktrees) != 0 {
		t.Fatalf("ListCodeWorktrees after prune returned %d worktrees, want 0", len(worktrees))
	}
	for _, path := range []string{first.Path, secondPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("pruned worktree path %q still exists", path)
		}
	}
}

func TestPruneCodeWorktree_RemovesWorktree(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Provision a worktree.
	wt, err := ProvisionCodeWorktree(ctx, repo, "prune-test")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree failed: %v", err)
	}

	// Verify it's in the list.
	list, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("before prune: ListCodeWorktrees returned %d, want 1", len(list))
	}

	// Extract the relID from the worktree path for pruning.
	delegationBase := filepath.Join(repo, ".steiner", "worktrees")
	relID, err := filepath.Rel(delegationBase, wt.Path)
	if err != nil {
		t.Fatalf("extract relID: %v", err)
	}

	// Prune it.
	removed, err := PruneCodeWorktree(ctx, repo, relID)
	if err != nil {
		t.Fatalf("PruneCodeWorktree failed: %v", err)
	}
	if !removed {
		t.Errorf("PruneCodeWorktree should report removed=true for existing worktree")
	}

	// Verify it's gone from the list.
	list, err = ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("after prune: ListCodeWorktrees returned %d, want 0", len(list))
	}

	// Verify the path no longer exists.
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree path still exists after prune")
	}
}

func TestPruneCodeWorktree_ToleratesMissing(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Attempt to prune a non-existent worktree (should not error).
	// Use a relID path structure (hash/branch/agentid).
	removed, err := PruneCodeWorktree(ctx, repo, "abcd1234/main/nonexistent")
	if err != nil {
		t.Errorf("PruneCodeWorktree should tolerate missing worktree, got error: %v", err)
	}
	if removed {
		t.Errorf("PruneCodeWorktree should report removed=false for non-existent worktree")
	}
}

func TestPruneCodeWorktree_RefusesForeignWorktree(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a foreign (non-delegation) worktree under .steiner/worktrees.
	foreignPath := filepath.Join(repo, ".steiner", "worktrees", "foreign-run")
	if err := os.MkdirAll(foreignPath, 0o755); err != nil {
		t.Fatalf("create foreign worktree dir: %v", err)
	}
	runCmd(t, repo, "git", "worktree", "add", "-b", "oneshot-run-1", foreignPath, "HEAD")

	// Attempt to prune it by directory name should fail.
	removed, err := PruneCodeWorktree(ctx, repo, "foreign-run")
	if err == nil {
		t.Fatalf("PruneCodeWorktree should refuse foreign worktree, got nil error")
	}
	if !errors.Is(err, ErrWorktreeNotDelegation) {
		t.Errorf("expected ErrWorktreeNotDelegation, got: %v", err)
	}
	if removed {
		t.Errorf("PruneCodeWorktree should report removed=false when refusing foreign worktree")
	}

	// Verify the foreign worktree's directory still exists.
	if _, err := os.Stat(foreignPath); os.IsNotExist(err) {
		t.Errorf("foreign worktree directory should still exist after refused prune")
	}

	// Verify it's still in git worktree list (not in delegation list).
	list, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	// Should not appear in delegation list.
	for _, wt := range list {
		if strings.Contains(wt.Path, "foreign-run") {
			t.Errorf("foreign worktree should not appear in ListCodeWorktrees")
		}
	}
}

func TestPruneAllCodeWorktrees_RemovesAll(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Provision three worktrees.
	const n = 3
	for i := 0; i < n; i++ {
		agentID := fmt.Sprintf("prune-all-agent-%d", i)
		_, err := ProvisionCodeWorktree(ctx, repo, agentID)
		if err != nil {
			t.Fatalf("ProvisionCodeWorktree failed: %v", err)
		}
	}

	// Verify they all exist.
	list, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(list) != n {
		t.Errorf("before prune-all: got %d worktrees, want %d", len(list), n)
	}

	// Prune all.
	removedCount, err := PruneAllCodeWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("PruneAllCodeWorktrees failed: %v", err)
	}
	if removedCount != n {
		t.Errorf("PruneAllCodeWorktrees returned removedCount=%d, want %d", removedCount, n)
	}

	// Verify all are gone.
	list, err = ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("after prune-all: got %d worktrees, want 0", len(list))
	}

	// Verify git worktree list in the source repo is clean.
	allWorktrees := runCmdOutput(t, repo, "git", "worktree", "list")
	lines := strings.Split(allWorktrees, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// The only entry should be the main worktree.
		if !strings.Contains(line, repo) {
			t.Errorf("unexpected worktree entry: %s", line)
		}
	}
}

func TestPruneAllCodeWorktrees_WithForeignWorktreePresent(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Provision two delegation worktrees.
	_, err1 := ProvisionCodeWorktree(ctx, repo, "delegate-1")
	_, err2 := ProvisionCodeWorktree(ctx, repo, "delegate-2")
	if err1 != nil || err2 != nil {
		t.Fatalf("ProvisionCodeWorktree failed: %v, %v", err1, err2)
	}

	// Create a foreign worktree under .steiner/worktrees.
	foreignPath := filepath.Join(repo, ".steiner", "worktrees", "oneshot-run-foreign")
	if err := os.MkdirAll(foreignPath, 0o755); err != nil {
		t.Fatalf("create foreign worktree dir: %v", err)
	}
	runCmd(t, repo, "git", "worktree", "add", "-b", "oneshot-run-1", foreignPath, "HEAD")

	// Verify initial state: 2 delegation + 1 foreign.
	list, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("before prune-all: expected 2 delegation worktrees, got %d", len(list))
	}

	// Prune all delegation worktrees.
	removedCount, err := PruneAllCodeWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("PruneAllCodeWorktrees failed: %v", err)
	}
	if removedCount != 2 {
		t.Errorf("PruneAllCodeWorktrees returned removedCount=%d, want 2", removedCount)
	}

	// Verify delegation worktrees are gone.
	list, err = ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("after prune-all: expected 0 delegation worktrees, got %d", len(list))
	}

	// Verify the foreign worktree still exists.
	if _, err := os.Stat(foreignPath); os.IsNotExist(err) {
		t.Errorf("foreign worktree directory should survive prune-all")
	}
}

func TestPruneAllCodeWorktrees_SkipsForeignWorktreeInDelegationPath(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Provision two delegation worktrees.
	_, err1 := ProvisionCodeWorktree(ctx, repo, "good-agent-1")
	_, err2 := ProvisionCodeWorktree(ctx, repo, "good-agent-2")
	if err1 != nil || err2 != nil {
		t.Fatalf("ProvisionCodeWorktree failed: %v, %v", err1, err2)
	}

	// Create a foreign (non-delegation) worktree under .steiner/worktrees.
	foreignPath := filepath.Join(repo, ".steiner", "worktrees", "foreign-oneshot")
	if err := os.MkdirAll(foreignPath, 0o755); err != nil {
		t.Fatalf("create foreign worktree dir: %v", err)
	}
	runCmd(t, repo, "git", "worktree", "add", "-b", "oneshot-run-1", foreignPath, "HEAD")

	// Verify we have 2 delegation + 1 foreign in the list initially.
	beforeList, err := listWorktreeEntries(context.Background(), repo)
	if err != nil {
		t.Fatalf("listWorktreeEntries failed: %v", err)
	}
	delegationCount := 0
	foreignFound := false
	for _, entry := range beforeList {
		if strings.HasPrefix(entry.Path, filepath.Join(repo, ".steiner", "worktrees")) {
			if strings.HasPrefix(entry.Branch, "delegate/") {
				delegationCount++
			} else if strings.Contains(entry.Path, "foreign-oneshot") {
				foreignFound = true
			}
		}
	}
	if delegationCount != 2 {
		t.Errorf("expected 2 delegation worktrees before prune, got %d", delegationCount)
	}
	if !foreignFound {
		t.Fatalf("foreign worktree not found in git list before prune")
	}

	// Prune all delegation worktrees.
	removedCount, err := PruneAllCodeWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("PruneAllCodeWorktrees failed: %v", err)
	}
	if removedCount != 2 {
		t.Errorf("PruneAllCodeWorktrees returned removedCount=%d, want 2", removedCount)
	}

	// Verify all delegation worktrees are gone.
	afterList, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(afterList) != 0 {
		t.Errorf("after prune-all: expected 0 delegation worktrees, got %d", len(afterList))
	}

	// Verify the foreign worktree's directory still exists.
	if _, err := os.Stat(foreignPath); os.IsNotExist(err) {
		t.Errorf("foreign worktree directory should survive prune-all")
	}

	// Verify the foreign worktree is still in git list (albeit not in ListCodeWorktrees).
	stillInGit, err := listWorktreeEntries(context.Background(), repo)
	if err != nil {
		t.Fatalf("listWorktreeEntries after prune failed: %v", err)
	}
	foreignStillExists := false
	for _, entry := range stillInGit {
		if strings.Contains(entry.Path, "foreign-oneshot") {
			foreignStillExists = true
			break
		}
	}
	if !foreignStillExists {
		t.Errorf("foreign worktree should still be in git list after prune-all")
	}
}

func TestProvisionCodeWorktree_CollisionFix(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Provision a worktree with agentID "child-1" in the first "process".
	wt1, err := ProvisionCodeWorktree(ctx, repo, "child-1")
	if err != nil {
		t.Fatalf("first ProvisionCodeWorktree failed: %v", err)
	}

	// Verify it's in the list.
	list1, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees after first provision failed: %v", err)
	}
	if len(list1) != 1 {
		t.Fatalf("expected 1 worktree after first provision, got %d", len(list1))
	}
	firstPath := list1[0].Path
	firstBranch := list1[0].Branch

	// Extract relID and prune it.
	delegationBase := filepath.Join(repo, ".steiner", "worktrees")
	relID1, err := filepath.Rel(delegationBase, wt1.Path)
	if err != nil {
		t.Fatalf("extract relID: %v", err)
	}

	removed, err := PruneCodeWorktree(ctx, repo, relID1)
	if err != nil {
		t.Fatalf("PruneCodeWorktree failed: %v", err)
	}
	if !removed {
		t.Errorf("PruneCodeWorktree should report removed=true for existing worktree")
	}

	// Verify it's gone.
	list, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees after prune failed: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 worktrees after prune, got %d", len(list))
	}

	// Simulate a new process by resetting the process hash.
	resetProcessHashForTesting()

	// Provision a worktree with the same agentID "child-1" in the "new process".
	// This should succeed without collision because the process hash is different.
	_, err = ProvisionCodeWorktree(ctx, repo, "child-1")
	if err != nil {
		t.Fatalf("second ProvisionCodeWorktree failed (this was the bug): %v", err)
	}

	// Verify it's in the list.
	list2, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees after second provision failed: %v", err)
	}
	if len(list2) != 1 {
		t.Fatalf("expected 1 worktree after second provision, got %d", len(list2))
	}
	secondPath := list2[0].Path
	secondBranch := list2[0].Branch

	// Verify the paths are different (different process hashes mean different paths).
	if firstPath == secondPath {
		t.Errorf("paths should be different due to different process hashes: %s == %s", firstPath, secondPath)
	}

	// Verify the branches are different (different process hashes mean different branches).
	if firstBranch == secondBranch {
		t.Errorf("branches should be different due to different process hashes: %s == %s", firstBranch, secondBranch)
	}

	// Both should start with "delegate/".
	if !strings.HasPrefix(firstBranch, "delegate/") {
		t.Errorf("first branch should start with 'delegate/': %s", firstBranch)
	}
	if !strings.HasPrefix(secondBranch, "delegate/") {
		t.Errorf("second branch should start with 'delegate/': %s", secondBranch)
	}
}

func TestProvisionCodeWorktree_SameBranchDifferentAgents(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Provision two worktrees with different agentIDs in the same process (same hash, same branch).
	wt1, err := ProvisionCodeWorktree(ctx, repo, "agent-1")
	if err != nil {
		t.Fatalf("first ProvisionCodeWorktree failed: %v", err)
	}

	wt2, err := ProvisionCodeWorktree(ctx, repo, "agent-2")
	if err != nil {
		t.Fatalf("second ProvisionCodeWorktree failed: %v", err)
	}

	// Verify both are in the list.
	list, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(list))
	}

	// Verify they have different paths (nested under same hash/branch but different agentID).
	if wt1.Path == wt2.Path {
		t.Errorf("worktree paths should be different: %s == %s", wt1.Path, wt2.Path)
	}

	// Verify they have different branches.
	if wt1.Branch == wt2.Branch {
		t.Errorf("worktree branches should be different: %s == %s", wt1.Branch, wt2.Branch)
	}

	// Both should be under the same hash/branch prefix (same process, same parent branch).
	// Extract the process hash and branch from the paths.
	// wt1.Path is like: <projectRoot>/.steiner/worktrees/<hash>/<branch>/agent-1
	// wt2.Path is like: <projectRoot>/.steiner/worktrees/<hash>/<branch>/agent-2
	rel1, _ := filepath.Rel(filepath.Join(repo, ".steiner", "worktrees"), wt1.Path)
	rel2, _ := filepath.Rel(filepath.Join(repo, ".steiner", "worktrees"), wt2.Path)

	// Both should have the same prefix (hash/branch), differing only in agentID.
	rel1Prefix := filepath.Dir(rel1)
	rel2Prefix := filepath.Dir(rel2)
	if rel1Prefix != rel2Prefix {
		t.Errorf("siblings should share hash/branch prefix: %s != %s", rel1Prefix, rel2Prefix)
	}
}

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal clean branch name",
			input:    "main",
			expected: "main",
		},
		{
			name:     "branch with spaces",
			input:    "feature branch name",
			expected: "feature_branch_name",
		},
		{
			name:     "branch with problematic characters",
			input:    "feat~issue^1:special?chars",
			expected: "feat_issue_1_special_chars",
		},
		{
			name:     "branch with square brackets",
			input:    "feature[abc]",
			expected: "feature_abc_",
		},
		{
			name:     "branch with asterisk",
			input:    "release*1.0",
			expected: "release_1.0",
		},
		{
			name:     "branch with consecutive slashes",
			input:    "feature//nested//branch",
			expected: "feature/nested/branch",
		},
		{
			name:     "branch with leading slashes",
			input:    "/leading/branch",
			expected: "leading/branch",
		},
		{
			name:     "branch with trailing slashes",
			input:    "trailing/branch/",
			expected: "trailing/branch",
		},
		{
			name:     "branch with double dots",
			input:    "feature..issue",
			expected: "feature_issue",
		},
		{
			name:     "branch with .lock suffix",
			input:    "feature.lock",
			expected: "feature",
		},
		{
			name:     "branch with nested slashes and spaces",
			input:    "feature / nested  branch",
			expected: "feature_/_nested__branch",
		},
		{
			name:     "detached (HEAD literal)",
			input:    "HEAD",
			expected: "HEAD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeBranchName(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeBranchName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestProvisionCodeWorktree_DetachedHead(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Get the current HEAD commit hash to detach to.
	currentHeadCommit := runCmdOutput(t, repo, "git", "rev-parse", "HEAD")
	currentHeadCommit = strings.TrimSpace(currentHeadCommit)

	// Detach HEAD by checking out the commit directly.
	runCmd(t, repo, "git", "checkout", currentHeadCommit)

	// Verify HEAD is detached.
	branchOutput := runCmdOutput(t, repo, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if strings.TrimSpace(branchOutput) != "HEAD" {
		t.Fatalf("HEAD should be detached, got: %s", branchOutput)
	}

	// Provision a worktree with detached HEAD.
	wt, err := ProvisionCodeWorktree(ctx, repo, "detached-agent")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree with detached HEAD failed: %v", err)
	}

	// Verify the worktree's path contains "detached" as the branch-name segment.
	// Path format: <root>/.steiner/worktrees/<hash>/detached/<agentID>
	rel, err := filepath.Rel(filepath.Join(repo, ".steiner", "worktrees"), wt.Path)
	if err != nil {
		t.Fatalf("extract relative path: %v", err)
	}

	pathParts := strings.Split(rel, string(filepath.Separator))
	if len(pathParts) < 2 {
		t.Fatalf("path has too few parts: %s", rel)
	}

	// The second part (index 1) should be "detached".
	if pathParts[1] != "detached" {
		t.Errorf("expected 'detached' in path segment 1, got %q in path %s", pathParts[1], rel)
	}

	// Verify the worktree's branch includes "detached".
	if !strings.Contains(wt.Branch, "detached") {
		t.Errorf("worktree branch should contain 'detached', got: %s", wt.Branch)
	}

	// Verify the worktree is usable (can run git commands).
	headInWT := runCmdOutput(t, wt.Path, "git", "rev-parse", "HEAD")
	headInWT = strings.TrimSpace(headInWT)
	if headInWT != currentHeadCommit {
		t.Errorf("worktree HEAD commit %q does not match parent %q", headInWT, currentHeadCommit)
	}

	// Verify it's in the delegation list.
	list, err := ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("ListCodeWorktrees failed: %v", err)
	}
	found := false
	for _, wt2 := range list {
		if wt2.Path == wt.Path {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("worktree not found in delegation list")
	}
}

func TestPruneCodeWorktree_RejectsPathTraversal(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create a canary directory outside the repo for testing.
	tmpDir := t.TempDir()
	canaryPath := filepath.Join(tmpDir, "canary-outside-repo")
	if err := os.MkdirAll(canaryPath, 0o755); err != nil {
		t.Fatalf("create canary dir: %v", err)
	}

	// Create a file in the canary directory to verify it's not deleted.
	canaryFile := filepath.Join(canaryPath, "canary.txt")
	if err := os.WriteFile(canaryFile, []byte("canary"), 0o644); err != nil {
		t.Fatalf("write canary file: %v", err)
	}

	// Construct a relID that contains ".." segments to try to escape.
	// If we join this with .steiner/worktrees, it should try to reach outside the repo.
	relID := "../../../" + strings.TrimPrefix(canaryPath, "/")

	// Attempt to prune with the path-traversal relID.
	removed, err := PruneCodeWorktree(ctx, repo, relID)
	if err == nil {
		t.Fatalf("PruneCodeWorktree should reject path traversal, got nil error")
	}

	// Verify it's the path-escape error.
	if !errors.Is(err, ErrWorktreePathEscape) {
		t.Errorf("expected ErrWorktreePathEscape, got: %v", err)
	}
	if removed {
		t.Errorf("PruneCodeWorktree should report removed=false when rejecting path traversal")
	}

	// Verify the canary file still exists (not deleted).
	if _, err := os.Stat(canaryFile); os.IsNotExist(err) {
		t.Errorf("canary file was deleted by path traversal!")
	}
}

func TestPruneCodeWorktree_RejectsRelIDWithDotDot(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Use a relID with .. segments that would escape the delegation directory.
	relID := "../../parent-dir-escape"

	// Attempt to prune with the escaping relID.
	removed, err := PruneCodeWorktree(ctx, repo, relID)
	if err == nil {
		t.Fatalf("PruneCodeWorktree should reject relID with .., got nil error")
	}

	// Verify it's the path-escape error.
	if !errors.Is(err, ErrWorktreePathEscape) {
		t.Errorf("expected ErrWorktreePathEscape, got: %v", err)
	}
	if removed {
		t.Errorf("PruneCodeWorktree should report removed=false when rejecting relID with ..")
	}
}

func TestPruneCodeWorktree_DeletesBranch(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Provision a worktree.
	wt, err := ProvisionCodeWorktree(ctx, repo, "branch-deletion-test")
	if err != nil {
		t.Fatalf("ProvisionCodeWorktree failed: %v", err)
	}

	// Extract the relID and branch name for verification.
	delegationBase := filepath.Join(repo, ".steiner", "worktrees")
	relID, err := filepath.Rel(delegationBase, wt.Path)
	if err != nil {
		t.Fatalf("extract relID: %v", err)
	}
	branchName := wt.Branch

	// Verify the branch exists before pruning.
	branchListOutput := runCmdOutput(t, repo, "git", "branch", "--list", branchName)
	if !strings.Contains(branchListOutput, branchName) {
		t.Errorf("branch %q should exist before prune, got: %q", branchName, branchListOutput)
	}

	// Prune the worktree.
	removed, err := PruneCodeWorktree(ctx, repo, relID)
	if err != nil {
		t.Fatalf("PruneCodeWorktree failed: %v", err)
	}
	if !removed {
		t.Errorf("PruneCodeWorktree should report removed=true for existing worktree")
	}

	// Verify the branch is gone after pruning.
	branchListOutput = runCmdOutput(t, repo, "git", "branch", "--list", branchName)
	if strings.TrimSpace(branchListOutput) != "" {
		t.Errorf("branch %q should not exist after prune, got: %q", branchName, branchListOutput)
	}
}

// Helper functions.

func setupTestRepo(t *testing.T) (string, func()) {
	tmpDir := t.TempDir()
	runCmd(t, tmpDir, "git", "init")
	runCmd(t, tmpDir, "git", "config", "user.email", "test@example.com")
	runCmd(t, tmpDir, "git", "config", "user.name", "Test User")

	// Create an initial commit.
	initialFile := filepath.Join(tmpDir, "initial.txt")
	if err := os.WriteFile(initialFile, []byte("initial"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	runCmd(t, tmpDir, "git", "add", "initial.txt")
	runCmd(t, tmpDir, "git", "commit", "-m", "initial commit")

	cleanup := func() {
		// Cleanup is automatic with t.TempDir().
	}
	return tmpDir, cleanup
}

func runCmd(t *testing.T, workDir string, args ...string) {
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
	cmd.Dir = workDir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("command %v failed: %v\nstderr: %s", args, err, stderr.String())
	}
}

func runCmdOutput(t *testing.T, workDir string, args ...string) string {
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
	cmd.Dir = workDir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("command %v failed: %v\nstderr: %s", args, err, stderr.String())
	}
	return stdout.String()
}
