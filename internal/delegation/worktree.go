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
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", errors.Join(ErrWorktreeProvisioning, err))
	}
	sanitizedBranch := sanitizeBranchName(parentBranch)
	processHash, err := getProcessHash()
	if err != nil {
		return CodeWorktree{}, fmt.Errorf("provision code worktree: %w", errors.Join(ErrWorktreeProvisioning, fmt.Errorf("get process hash: %w", err)))
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

// ListCodeWorktrees lists all provisioned code worktrees owned by delegation under
// projectRoot/.steiner/worktrees, parsing git worktree list --porcelain.
// Only includes worktrees whose branch name starts with "delegate/" (the delegation ownership marker).
func ListCodeWorktrees(ctx context.Context, projectRoot string) ([]CodeWorktree, error) {
	entries, err := listWorktreeEntries(ctx, projectRoot)
	if err != nil {
		return nil, err
	}

	delegationPath := filepath.Join(projectRoot, ".steiner", "worktrees")
	delegationPathPrefix := delegationPath + string(filepath.Separator)
	var worktrees []CodeWorktree

	for _, entry := range entries {
		// Only include worktrees under .steiner/worktrees AND with delegate/ branch.
		// A trailing separator guards against sibling directories that merely
		// share the string prefix (e.g. .steiner/worktrees-other).
		if entry.Path != delegationPath && !strings.HasPrefix(entry.Path, delegationPathPrefix) {
			continue
		}
		if !strings.HasPrefix(entry.Branch, "delegate/") {
			continue
		}

		worktrees = append(worktrees, entry)
	}

	return worktrees, nil
}

// ListProcessCodeWorktrees lists delegation-owned code worktrees created by this process.
func ListProcessCodeWorktrees(ctx context.Context, projectRoot string) ([]CodeWorktree, error) {
	worktrees, err := ListCodeWorktrees(ctx, projectRoot)
	if err != nil {
		return nil, err
	}

	return filterProcessCodeWorktrees(worktrees)
}

func filterProcessCodeWorktrees(worktrees []CodeWorktree) ([]CodeWorktree, error) {
	processHash, err := getProcessHash()
	if err != nil {
		return nil, fmt.Errorf("get process hash: %w", err)
	}
	processPrefix := "delegate/" + processHash + "/"
	filtered := make([]CodeWorktree, 0, len(worktrees))
	for _, worktree := range worktrees {
		if strings.HasPrefix(worktree.Branch, processPrefix) {
			filtered = append(filtered, worktree)
		}
	}
	return filtered, nil
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
// (branch starts with "delegate/"). Returns (removed, error) where removed is true
// once git deregistered the worktree (including when it was already gone), and false
// if nothing was found. After a successful git removal, the stale path, admin dir,
// and branch cleanup runs best-effort: any failures there are aggregated and returned
// alongside removed=true.
// It tolerates "not a working tree" errors (already-removed paths) and missing branches
// as idempotent no-ops, but refuses to remove worktrees not owned by delegation.
func PruneCodeWorktree(ctx context.Context, projectRoot, relID string) (bool, error) {
	worktreeMu.Lock()
	defer worktreeMu.Unlock()
	return pruneCodeWorktreeLocked(ctx, projectRoot, relID)
}

// pruneCodeWorktreeLocked implements PruneCodeWorktree's removal logic and
// requires the caller to hold worktreeMu. The public wrapper takes the mutex;
// the prune-all helpers hold it across their whole sweep and call this directly
// so they never re-enter a non-reentrant mutex through the public helper.
func pruneCodeWorktreeLocked(ctx context.Context, projectRoot, relID string) (bool, error) {
	delegationBase := filepath.Clean(filepath.Join(projectRoot, ".steiner", "worktrees"))
	worktreePath := filepath.Clean(filepath.Join(delegationBase, relID))

	// Verify the resolved path is contained within the delegation base directory.
	// Reject path traversal attempts (e.g., relID containing ".." segments).
	if worktreePath != delegationBase && !strings.HasPrefix(worktreePath, delegationBase+string(filepath.Separator)) {
		return false, fmt.Errorf("prune code worktree: %w", ErrWorktreePathEscape)
	}

	// Check if the worktree is known to git and what branch it has.
	entries, err := listWorktreeEntries(ctx, projectRoot)
	if err != nil {
		return false, err
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

	// Only proceed with removal if the worktree is known to git AND delegation-owned.
	if foundEntry == nil {
		// Worktree not found in git list: either already-removed or unrecognized path.
		// Treat as idempotent no-op (nothing to remove).
		return false, nil
	}

	// Verify it's delegation-owned (branch starts with "delegate/").
	if !strings.HasPrefix(foundEntry.Branch, "delegate/") {
		return false, fmt.Errorf("prune code worktree: %w: branch %q does not start with \"delegate/\"",
			ErrWorktreeNotDelegation, foundEntry.Branch)
	}

	// Attempt to remove via git worktree remove.
	err = runGit(ctx, projectRoot, "worktree", "remove", "--force", worktreePath)
	if err != nil && !isGitWorktreeRemovalMissingPath(err) {
		return false, err
	}

	// Git removal succeeded, so the worktree is considered removed. The
	// remaining stale path/admin/branch cleanup is best-effort: attempt each
	// step even if an earlier one fails, aggregate the errors, and still report
	// removed=true.
	var cleanupErrs []error

	// Remove stale checkout path.
	if err := removeAll(worktreePath); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove worktree path: %w", err))
	}

	// Delete the branch ref if it exists and is non-empty.
	if foundEntry.Branch != "" {
		if err := runGit(ctx, projectRoot, "branch", "-D", foundEntry.Branch); err != nil && !isGitBranchNotFound(err) {
			cleanupErrs = append(cleanupErrs, err)
		}
	}

	return true, errors.Join(cleanupErrs...)
}

// PruneAllCodeWorktrees prunes all delegation-owned code worktrees under projectRoot/.steiner/worktrees,
// collecting errors with errors.Join rather than stopping at the first failure.
// Returns (removedCount, error) where removedCount is the number of worktrees git deregistered,
// counted even when a worktree's post-removal cleanup failed and err != nil
// (accurate under partial progress).
// Only prunes worktrees known to git with branches starting with "delegate/".
func PruneAllCodeWorktrees(ctx context.Context, projectRoot string) (int, error) {
	worktreeMu.Lock()
	defer worktreeMu.Unlock()

	worktrees, err := ListCodeWorktrees(ctx, projectRoot)
	if err != nil {
		return 0, err
	}

	delegationBase := filepath.Join(projectRoot, ".steiner", "worktrees")
	var errs []error
	removedCount := 0
	for _, wt := range worktrees {
		// Extract the relative ID (path suffix under .steiner/worktrees/).
		relID, err := filepath.Rel(delegationBase, wt.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("extract relative worktree ID: %w", err))
			continue
		}
		removed, err := pruneCodeWorktreeLocked(ctx, projectRoot, relID)
		if removed {
			removedCount++
		}
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return removedCount, errors.Join(errs...)
	}
	return removedCount, nil
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

// PruneProcessCodeWorktrees prunes delegation-owned code worktrees created by this process.
// It continues after per-worktree errors and returns the number of worktrees git
// deregistered, counting a worktree even when its post-removal cleanup failed and an
// aggregated error is returned.
func PruneProcessCodeWorktrees(ctx context.Context, projectRoot string) (int, error) {
	worktreeMu.Lock()
	defer worktreeMu.Unlock()

	worktrees, err := ListCodeWorktrees(ctx, projectRoot)
	if err != nil {
		return 0, err
	}

	processWorktrees, err := filterProcessCodeWorktrees(worktrees)
	if err != nil {
		return 0, fmt.Errorf("get process hash: %w", err)
	}

	delegationBase := filepath.Join(projectRoot, ".steiner", "worktrees")
	var errs []error
	removedCount := 0
	for _, worktree := range processWorktrees {
		relID, err := filepath.Rel(delegationBase, worktree.Path)
		if err != nil {
			errs = append(errs, fmt.Errorf("extract relative worktree ID: %w", err))
			continue
		}
		removed, err := pruneCodeWorktreeLocked(ctx, projectRoot, relID)
		if removed {
			removedCount++
		}
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return removedCount, fmt.Errorf("prune process worktrees: %w", errors.Join(errs...))
	}
	return removedCount, nil
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

func isGitWorktreeRemovalMissingPath(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not a working tree") || strings.Contains(msg, "is not a working tree") || strings.Contains(msg, "is not a valid working tree")
}

func isGitBranchNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not found")
}
