package oneshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/luispabon/steiner/internal/session"
)

// readResumeManifest loads the manifest and applies the defaults Resume needs.
func (o *Orchestrator) readResumeManifest(store *ManifestStore) (Manifest, error) {
	manifest, err := store.Read()
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
	return manifest, nil
}

// Resume reopens the run described by the manifest and restarts from the first incomplete phase.
func (o *Orchestrator) Resume(ctx context.Context) (manifest Manifest, err error) {
	if o == nil {
		return Manifest{}, fmt.Errorf("orchestrator is required")
	}

	store := o.manifestStore()

	// This copy only feeds the early error checks below, so a missing manifest
	// or a manifest with no run id fails before the lock is taken. The run
	// itself acts on the copy read under the lock, because a concurrent
	// resolution can land in between.
	manifest, err = o.readResumeManifest(store)
	if err != nil {
		return Manifest{}, err
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

	// Re-read under the lock so a resolution that landed since the first read
	// is not reverted by writing this copy back.
	manifest, err = o.readResumeManifest(store)
	if err != nil {
		return Manifest{}, err
	}

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
	if !ok {
		if !reportPending(manifest) && !closeoutOutcomeUnknown(manifest) && !closeoutRetryable(manifest) {
			return Manifest{}, fmt.Errorf("resume run: %s is already complete", manifest.RunID)
		}
		// All phases finished: re-run finalization only. finalizeRun writes a
		// pending report, refuses an unknown closeout outcome, and runs closeout
		// only when an operator authorised a retry.
		if err := o.finalizeRun(ctx, store, &manifest, o.deps.Identity.PlanningPath(worktree.Path)); err != nil {
			return manifest, err
		}
		return manifest, nil
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

	if err := o.finalizeRun(ctx, store, &manifest, planningPath); err != nil {
		return manifest, err
	}

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

// Synthetic resume phases label finalization-only work in run listings. They
// are listing labels only and never run as phases.
const (
	// phaseCloseout labels an operator-authorised closeout retry.
	phaseCloseout Phase = "closeout"
	// phaseReport labels a run whose final report still has to be written.
	phaseReport Phase = "report"
	// phaseResolve labels a run whose closeout outcome is unknown and needs an
	// explicit manual resolution.
	phaseResolve Phase = "resolve"
)

// reportPending reports whether the final report still has to be written.
func reportPending(manifest Manifest) bool {
	if manifest.ReportState == reportStateWritten {
		return false
	}
	if manifest.FinalizationVersion > 0 {
		return manifest.ReportState == reportStatePending
	}
	return strings.TrimSpace(manifest.ReportPath) == ""
}

// closeoutRetryable reports whether every phase is finished and an operator
// authorised one more closeout attempt. A legacy failed state is ambiguous, so
// it is not retried automatically.
func closeoutRetryable(manifest Manifest) bool {
	if _, incomplete := firstIncompletePhase(manifest); incomplete {
		return false
	}
	return manifest.CloseoutState == closeoutStateRetryAuthorized
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
		if err := session.EnsureSteinerProjectDir(worktreePath); err != nil {
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
