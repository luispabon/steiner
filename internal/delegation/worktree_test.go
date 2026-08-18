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
	if got := wt.Branch; got != "delegate-test-agent-1" {
		t.Errorf("CodeWorktree.Branch mismatch: got %q, want delegate-test-agent-1", got)
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
		expectedBranch := "delegate-" + agentID
		if wt.Branch != expectedBranch {
			t.Errorf("result %d branch mismatch: got %q, want %q", i, wt.Branch, expectedBranch)
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
		if !strings.HasPrefix(wt.Branch, "delegate-") {
			t.Errorf("worktree branch does not start with 'delegate-': %s", wt.Branch)
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

	// Prune it.
	if err := PruneCodeWorktree(ctx, repo, "prune-test"); err != nil {
		t.Fatalf("PruneCodeWorktree failed: %v", err)
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
	err := PruneCodeWorktree(ctx, repo, "nonexistent")
	if err != nil {
		t.Errorf("PruneCodeWorktree should tolerate missing worktree, got error: %v", err)
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
	err := PruneCodeWorktree(ctx, repo, "foreign-run")
	if err == nil {
		t.Fatalf("PruneCodeWorktree should refuse foreign worktree, got nil error")
	}
	if !errors.Is(err, ErrWorktreeNotDelegation) {
		t.Errorf("expected ErrWorktreeNotDelegation, got: %v", err)
	}

	// Verify the foreign worktree's directory still exists.
	if _, err := os.Stat(foreignPath); os.IsNotExist(err) {
		t.Errorf("foreign worktree directory should still exist after refused prune")
	}

	// Verify it's still in git worktree list.
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
	if err := PruneAllCodeWorktrees(ctx, repo); err != nil {
		t.Fatalf("PruneAllCodeWorktrees failed: %v", err)
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
	if err := PruneAllCodeWorktrees(ctx, repo); err != nil {
		t.Fatalf("PruneAllCodeWorktrees failed: %v", err)
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
			if strings.HasPrefix(entry.Branch, "delegate-") {
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
	err = PruneAllCodeWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("PruneAllCodeWorktrees failed: %v", err)
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
