package delegation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ErrWorktreeProvisioning is a sentinel error for worktree provisioning failures.
var ErrWorktreeProvisioning = errors.New("git worktree provisioning failed")

// worktreeMu serializes concurrent git worktree add calls against the same .git
// metadata store to avoid index-lock races.
var worktreeMu sync.Mutex

// CodeWorktree describes the provisioned checkout for a delegation run.
type CodeWorktree struct {
	Path   string
	Branch string
}

// ProvisionCodeWorktree provisions a new code worktree for the given agentID,
// branching from the current HEAD. It holds worktreeMu for the entire
// provisioning and verification critical section to serialize concurrent
// git worktree add calls.
func ProvisionCodeWorktree(ctx context.Context, projectRoot, agentID string) (CodeWorktree, error) {
	worktreeMu.Lock()
	defer worktreeMu.Unlock()

	worktreePath := filepath.Join(projectRoot, ".steiner", "worktrees", agentID)
	branchName := "delegate-" + agentID

	// Prune stale metadata and remove any existing path at this location.
	if err := runGit(ctx, projectRoot, "worktree", "prune"); err != nil {
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	// Remove stale admin dir under the common .git dir.
	if err := removeWorktreeAdminDirForAgentID(ctx, projectRoot, agentID); err != nil {
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	// Remove any stale checkout path.
	if err := os.RemoveAll(worktreePath); err != nil {
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	// Create the worktree, branching from current HEAD.
	if err := runGit(ctx, projectRoot, "worktree", "add", "-b", branchName, worktreePath, "HEAD"); err != nil {
		// Best-effort cleanup on failure.
		_ = runGit(ctx, projectRoot, "worktree", "prune")
		_ = removeWorktreeAdminDirForAgentID(ctx, projectRoot, agentID)
		_ = os.RemoveAll(worktreePath)
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	// Verify the worktree was created correctly.
	if err := verifyCodeWorktree(ctx, worktreePath, branchName); err != nil {
		// Best-effort cleanup on failure.
		_ = runGit(ctx, projectRoot, "worktree", "prune")
		_ = removeWorktreeAdminDirForAgentID(ctx, projectRoot, agentID)
		_ = os.RemoveAll(worktreePath)
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	return CodeWorktree{
		Path:   worktreePath,
		Branch: branchName,
	}, nil
}

// DirtyPaths returns the list of modified/untracked paths in the project,
// by parsing git status --porcelain. Returns an empty slice if the tree is clean.
func DirtyPaths(ctx context.Context, projectRoot string) ([]string, error) {
	out, err := gitOutput(ctx, projectRoot, "status", "--porcelain")
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, line := range strings.Split(out, "\n") {
		// Do NOT TrimSpace here; porcelain format has status prefix that matters.
		if len(line) == 0 {
			continue
		}
		// Strip the 2-character porcelain status prefix and the following space.
		if len(line) > 3 {
			paths = append(paths, line[3:])
		}
	}
	return paths, nil
}

// ListCodeWorktrees lists all provisioned code worktrees under
// projectRoot/.steiner/worktrees, parsing git worktree list --porcelain.
func ListCodeWorktrees(projectRoot string) ([]CodeWorktree, error) {
	out, err := gitOutput(context.Background(), projectRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	delegationPath := filepath.Join(projectRoot, ".steiner", "worktrees")
	var worktrees []CodeWorktree

	lines := strings.Split(out, "\n")
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		i++

		if line == "" {
			continue
		}

		// Parse "worktree <path>" line format.
		parts := strings.Fields(line)
		if len(parts) < 2 || parts[0] != "worktree" {
			continue
		}

		worktreePath := parts[1]

		// Only include worktrees under .steiner/worktrees.
		if !strings.HasPrefix(worktreePath, delegationPath) {
			// Skip all lines until the next empty line (end of this worktree entry).
			for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
				i++
			}
			continue
		}

		// Extract branch name from following lines (skip HEAD line, look for branch line).
		branch := ""
		for i < len(lines) {
			nextLine := strings.TrimSpace(lines[i])
			if nextLine == "" {
				// End of this worktree entry.
				i++
				break
			}
			if strings.HasPrefix(nextLine, "branch ") {
				// Extract the branch ref and get just the branch name.
				ref := strings.TrimPrefix(nextLine, "branch ")
				// ref is typically "refs/heads/branch-name", extract just the branch name.
				if strings.HasPrefix(ref, "refs/heads/") {
					branch = strings.TrimPrefix(ref, "refs/heads/")
				} else {
					branch = ref
				}
				i++
				break
			}
			i++
		}

		if branch != "" {
			worktrees = append(worktrees, CodeWorktree{
				Path:   worktreePath,
				Branch: branch,
			})
		}
	}

	return worktrees, nil
}

// PruneCodeWorktree removes the code worktree for the given agentID.
// It tolerates "not a working tree" errors as already-removed.
func PruneCodeWorktree(ctx context.Context, projectRoot, agentID string) error {
	worktreePath := filepath.Join(projectRoot, ".steiner", "worktrees", agentID)

	// Attempt to remove via git worktree remove.
	err := runGit(ctx, projectRoot, "worktree", "remove", "--force", worktreePath)
	if err != nil && !isGitWorktreeRemovalMissingPath(err) {
		return err
	}

	// Remove stale checkout path.
	if err := os.RemoveAll(worktreePath); err != nil {
		return fmt.Errorf("remove worktree path: %w", err)
	}

	// Remove stale admin dir under the common .git dir.
	if err := removeWorktreeAdminDirForAgentID(ctx, projectRoot, agentID); err != nil {
		return err
	}

	return nil
}

// PruneAllCodeWorktrees prunes all code worktrees under projectRoot/.steiner/worktrees,
// collecting errors with errors.Join rather than stopping at the first failure.
// It discovers worktrees both via git worktree list and by scanning the filesystem,
// to handle corrupted worktrees whose admin dirs have been removed.
func PruneAllCodeWorktrees(ctx context.Context, projectRoot string) error {
	// First, prune all worktrees known to git.
	worktrees, err := ListCodeWorktrees(projectRoot)
	if err != nil {
		return err
	}

	seenAgentIDs := make(map[string]struct{})
	var errs []error
	for _, wt := range worktrees {
		agentID := filepath.Base(wt.Path)
		seenAgentIDs[agentID] = struct{}{}
		if err := PruneCodeWorktree(ctx, projectRoot, agentID); err != nil {
			errs = append(errs, err)
		}
	}

	// Also scan the filesystem for worktrees that git no longer knows about
	// (e.g., due to missing or corrupted admin dirs).
	delegationPath := filepath.Join(projectRoot, ".steiner", "worktrees")
	entries, err := os.ReadDir(delegationPath)
	if err != nil {
		// If the directory doesn't exist, that's fine.
		if !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	} else {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			agentID := entry.Name()
			if _, seen := seenAgentIDs[agentID]; seen {
				continue
			}
			// Attempt to prune this undiscovered worktree.
			if err := PruneCodeWorktree(ctx, projectRoot, agentID); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// Internal helpers.

func verifyCodeWorktree(ctx context.Context, worktreePath, wantBranch string) error {
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

func removeWorktreeAdminDirForAgentID(ctx context.Context, projectRoot, agentID string) error {
	commonDir, err := gitOutput(ctx, projectRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}

	adminDir := strings.TrimSpace(commonDir)
	if !filepath.IsAbs(adminDir) {
		adminDir = filepath.Join(projectRoot, adminDir)
	}
	adminDir = filepath.Join(adminDir, "worktrees", agentID)

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
