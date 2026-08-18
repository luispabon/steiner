package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/delegation"
)

func TestWorktreesListEmpty(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	cmd := newWorktreesCommand(&cliFlags{})
	cmd.SetArgs([]string{"--list"})

	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.Execute()
	if err != nil {
		t.Fatalf("command execution failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "no worktrees provisioned") {
		t.Errorf("expected 'no worktrees provisioned' in output, got: %q", output)
	}
}

func TestWorktreesListWithProvisioned(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()

	// Provision two worktrees.
	wt1, err := delegation.ProvisionCodeWorktree(ctx, repo, "test-agent-1")
	if err != nil {
		t.Fatalf("provision first worktree: %v", err)
	}

	wt2, err := delegation.ProvisionCodeWorktree(ctx, repo, "test-agent-2")
	if err != nil {
		t.Fatalf("provision second worktree: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	cmd := newWorktreesCommand(&cliFlags{})
	cmd.SetArgs([]string{"--list"})

	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.Execute()
	if err != nil {
		t.Fatalf("command execution failed: %v", err)
	}

	output := out.String()

	// Verify both agent IDs appear in the output.
	if !strings.Contains(output, "test-agent-1") {
		t.Errorf("expected 'test-agent-1' in output, got: %q", output)
	}
	if !strings.Contains(output, "test-agent-2") {
		t.Errorf("expected 'test-agent-2' in output, got: %q", output)
	}

	// Verify branches appear.
	if !strings.Contains(output, "delegate-test-agent-1") {
		t.Errorf("expected 'delegate-test-agent-1' branch in output, got: %q", output)
	}
	if !strings.Contains(output, "delegate-test-agent-2") {
		t.Errorf("expected 'delegate-test-agent-2' branch in output, got: %q", output)
	}

	// Verify paths appear.
	if !strings.Contains(output, wt1.Path) {
		t.Errorf("expected path %q in output, got: %q", wt1.Path, output)
	}
	if !strings.Contains(output, wt2.Path) {
		t.Errorf("expected path %q in output, got: %q", wt2.Path, output)
	}
}

func TestWorktreesPruneSingle(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()

	// Provision two worktrees.
	_, err := delegation.ProvisionCodeWorktree(ctx, repo, "test-agent-1")
	if err != nil {
		t.Fatalf("provision first worktree: %v", err)
	}

	_, err = delegation.ProvisionCodeWorktree(ctx, repo, "test-agent-2")
	if err != nil {
		t.Fatalf("provision second worktree: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	// Prune the first worktree.
	cmd := newWorktreesCommand(&cliFlags{})
	cmd.SetArgs([]string{"--prune", "test-agent-1"})

	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.Execute()
	if err != nil {
		t.Fatalf("command execution failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "removed worktree: test-agent-1") {
		t.Errorf("expected success message, got: %q", output)
	}

	// Verify only test-agent-2 remains.
	worktrees, err := delegation.ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("list worktrees after prune: %v", err)
	}

	if len(worktrees) != 1 {
		t.Errorf("expected 1 worktree remaining, got %d", len(worktrees))
	}

	if len(worktrees) > 0 && !strings.Contains(worktrees[0].Path, "test-agent-2") {
		t.Errorf("expected test-agent-2 to remain, got: %s", worktrees[0].Path)
	}
}

func TestWorktreesPruneAll(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()

	// Provision three worktrees.
	for i := 1; i <= 3; i++ {
		agentID := fmt.Sprintf("test-agent-%d", i)
		_, err := delegation.ProvisionCodeWorktree(ctx, repo, agentID)
		if err != nil {
			t.Fatalf("provision worktree %s: %v", agentID, err)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	// Prune all.
	cmd := newWorktreesCommand(&cliFlags{})
	cmd.SetArgs([]string{"--prune-all"})

	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.Execute()
	if err != nil {
		t.Fatalf("command execution failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "removed 3 worktree(s)") {
		t.Errorf("expected 'removed 3 worktree(s)' in output, got: %q", output)
	}

	// Verify no worktrees remain.
	worktrees, err := delegation.ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("list worktrees after prune-all: %v", err)
	}

	if len(worktrees) != 0 {
		t.Errorf("expected 0 worktrees remaining, got %d", len(worktrees))
	}
}

func TestWorktreesFlagValidation_ListAndPrune(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	cmd := newWorktreesCommand(&cliFlags{})
	cmd.SetArgs([]string{"--list", "--prune", "test-agent"})

	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when combining --list and --prune, got nil")
	}

	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected 'exactly one' error message, got: %v", err)
	}

	// Verify no action was taken (no worktrees exist).
	worktrees, err := delegation.ListCodeWorktrees(repo)
	if err != nil {
		t.Fatalf("list worktrees: %v", err)
	}
	if len(worktrees) != 0 {
		t.Errorf("expected no worktrees after failed command, got %d", len(worktrees))
	}
}

func TestWorktreesFlagValidation_PruneAndPruneAll(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	cmd := newWorktreesCommand(&cliFlags{})
	cmd.SetArgs([]string{"--prune", "test-agent", "--prune-all"})

	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when combining --prune and --prune-all, got nil")
	}

	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected 'exactly one' error message, got: %v", err)
	}
}

func TestWorktreesFlagValidation_NoFlags(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	cmd := newWorktreesCommand(&cliFlags{})
	cmd.SetArgs([]string{})

	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when no flags provided, got nil")
	}

	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected 'exactly one' error message, got: %v", err)
	}
}

func TestWorktreesListSingleWorktree(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()

	// Provision one worktree.
	_, err := delegation.ProvisionCodeWorktree(ctx, repo, "single-agent")
	if err != nil {
		t.Fatalf("provision worktree: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir to repo: %v", err)
	}

	cmd := newWorktreesCommand(&cliFlags{})
	cmd.SetArgs([]string{"--list"})

	var out bytes.Buffer
	cmd.SetOut(&out)

	err = cmd.Execute()
	if err != nil {
		t.Fatalf("command execution failed: %v", err)
	}

	output := out.String()

	// Verify the header is printed.
	if !strings.Contains(output, "Agent ID") {
		t.Errorf("expected header 'Agent ID' in output, got: %q", output)
	}

	// Verify the single worktree appears.
	if !strings.Contains(output, "single-agent") {
		t.Errorf("expected 'single-agent' in output, got: %q", output)
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
