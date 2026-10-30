package delegation

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// provisionCodeWorktreeAndWarnings checks for dirty changes and provisions an isolated
// worktree for worktree-isolated agents, collecting warnings for any issues encountered.
func provisionCodeWorktreeAndWarnings(ctx context.Context, workDir string, agentID string) (CodeWorktree, []string, error) {
	var warnings []string
	var provisionedWorktree CodeWorktree

	// Best-effort dirty-tree check; skip silently on error.
	if paths, err := DirtyPaths(ctx, workDir); err == nil && len(paths) > 0 {
		shown := paths
		if len(paths) > 10 {
			shown = paths[:10]
		}
		warnings = append(warnings, fmt.Sprintf(
			"parent working tree has %d uncommitted change(s) not visible to the isolated worktree: %s",
			len(paths), strings.Join(shown, ", ")))
		if len(paths) > 10 {
			warnings[len(warnings)-1] = warnings[len(warnings)-1] + fmt.Sprintf(
				"...and %d more", len(paths)-10)
		}
	}

	provisionedWorktree, err := ProvisionCodeWorktree(ctx, workDir, agentID)
	if err != nil {
		return provisionedWorktree, warnings, err
	}

	return provisionedWorktree, warnings, nil
}

// applyCodeWorktreeResult updates the delegation result with worktree path, branch, and warnings.
func applyCodeWorktreeResult(result tool.ExecutionResult, worktree CodeWorktree, warnings []string, workDir string) tool.ExecutionResult {
	if delegationResult, ok := result.Value.(Result); ok {
		if worktree.Path != "" {
			delegationResult.WorktreePath = worktree.Path
			delegationResult.WorktreeBranch = worktree.Branch
			delegationResult.providerWorktreePath = providerRelativeWorktreePath(workDir, worktree.Path)
		}
		delegationResult.Warnings = append(append([]string(nil), warnings...), delegationResult.Warnings...)
		result.Value = delegationResult
	}
	return result
}

// provisionCodePlan provisions a worktree-isolated agent's worktree and builds its
// request. It runs at dequeue time on the supervisor goroutine.
func provisionCodePlan(ctx context.Context, plan *delegatePlan, spec Spec, deps SpecializedToolDeps, resolvedProvider provider.Provider, resolvedModel provider.ResolvedModel, allowedTools []string) error {
	worktree, warnings, err := provisionCodeWorktreeAndWarnings(ctx, deps.WorkDir, spec.AgentID)
	if err != nil {
		return fmt.Errorf("%s: %w", spec.AgentType, err)
	}
	plan.worktree = worktree
	plan.warnings = warnings
	req, limits, err := buildSpecializedRun(ctx, spec, deps, resolvedProvider, resolvedModel, allowedTools, worktree)
	if err != nil {
		return err
	}
	plan.req = req
	plan.limits = limits
	plan.remediation = codeRemediationConfig(worktree)
	return nil
}

func codeRemediationConfig(worktree CodeWorktree) *RemediationConfig {
	if worktree.Path == "" {
		return nil
	}
	return &RemediationConfig{
		WorktreePath:   worktree.Path,
		ExpectedBranch: worktree.Branch,
		IsDirty: func(ctx context.Context) ([]string, error) {
			return DirtyPaths(ctx, worktree.Path)
		},
		Head: func(ctx context.Context) (string, error) {
			out, err := gitOutput(ctx, worktree.Path, "rev-parse", "HEAD")
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(out), nil
		},
		Committed: func(ctx context.Context, preHEAD string, initialDirty []string) (bool, error) {
			diffOut, err := gitOutput(ctx, worktree.Path, "diff", "--name-only", "--no-renames", preHEAD+"..HEAD")
			if err != nil {
				return false, err
			}
			committedPaths := nonEmptyLines(diffOut)
			// Every initially-dirty path must appear in the committed diff.
			for _, p := range initialDirty {
				if !slices.Contains(committedPaths, p) {
					return false, nil
				}
			}
			// Tree must now be clean.
			stillDirty, err := DirtyPaths(ctx, worktree.Path)
			if err != nil {
				return false, err
			}
			return len(stillDirty) == 0, nil
		},
	}
}

func applySpecializedWorktreeResult(agentType AgentType, result tool.ExecutionResult, worktree CodeWorktree, warnings []string, workDir string) tool.ExecutionResult {
	if UsesWorktree(agentType) {
		return applyCodeWorktreeResult(result, worktree, warnings, workDir)
	}
	return result
}

func cleanupRegistrationWorktree(agentType AgentType, workDir string, worktree CodeWorktree) {
	if !UsesWorktree(agentType) || workDir == "" || worktree.Path == "" {
		return
	}
	// best-effort: worktree left for manual cleanup if prune fails
	_, _ = pruneCodeWorktree(workDir, worktree)
}
