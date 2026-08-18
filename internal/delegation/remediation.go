package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
)

// RemediationConfig configures an optional post-run commit-remediation hook.
// The generic SpawnDelegate orchestrates only; git behavior is injected via
// the function fields so SpawnDelegate stays agnostic of git.
type RemediationConfig struct {
	WorktreePath   string
	ExpectedBranch string
	// IsDirty returns the list of dirty/untracked paths in the worktree.
	IsDirty func(ctx context.Context) ([]string, error)
	// Head returns the current HEAD commit hash of the worktree.
	Head func(ctx context.Context) (string, error)
	// Committed reports whether the given initial dirty paths were all
	// committed between preHEAD and the current HEAD, leaving a clean tree.
	Committed func(ctx context.Context, preHEAD string, initialDirty []string) (bool, error)
}

type spawnOption func(*spawnOptions)

type spawnOptions struct {
	remediation *RemediationConfig
}

// WithRemediation opts SpawnDelegate into post-run commit remediation.
func WithRemediation(cfg *RemediationConfig) spawnOption {
	return func(o *spawnOptions) {
		o.remediation = cfg
	}
}

type remediationOutcome string

const (
	remediationNotAttempted remediationOutcome = "not_attempted"
	remediationAttempted    remediationOutcome = "attempted"
)

func applyRemediation(
	ctx context.Context,
	spec DelegationSpec,
	req agent.RunRequest,
	runner AgentRunner,
	state agent.RunState,
	runUsage CacheUsage,
	cfg *RemediationConfig,
	tc *traceCollector,
) (agent.RunState, CacheUsage, DelegationResult, remediationOutcome, error) {
	total := spec.PriorCacheUsage.Add(runUsage)
	result := buildResultWithTrace(spec.AgentID, state, tc, total)
	if cfg == nil {
		return state, runUsage, result, remediationNotAttempted, nil
	}

	if state.StopReason != agent.StopReasonComplete || result.Status != StatusComplete {
		dirty, err := cfg.IsDirty(ctx)
		if err == nil && len(dirty) > 0 {
			result.Warnings = append(result.Warnings, dirtyWorktreeWarning(cfg.WorktreePath, dirty))
		}
		return state, runUsage, result, remediationNotAttempted, nil
	}

	dirty, err := cfg.IsDirty(ctx)
	if err != nil {
		return state, runUsage, result, remediationNotAttempted, nil
	}
	if len(dirty) == 0 {
		return state, runUsage, result, remediationNotAttempted, nil
	}

	preHEAD, err := cfg.Head(ctx)
	if err != nil {
		return failedRemediation(
			spec, state, runUsage, result.Output, cfg.WorktreePath, dirty, tc,
			fmt.Errorf("get pre-remediation HEAD: %w", err),
		)
	}

	originalOutput := result.Output
	remediationMsg := fmt.Sprintf(`The implement step requires you to commit your changes and leave the worktree clean before reporting completion. Your worktree is still dirty. Please:
- Stage only your intended, in-scope changes (no blanket git add).
- Commit with a clear message describing what changed and why.
- Verify the working tree is clean: git status --porcelain must be empty.
Do not merge, rebase, or push. Do not switch branches.

Worktree root: %s
Branch: %s
Pre-remediation HEAD: %s
Uncommitted paths: %s`, cfg.WorktreePath, cfg.ExpectedBranch, preHEAD, strings.Join(dirty, ", "))
	remedReq := buildContinuationRequest(req, state.Conversation, remediationMsg, state.TurnCount, spec.Limits)
	remedReq.Events = nil

	tc.add("remediation", "remediation attempted", map[string]any{
		"pre_head":        preHEAD,
		"initial_dirty":   dirtyPathsForTrace(dirty),
		"worktree_path":   cfg.WorktreePath,
		"expected_branch": cfg.ExpectedBranch,
	})
	remedState, remErr := runner.Run(ctx, remedReq)
	runUsage = runUsage.Add(cacheUsageOf(remedState))
	state = remedState
	tc.add("remediation", "remediation run complete", runStateFields(ctx, remedState, remErr))

	stillDirty, dirtyErr := cfg.IsDirty(ctx)
	committed, committedErr := cfg.Committed(ctx, preHEAD, dirty)
	succeeded := remErr == nil && dirtyErr == nil && committedErr == nil && len(stillDirty) == 0 && committed
	if succeeded {
		result = buildResultWithTrace(spec.AgentID, state, tc, spec.PriorCacheUsage.Add(runUsage))
		result.Output = originalOutput + "\n\n<remdiation note: committed remaining changes; worktree left clean>"
		tc.add("remediation", "remediation succeeded", map[string]any{
			"pre_head":      preHEAD,
			"initial_dirty": dirtyPathsForTrace(dirty),
		})
		return state, runUsage, result, remediationAttempted, nil
	}

	verificationErr := remediationVerificationError(remErr, dirtyErr, committedErr, stillDirty, committed)
	failedStateResult := buildResultWithTrace(spec.AgentID, state, tc, spec.PriorCacheUsage.Add(runUsage))
	failedStateResult.Status = StatusFailed
	failedStateResult.Output = originalOutput
	failedStateResult.Warnings = append(failedStateResult.Warnings, dirtyWorktreeWarning(cfg.WorktreePath, dirty))
	failedStateResult.SessionResumable = false
	tc.add("remediation", "remediation failed", map[string]any{
		"pre_head":           preHEAD,
		"initial_dirty":      dirtyPathsForTrace(dirty),
		"still_dirty":        dirtyPathsForTrace(stillDirty),
		"committed":          committed,
		"verification_error": verificationErr.Error(),
	})
	return state, runUsage, failedStateResult, remediationAttempted, verificationErr
}

