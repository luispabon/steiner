package delegation

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

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
