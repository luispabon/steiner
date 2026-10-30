package delegation

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// PruneCodeWorktree removes the worktree identified by relID (relative path
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
		return false, fmt.Errorf("prune worktree: %w", ErrWorktreePathEscape)
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
		return false, fmt.Errorf("prune worktree: %w: branch %q does not start with \"delegate/\"",
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

// PruneAllCodeWorktrees prunes all delegation-owned worktrees under projectRoot/.steiner/worktrees,
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

// PruneProcessCodeWorktrees prunes delegation-owned worktrees created by this process.
// It continues after per-worktree errors and returns the number of worktrees git
// deregistered, counting a worktree even when its post-removal cleanup failed and an
// aggregated error is returned. Worktrees at the protected paths are left in place.
func PruneProcessCodeWorktrees(ctx context.Context, projectRoot string, protected ...string) (int, error) {
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
	skip := make(map[string]struct{}, len(protected))
	for _, path := range protected {
		skip[filepath.Clean(path)] = struct{}{}
	}
	for _, worktree := range processWorktrees {
		if _, kept := skip[filepath.Clean(worktree.Path)]; kept {
			continue
		}
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
