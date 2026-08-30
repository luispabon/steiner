package oneshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvisionWorktreeAndCleanup(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}

	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed: %v", err)
	}

	if got, want := worktree.Path, identity.WorktreePath(projectRoot); got != want {
		t.Fatalf("worktree path = %q, want %q", got, want)
	}
	if got, want := worktree.BranchName, identity.BranchName(); got != want {
		t.Fatalf("branch name = %q, want %q", got, want)
	}

	if stat, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("worktree path missing: %v", err)
	} else if !stat.IsDir() {
		t.Fatalf("worktree path is not a dir: %s", worktree.Path)
	}

	out := mustGitOutput(t, worktree.Path, "branch", "--show-current")
	if got := strings.TrimSpace(out); got != identity.BranchName() {
		t.Fatalf("branch = %q, want %q", got, identity.BranchName())
	}
	originMain := strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "origin/main"))
	if got := worktree.StartPoint; got != originMain {
		t.Fatalf("worktree start point = %q, want origin/main %q", got, originMain)
	}

	if _, err := os.Stat(filepath.Join(worktree.Path, ".steiner", ".gitignore")); err != nil {
		t.Fatalf(".steiner scaffolding missing: %v", err)
	}

	if err := CleanupWorktree(context.Background(), projectRoot, identity); err != nil {
		t.Fatalf("CleanupWorktree failed: %v", err)
	}

	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists after cleanup: %v", err)
	}
}

func TestProvisionWorktreeUsesLocalHeadWithoutRemote(t *testing.T) {
	projectRoot := setupLocalGitRepo(t)
	identity := RunIdentity{ID: "local123", Slug: "local-only"}

	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed: %v", err)
	}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	projectHead := strings.TrimSpace(mustGitOutput(t, projectRoot, "rev-parse", "HEAD"))
	if got := worktree.StartPoint; got != projectHead {
		t.Fatalf("worktree start point = %q, want project HEAD %q", got, projectHead)
	}
	if got := strings.TrimSpace(mustGitOutput(t, worktree.Path, "rev-parse", "HEAD")); got != projectHead {
		t.Fatalf("worktree HEAD = %q, want project HEAD %q", got, projectHead)
	}
}

func TestProvisionWorktreeReprovisionsAfterAdminDirPrune(t *testing.T) {
	projectRoot := setupGitRepo(t)
	identity := RunIdentity{ID: "abc123", Slug: "build-parser"}

	adminDir := filepath.Join(projectRoot, ".git", "worktrees", "oneshot-"+identity.ID)
	if err := os.MkdirAll(adminDir, 0o755); err != nil {
		t.Fatalf("make fake admin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "dummy"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("seed fake admin dir: %v", err)
	}

	worktree, err := ProvisionWorktree(context.Background(), projectRoot, identity)
	if err != nil {
		t.Fatalf("ProvisionWorktree failed: %v", err)
	}
	t.Cleanup(func() {
		_ = CleanupWorktree(context.Background(), projectRoot, identity)
	})

	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("worktree path missing after reprovision: %v", err)
	}
	if _, err := os.Stat(filepath.Join(adminDir, "dummy")); !os.IsNotExist(err) {
		t.Fatalf("stale admin dir payload still exists after reprovision: %v", err)
	}
}

func setupLocalGitRepo(t *testing.T) string {
	t.Helper()

	projectRoot := t.TempDir()
	runGitTest(t, projectRoot, "init", "-b", "main")
	runGitTest(t, projectRoot, "config", "user.name", "Steiner Test")
	runGitTest(t, projectRoot, "config", "user.email", "steiner@example.com")
	if err := os.WriteFile(filepath.Join(projectRoot, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitTest(t, projectRoot, "add", "README.md")
	runGitTest(t, projectRoot, "commit", "-m", "initial")

	return projectRoot
}

func setupGitRepo(t *testing.T) string {
	t.Helper()

	projectRoot := setupLocalGitRepo(t)
	bareRemote := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(bareRemote, 0o755); err != nil {
		t.Fatalf("create bare remote dir: %v", err)
	}
	runGitTest(t, bareRemote, "init", "--bare")
	runGitTest(t, projectRoot, "remote", "add", "origin", bareRemote)

	runGitTest(t, projectRoot, "push", "-u", "origin", "main")

	return projectRoot
}

func mustGitOutput(t *testing.T, workDir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", workDir}, args...)...) //nolint:noctx
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func runGitTest(t *testing.T, workDir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", workDir}, args...)...) //nolint:noctx
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
