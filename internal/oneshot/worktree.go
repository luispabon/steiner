package oneshot

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const defaultWorktreeStartPoint = "origin/main"

// Worktree describes the provisioned checkout for a oneshot run.
type Worktree struct {
	Path       string
	BranchName string
	StartPoint string
}

// ProvisionWorktree creates or re-provisions the run worktree from origin/main.
func ProvisionWorktree(ctx context.Context, projectRoot string, identity RunIdentity) (Worktree, error) {
	if err := ensureSteinerProjectDir(projectRoot); err != nil {
		return Worktree{}, err
	}

	worktreePath := identity.WorktreePath(projectRoot)
	if err := prepareWorktreePath(ctx, projectRoot, worktreePath); err != nil {
		return Worktree{}, err
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return Worktree{}, fmt.Errorf("create worktree parent directory: %w", err)
	}

	branchName := identity.BranchName()
	if err := addWorktree(ctx, projectRoot, worktreePath, branchName); err != nil {
		if retryErr := recoverWorktreePath(ctx, projectRoot, identity, err); retryErr != nil {
			return Worktree{}, retryErr
		}
	}

	if err := verifyWorktree(ctx, worktreePath, branchName); err != nil {
		return Worktree{}, err
	}

	if err := ensureSteinerProjectDir(worktreePath); err != nil {
		return Worktree{}, err
	}

	return Worktree{
		Path:       worktreePath,
		BranchName: branchName,
		StartPoint: defaultWorktreeStartPoint,
	}, nil
}

// CleanupWorktree prunes worktree metadata, removes the checkout, and removes stale admin dirs.
func CleanupWorktree(ctx context.Context, projectRoot string, identity RunIdentity) error {
	worktreePath := identity.WorktreePath(projectRoot)
	if err := runGit(ctx, projectRoot, "worktree", "prune"); err != nil {
		return err
	}
	if err := runGit(ctx, projectRoot, "worktree", "remove", "--force", worktreePath); err != nil {
		if !isGitWorktreeRemovalMissingPath(err) {
			return err
		}
	}
	if err := os.RemoveAll(worktreePath); err != nil {
		return fmt.Errorf("remove worktree path: %w", err)
	}
	if err := removeWorktreeAdminDir(ctx, projectRoot, identity); err != nil {
		return err
	}
	return nil
}

func prepareWorktreePath(ctx context.Context, projectRoot, worktreePath string) error {
	if err := runGit(ctx, projectRoot, "worktree", "prune"); err != nil {
		return err
	}
	if err := removeWorktreeAdminDirFromPath(ctx, projectRoot, worktreePath); err != nil {
		return err
	}
	if err := os.RemoveAll(worktreePath); err != nil {
		return fmt.Errorf("clear worktree path: %w", err)
	}
	return nil
}

func recoverWorktreePath(ctx context.Context, projectRoot string, identity RunIdentity, addErr error) error {
	if err := runGit(ctx, projectRoot, "worktree", "prune"); err != nil {
		return fmt.Errorf("prune worktree metadata after add failure: %w (original add error: %s)", err, addErr.Error())
	}
	if err := removeWorktreeAdminDir(ctx, projectRoot, identity); err != nil {
		return fmt.Errorf("remove stale worktree admin dir after add failure: %w (original add error: %s)", err, addErr.Error())
	}
	if err := os.RemoveAll(identity.WorktreePath(projectRoot)); err != nil {
		return fmt.Errorf("remove stale worktree path after add failure: %w (original add error: %s)", err, addErr.Error())
	}

	if retryErr := addWorktree(ctx, projectRoot, identity.WorktreePath(projectRoot), identity.BranchName()); retryErr != nil {
		return fmt.Errorf("add worktree after cleanup: %w (original add error: %s)", retryErr, addErr.Error())
	}
	return nil
}

func verifyWorktree(ctx context.Context, worktreePath, wantBranch string) error {
	if stat, err := os.Stat(worktreePath); err != nil {
		return fmt.Errorf("verify worktree path: %w", err)
	} else if !stat.IsDir() {
		return fmt.Errorf("verify worktree path: %s is not a directory", worktreePath)
	}

	out, err := gitOutput(ctx, worktreePath, "branch", "--show-current")
	if err != nil {
		return err
	}
	gotBranch := strings.TrimSpace(out)
	if gotBranch != wantBranch {
		return fmt.Errorf("verify worktree branch: got %q, want %q", gotBranch, wantBranch)
	}

	return nil
}

func runGit(ctx context.Context, workDir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workDir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return nil
}

func gitOutput(ctx context.Context, workDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workDir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return stdout.String(), nil
}

func removeWorktreeAdminDir(ctx context.Context, projectRoot string, identity RunIdentity) error {
	commonDir, err := gitOutput(ctx, projectRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	return removeWorktreeAdminDirAt(projectRoot, strings.TrimSpace(commonDir), identity.ID)
}

func addWorktree(ctx context.Context, projectRoot, worktreePath, branchName string) error {
	if branchExists(ctx, projectRoot, branchName) {
		return runGit(ctx, projectRoot, "worktree", "add", worktreePath, branchName)
	}
	return runGit(ctx, projectRoot, "worktree", "add", "-b", branchName, worktreePath, defaultWorktreeStartPoint)
}

func branchExists(ctx context.Context, projectRoot, branchName string) bool {
	if _, err := gitOutput(ctx, projectRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branchName); err == nil {
		return true
	}
	return false
}

func removeWorktreeAdminDirFromPath(ctx context.Context, projectRoot, worktreePath string) error {
	id := filepath.Base(worktreePath)
	commonDir, err := gitOutput(ctx, projectRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	return removeWorktreeAdminDirAt(projectRoot, strings.TrimSpace(commonDir), strings.TrimPrefix(id, "oneshot-"))
}

func removeWorktreeAdminDirAt(projectRoot, commonDir, id string) error {
	adminDir := commonDir
	if !filepath.IsAbs(adminDir) {
		adminDir = filepath.Join(projectRoot, adminDir)
	}
	adminDir = filepath.Join(adminDir, "worktrees", "oneshot-"+id)
	if err := os.RemoveAll(adminDir); err != nil {
		return fmt.Errorf("remove worktree admin dir: %w", err)
	}
	return nil
}

func isGitWorktreeRemovalMissingPath(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not a working tree") || strings.Contains(msg, "is not a working tree") || strings.Contains(msg, "is not a valid working tree")
}
