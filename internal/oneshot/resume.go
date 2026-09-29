package oneshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/luispabon/steiner/internal/session"
)

// Resume reopens the run described by the manifest and restarts from the first incomplete phase.
func (o *Orchestrator) Resume(ctx context.Context) (manifest Manifest, err error) {
	if o == nil {
		return Manifest{}, fmt.Errorf("orchestrator is required")
	}

	store := o.manifestStore()

	manifest, err = store.Read()
	if err != nil {
		return Manifest{}, fmt.Errorf("load manifest: %w", err)
	}
	manifest.WorktreeBase = strings.TrimSpace(manifest.WorktreeBase)
	if strings.TrimSpace(manifest.RunID) == "" {
		return Manifest{}, fmt.Errorf("resume run: manifest run id is required")
	}
	if strings.TrimSpace(manifest.Branch) == "" {
		manifest.Branch = o.deps.Identity.BranchName()
	}
	if strings.TrimSpace(manifest.WorktreePath) == "" {
		manifest.WorktreePath = o.deps.Identity.WorktreePath(o.deps.ProjectRoot)
	}

	lock, err := o.deps.RunLockFactory(o.deps.ProjectRoot, o.deps.Identity)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()

	if strings.TrimSpace(manifest.WorktreeBase) == "" {
		base, err := resolveWorktreeStartPoint(ctx, o.deps.ProjectRoot)
		if err != nil {
			return Manifest{}, fmt.Errorf("infer worktree base: %w", err)
		}
		manifest.WorktreeBase = base
		if err := store.Write(manifest); err != nil {
			return Manifest{}, fmt.Errorf("persist inferred worktree base: %w", err)
		}
	}

	worktree, err := ensureResumeWorktree(ctx, o.deps.ProjectRoot, o.deps.Identity, manifest.Branch, manifest.WorktreeBase)
	if err != nil {
		return Manifest{}, err
	}
	manifest.WorktreePath = worktree.Path

	return o.resumeFromManifest(ctx, store, manifest, worktree, lock)
}

//nolint:gocyclo
func (o *Orchestrator) resumeFromManifest(ctx context.Context, store *ManifestStore, manifest Manifest, worktree Worktree, lock *RunLock) (ret Manifest, retErr error) {
	startPhase, ok := firstIncompletePhase(manifest)
	if !ok && closeoutRetryable(manifest) {
		// All phases finished but closeout failed: re-run closeout only.
		o.finalizeRun(ctx, store, &manifest, o.deps.Identity.PlanningPath(worktree.Path))
		if closeoutRetryable(manifest) {
			return manifest, fmt.Errorf("resume run: closeout for %s failed again: %s", manifest.RunID, manifest.CloseoutNote)
		}
		return manifest, nil
	}
	if !ok {
		return Manifest{}, fmt.Errorf("resume run: %s is already complete", manifest.RunID)
	}

	o.reportOrphanedWorktrees(manifest, startPhase)

	interruptCtx, interruptStop := o.deps.InterruptFactory(ctx)
	defer interruptStop()

	planningPath := o.deps.Identity.PlanningPath(worktree.Path)

	defer func() {
		if retErr != nil && manifest.RunID != "" {
			o.tryFailureReport(ctx, store, &manifest)
		}
	}()

	if err := os.MkdirAll(planningPath, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("create planning directory: %w", err)
	}

	previousPhase := previousPhaseBefore(startPhase)
	started := false
	for _, phase := range phaseOrder() {
		if !started {
			if phase != startPhase {
				continue
			}
			started = true
		}

		if err := interruptCtx.Err(); err != nil {
			manifest.PhaseStatuses[phase] = PhaseStatusFailed
			if err := store.Write(manifest); err != nil {
				return manifest, err
			}
			emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorCancelled, "run interrupted before phase start")
			return manifest, err
		}

		if current := manifest.PhaseStatuses[phase]; current == PhaseStatusDone || current == PhaseStatusSkipped {
			previousPhase = phase
			continue
		}

		if err := o.runPhase(runPhaseParams{
			InterruptCtx:  interruptCtx,
			Store:         store,
			Manifest:      &manifest,
			Lock:          lock,
			WorktreePath:  worktree.Path,
			PlanningPath:  planningPath,
			Phase:         phase,
			PreviousPhase: previousPhase,
		}); err != nil {
			return manifest, err
		}
		previousPhase = phase
	}

	o.finalizeRun(ctx, store, &manifest, planningPath)

	return manifest, nil
}

// sessionLoader is the optional read side of a SessionStore; stores that
// cannot load sessions simply yield no orphan report.
type sessionLoader interface {
	Load(id string) (session.Session, error)
}

// reportOrphanedWorktrees lists the worktrees of sub-agents that were still
// running when the interrupted phase died. The phase is restarted regardless
// and lost envelopes are never delivered; agents without a worktree are
// skipped since there is nothing on disk to inspect.
func (o *Orchestrator) reportOrphanedWorktrees(manifest Manifest, phase Phase) {
	id := strings.TrimSpace(manifest.PhaseSessionIDs[phase])
	loader, ok := o.deps.SessionStore.(sessionLoader)
	if id == "" || !ok {
		return
	}
	sess, err := loader.Load(id)
	if err != nil {
		return
	}
	for _, entry := range sess.SubAgentLedger {
		if strings.TrimSpace(entry.WorktreePath) == "" {
			continue
		}
		emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorOrphaned,
			fmt.Sprintf("worktree left by interrupted sub-agent %s: %s", entry.AgentID, entry.WorktreePath))
	}
}

func firstIncompletePhase(manifest Manifest) (Phase, bool) {
	for _, phase := range phaseOrder() {
		switch manifest.PhaseStatuses[phase] {
		case PhaseStatusDone, PhaseStatusSkipped:
			continue
		default:
			return phase, true
		}
	}
	return "", false
}

// phaseCloseout labels the closeout-only resume step in run listings.
const phaseCloseout Phase = "closeout"

// closeoutRetryable reports whether every phase is finished but closeout
// failed, so resume can re-run just the closeout step.
func closeoutRetryable(manifest Manifest) bool {
	if _, incomplete := firstIncompletePhase(manifest); incomplete {
		return false
	}
	return manifest.CloseoutState == closeoutStateFailed
}

func previousPhaseBefore(phase Phase) Phase {
	prev := Phase("")
	for _, candidate := range phaseOrder() {
		if candidate == phase {
			return prev
		}
		prev = candidate
	}
	return ""
}

func ensureResumeWorktree(ctx context.Context, projectRoot string, identity RunIdentity, branch, base string) (Worktree, error) {
	worktreePath := identity.WorktreePath(projectRoot)
	if stat, err := os.Stat(worktreePath); err == nil {
		if !stat.IsDir() {
			return Worktree{}, fmt.Errorf("resume worktree: %s is not a directory", worktreePath)
		}
		if err := verifyWorktree(ctx, worktreePath, branch); err != nil {
			return Worktree{}, err
		}
		if err := ensureSteinerProjectDir(worktreePath); err != nil {
			return Worktree{}, err
		}
		return Worktree{
			Path:       worktreePath,
			BranchName: branch,
			StartPoint: base,
		}, nil
	} else if !os.IsNotExist(err) {
		return Worktree{}, fmt.Errorf("stat worktree: %w", err)
	}

	return provisionWorktreeAt(ctx, projectRoot, identity, base)
}
