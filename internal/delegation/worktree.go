package delegation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ErrWorktreeProvisioning is a sentinel error for worktree provisioning failures.
var ErrWorktreeProvisioning = errors.New("git worktree provisioning failed")

// ErrWorktreeNotDelegation indicates an attempt to prune a worktree that is not owned by delegation.
var ErrWorktreeNotDelegation = errors.New("worktree is not delegation-owned")

// worktreeMu serializes concurrent git worktree add calls against the same .git
// metadata store to avoid index-lock races.
var worktreeMu sync.Mutex

// CodeWorktree describes the provisioned checkout for a delegation run.
type CodeWorktree struct {
	Path   string
	Branch string
}

// getParentBranchName returns the current branch of the parent repo, or "detached" if HEAD is detached.
func getParentBranchName(ctx context.Context, projectRoot string) (string, error) {
	out, err := gitOutput(ctx, projectRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "HEAD" {
		branch = "detached"
	}
	return branch, nil
}

// sanitizeBranchName removes or replaces characters that are unsafe in git branch names
// or filesystem paths. It preserves "/" for nested namespacing.
func sanitizeBranchName(name string) string {
	// Git disallows these characters in branch names:
	// space, ~, ^, :, ?, *, [, leading/trailing /, consecutive /, trailing .lock, ..
	// Also disallow backslash for cross-platform safety.
	// We'll replace these with underscores, and strip leading/trailing slashes.

	// Replace problematic characters with underscores.
	name = regexp.MustCompile(`[~^:?\[\]\\*\x00]`).ReplaceAllString(name, "_")

	// Replace spaces with underscores.
	name = strings.ReplaceAll(name, " ", "_")

	// Remove leading/trailing slashes.
	name = strings.Trim(name, "/")

	// Replace consecutive slashes with a single slash.
	name = regexp.MustCompile(`/+`).ReplaceAllString(name, "/")

	// Remove trailing .lock suffix (not strictly necessary but defensive).
	name = strings.TrimSuffix(name, ".lock")

	// Handle double-dot path component edge case.
	name = strings.ReplaceAll(name, "..", "_")

	return name
}

// ProvisionCodeWorktree provisions a new code worktree for the given agentID,
// branching from the current HEAD. It holds worktreeMu for the entire
// provisioning and verification critical section to serialize concurrent
// git worktree add calls. The worktree path and branch are derived from the
// process hash, parent branch name, and agentID to ensure collision-free
// isolation across process restarts.
func ProvisionCodeWorktree(ctx context.Context, projectRoot, agentID string) (CodeWorktree, error) {
	worktreeMu.Lock()
	defer worktreeMu.Unlock()

	// Derive the parent branch name and process hash for collision-free identity.
	parentBranch, err := getParentBranchName(ctx, projectRoot)
	if err != nil {
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}
	sanitizedBranch := sanitizeBranchName(parentBranch)
	processHash := getProcessHash()

	// Construct the nested worktree path and branch name.
	relID := filepath.Join(processHash, sanitizedBranch, agentID)
	worktreePath := filepath.Join(projectRoot, ".steiner", "worktrees", relID)
	branchName := "delegate/" + strings.ReplaceAll(relID, string(filepath.Separator), "/")

	// Prune stale metadata and remove any existing path at this location.
	if err := runGit(ctx, projectRoot, "worktree", "prune"); err != nil {
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	// Remove stale admin dir under the common .git dir.
	if err := removeWorktreeAdminDirForRelID(ctx, projectRoot, relID); err != nil {
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
		_ = removeWorktreeAdminDirForRelID(ctx, projectRoot, relID)
		_ = os.RemoveAll(worktreePath)
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	// Verify the worktree was created correctly.
	if err := verifyCodeWorktree(ctx, worktreePath, branchName); err != nil {
		// Best-effort cleanup on failure.
		_ = runGit(ctx, projectRoot, "worktree", "prune")
		_ = removeWorktreeAdminDirForRelID(ctx, projectRoot, relID)
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

// ListCodeWorktrees lists all provisioned code worktrees owned by delegation under
// projectRoot/.steiner/worktrees, parsing git worktree list --porcelain.
// Only includes worktrees whose branch name starts with "delegate/" (the delegation ownership marker).
func ListCodeWorktrees(projectRoot string) ([]CodeWorktree, error) {
	entries, err := listWorktreeEntries(context.Background(), projectRoot)
	if err != nil {
		return nil, err
	}

	delegationPath := filepath.Join(projectRoot, ".steiner", "worktrees")
	var worktrees []CodeWorktree

	for _, entry := range entries {
		// Only include worktrees under .steiner/worktrees AND with delegate/ branch.
		if !strings.HasPrefix(entry.Path, delegationPath) {
			continue
		}
		if !strings.HasPrefix(entry.Branch, "delegate/") {
			continue
		}

		worktrees = append(worktrees, entry)
	}

	return worktrees, nil
}

// listWorktreeEntries parses git worktree list --porcelain and returns all entries.
// Branch may be empty for detached-HEAD worktrees.
func listWorktreeEntries(ctx context.Context, projectRoot string) ([]CodeWorktree, error) {
	out, err := gitOutput(ctx, projectRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

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

		worktrees = append(worktrees, CodeWorktree{
			Path:   worktreePath,
			Branch: branch,
		})
	}

	return worktrees, nil
}

// PruneCodeWorktree removes the code worktree identified by relID (relative path
// under .steiner/worktrees/) if and only if it is owned by delegation
// (branch starts with "delegate/").
// It tolerates "not a working tree" errors (already-removed paths) as idempotent no-ops,
// but refuses to remove worktrees not owned by delegation.
func PruneCodeWorktree(ctx context.Context, projectRoot, relID string) error {
	worktreePath := filepath.Clean(filepath.Join(projectRoot, ".steiner", "worktrees", relID))

	// Check if the worktree is known to git and what branch it has.
	entries, err := listWorktreeEntries(ctx, projectRoot)
	if err != nil {
		return err
	}

	// Look for a matching worktree in the list.
	var foundEntry *CodeWorktree
	for i := range entries {
		cleanEntryPath := filepath.Clean(entries[i].Path)
		if cleanEntryPath == worktreePath {
			foundEntry = &entries[i]
			break
		}
	}

	// If found, verify it's delegation-owned (branch starts with "delegate/").
	if foundEntry != nil {
		if !strings.HasPrefix(foundEntry.Branch, "delegate/") {
			return fmt.Errorf("prune code worktree: %w: branch %q does not start with \"delegate/\"",
				ErrWorktreeNotDelegation, foundEntry.Branch)
		}
	}
	// If not found, it's either already-removed or a stale directory.
	// Treat as idempotent no-op (tolerate missing paths).

	// Attempt to remove via git worktree remove.
	err = runGit(ctx, projectRoot, "worktree", "remove", "--force", worktreePath)
	if err != nil && !isGitWorktreeRemovalMissingPath(err) {
		return err
	}

	// Remove stale checkout path.
	if err := os.RemoveAll(worktreePath); err != nil {
		return fmt.Errorf("remove worktree path: %w", err)
	}

	// Remove stale admin dir under the common .git dir.
	if err := removeWorktreeAdminDirForRelID(ctx, projectRoot, relID); err != nil {
		return err
	}

	return nil
}

// PruneAllCodeWorktrees prunes all delegation-owned code worktrees under projectRoot/.steiner/worktrees,
// collecting errors with errors.Join rather than stopping at the first failure.
// Only prunes worktrees known to git with branches starting with "delegate/".
func PruneAllCodeWorktrees(ctx context.Context, projectRoot string) error {
	worktrees, err := ListCodeWorktrees(projectRoot)
	if err != nil {
		return err
	}

	delegationBase := filepath.Join(projectRoot, ".steiner", "worktrees")
	var errs []error
	for _, wt := range worktrees {
		// Extract the relative ID (path suffix under .steiner/worktrees/).
		relID, err := filepath.Rel(delegationBase, wt.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("extract relative worktree ID: %w", err))
			continue
		}
		if err := PruneCodeWorktree(ctx, projectRoot, relID); err != nil {
			errs = append(errs, err)
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

func removeWorktreeAdminDirForRelID(ctx context.Context, projectRoot, relID string) error {
	commonDir, err := gitOutput(ctx, projectRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}

	adminDir := strings.TrimSpace(commonDir)
	if !filepath.IsAbs(adminDir) {
		adminDir = filepath.Join(projectRoot, adminDir)
	}
	adminDir = filepath.Join(adminDir, "worktrees", relID)

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
