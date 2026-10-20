package delegation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ErrWorktreeProvisioning is a sentinel error for worktree provisioning failures.
var ErrWorktreeProvisioning = errors.New("git worktree provisioning failed")

// ErrCodeWorktreeRequiresCommit indicates that a code worktree cannot be created because the parent repository has no commit.
var ErrCodeWorktreeRequiresCommit = errors.New("code worktree requires a commit")

// ErrWorktreeNotDelegation indicates an attempt to prune a worktree that is not owned by delegation.
var ErrWorktreeNotDelegation = errors.New("worktree is not delegation-owned")

// ErrWorktreePathEscape indicates that the worktree path would escape the delegation directory.
var ErrWorktreePathEscape = errors.New("worktree id escapes the delegation worktrees directory")

// providerRelativeWorktreePath computes a project-relative path for a worktree to expose
// in the provider result. It returns an empty string if the path cannot be safely resolved
// as relative to the project root or if it doesn't fall under .steiner/worktrees/.
func providerRelativeWorktreePath(projectRoot, worktreePath string) string {
	if projectRoot == "" || worktreePath == "" {
		return ""
	}

	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return ""
	}
	absRoot = filepath.Clean(absRoot)

	absWorktree, err := filepath.Abs(worktreePath)
	if err != nil {
		return ""
	}
	absWorktree = filepath.Clean(absWorktree)

	rel, err := filepath.Rel(absRoot, absWorktree)
	if err != nil {
		return ""
	}

	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return ""
	}

	if !strings.HasPrefix(rel, ".steiner"+string(filepath.Separator)+"worktrees"+string(filepath.Separator)) {
		return ""
	}

	return rel
}

// worktreeMu serializes git worktree mutations (add, remove, prune, branch
// deletion) against the same .git metadata store to avoid index-lock races
// between concurrent provisioning and pruning.
var worktreeMu sync.Mutex

// removeAll wraps os.RemoveAll so tests can inject stale-checkout cleanup
// failures without affecting git worktree removal.
var removeAll = os.RemoveAll

// CodeWorktree describes the provisioned checkout for a delegation run.
type CodeWorktree struct {
	Path   string
	Branch string
}

// getParentBranchName returns the current branch of the parent repo, or "detached" if HEAD is detached.
func getParentBranchName(ctx context.Context, projectRoot string) (string, error) {
	if _, err := gitOutput(ctx, projectRoot, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", err
	}
	out, err := gitOutput(ctx, projectRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		if _, verifyErr := gitOutput(ctx, projectRoot, "rev-parse", "--verify", "HEAD"); verifyErr != nil {
			return "", ErrCodeWorktreeRequiresCommit
		}
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

// checkCodeWorktreeFeasible runs the checks that can fail before any git
// worktree work, so callers can surface them synchronously ahead of dequeue-time
// provisioning.
func checkCodeWorktreeFeasible(ctx context.Context, projectRoot string) error {
	_, _, err := worktreeIdentity(ctx, projectRoot)
	return err
}

// worktreeIdentity derives the sanitized parent branch name and process hash
// that make worktree paths collision-free.
func worktreeIdentity(ctx context.Context, projectRoot string) (sanitizedBranch, processHash string, err error) {
	parentBranch, err := getParentBranchName(ctx, projectRoot)
	if err != nil {
		return "", "", fmt.Errorf("provision code worktree: %w", errors.Join(ErrWorktreeProvisioning, err))
	}
	processHash, err = getProcessHash()
	if err != nil {
		return "", "", fmt.Errorf("provision code worktree: %w", errors.Join(ErrWorktreeProvisioning, fmt.Errorf("get process hash: %w", err)))
	}
	return sanitizeBranchName(parentBranch), processHash, nil
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

	sanitizedBranch, processHash, err := worktreeIdentity(ctx, projectRoot)
	if err != nil {
		return CodeWorktree{}, err
	}

	// Construct the nested worktree path and branch name.
	relID := filepath.Join(processHash, sanitizedBranch, agentID)
	worktreePath := filepath.Join(projectRoot, ".steiner", "worktrees", relID)
	branchName := "delegate/" + strings.ReplaceAll(relID, string(filepath.Separator), "/")

	// Prune stale metadata and remove any existing path at this location.
	if err := runGit(ctx, projectRoot, "worktree", "prune"); err != nil {
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	// Refuse to clobber a path that is still a registered worktree: kept
	// worktrees may hold uncommitted or unmerged work needed by follow_up.
	if registered, err := listWorktreeEntries(ctx, projectRoot); err == nil {
		for _, e := range registered {
			if filepath.Clean(e.Path) == filepath.Clean(worktreePath) {
				return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", errors.Join(ErrWorktreeProvisioning, fmt.Errorf("worktree %s already exists (branch %s); refusing to overwrite", worktreePath, e.Branch)))
			}
		}
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
		_ = os.RemoveAll(worktreePath)
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	// Verify the worktree was created correctly.
	if err := verifyCodeWorktree(ctx, worktreePath, branchName); err != nil {
		// Best-effort cleanup on failure.
		_ = runGit(ctx, projectRoot, "worktree", "prune")
		_ = os.RemoveAll(worktreePath)
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", ErrWorktreeProvisioning)
	}

	return CodeWorktree{
		Path:   worktreePath,
		Branch: branchName,
	}, nil
}

// DirtyPaths returns the list of modified/untracked paths in the project,
// by parsing git status --porcelain -z. Renames and copies contribute both the
// new and the original path. Returns an empty slice if the tree is clean.
func DirtyPaths(ctx context.Context, projectRoot string) ([]string, error) {
	out, err := gitOutput(ctx, projectRoot, "status", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}

	var paths []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		// Do NOT TrimSpace here; porcelain format has status prefix that matters.
		if len(entry) <= 3 {
			continue
		}
		paths = append(paths, entry[3:])
		// In -z format a rename/copy entry is "XY new\x00old\x00".
		if entry[0] == 'R' || entry[0] == 'C' || entry[1] == 'R' || entry[1] == 'C' {
			i++
			if i < len(entries) && entries[i] != "" {
				paths = append(paths, entries[i])
			}
		}
	}
	return paths, nil
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