func failedRemediation(
	spec DelegationSpec,
	state agent.RunState,
	runUsage CacheUsage,
	originalOutput string,
	worktreePath string,
	dirty []string,
	tc *traceCollector,
	err error,
) (agent.RunState, CacheUsage, DelegationResult, remediationOutcome, error) {
	result := buildResultWithTrace(spec.AgentID, state, tc, spec.PriorCacheUsage.Add(runUsage))
	result.Status = StatusFailed
	result.Output = originalOutput
	result.Warnings = append(result.Warnings, dirtyWorktreeWarning(worktreePath, dirty))
	result.SessionResumable = false
	tc.add("remediation", "remediation failed", map[string]any{
		"verification_error": err.Error(),
		"initial_dirty":      dirtyPathsForTrace(dirty),
	})
	return state, runUsage, result, remediationAttempted, err
}

func remediationVerificationError(runErr, dirtyErr, committedErr error, stillDirty []string, committed bool) error {
	var details []string
	if runErr != nil {
		details = append(details, "remediation run: "+runErr.Error())
	}
	if dirtyErr != nil {
		details = append(details, "worktree verification: "+dirtyErr.Error())
	}
	if committedErr != nil {
		details = append(details, "commit verification: "+committedErr.Error())
	}
	if len(stillDirty) > 0 {
		details = append(details, "still dirty: "+formatDirtyPaths(stillDirty))
	}
	if !committed {
		details = append(details, "initial changes were not committed")
	}
	if len(details) == 0 {
		return errors.New("remediation verification did not confirm a commit")
	}
	return errors.New(strings.Join(details, "; "))
}

func dirtyWorktreeWarning(worktreePath string, dirty []string) string {
	return fmt.Sprintf("code agent worktree %s is not clean; uncommitted changes: %s", worktreePath, formatDirtyPaths(dirty))
}

func formatDirtyPaths(paths []string) string {
	const maxPaths = 10
	if len(paths) <= maxPaths {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s, ...and %d more", strings.Join(paths[:maxPaths], ", "), len(paths)-maxPaths)
}

func dirtyPathsForTrace(paths []string) []string {
	if len(paths) <= 10 {
		return paths
	}
	return append(append([]string(nil), paths[:10]...), fmt.Sprintf("...and %d more", len(paths)-10))
}
