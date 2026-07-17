package oneshot

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Resume reopens the run described by the manifest and restarts from the first incomplete phase.
func (o *Orchestrator) Resume(ctx context.Context) (Manifest, error) {
	if o == nil {
		return Manifest{}, fmt.Errorf("orchestrator is required")
	}

	store := o.deps.ManifestStore
	if store == nil {
		store = NewManifestStore(o.deps.Identity.ManifestPath(o.deps.ProjectRoot))
	}

	manifest, err := store.Read()
	if err != nil {
		return Manifest{}, fmt.Errorf("load manifest: %w", err)
	}
	if strings.TrimSpace(manifest.RunID) == "" {
		return Manifest{}, fmt.Errorf("resume run: manifest run id is required")
	}
	if strings.TrimSpace(manifest.Branch) == "" {
		manifest.Branch = o.deps.Identity.BranchName()
	}
	if strings.TrimSpace(manifest.WorktreePath) == "" {
		manifest.WorktreePath = o.deps.Identity.WorktreePath(o.deps.ProjectRoot)
	}

	lock, err := AcquireRunLock(o.deps.ProjectRoot, o.deps.Identity)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		_ = lock.Release()
	}()

	worktree, err := ensureResumeWorktree(ctx, o.deps.ProjectRoot, o.deps.Identity, manifest.Branch)
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
		return Manifest{}, fmt.Errorf("resume run: %s is already complete", manifest.RunID)
	}

	interruptCtx, interruptStop := o.deps.InterruptFactory(ctx)
	defer interruptStop()

	planningPath := o.deps.Identity.PlanningPath(worktree.Path)

	defer func() {
		if retErr != nil && manifest.RunID != "" {
			o.tryFailureReport(ctx, &manifest, planningPath)
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

		// Check if this phase is being resumed (re-run after a failure).
		// A phase is resuming if its status is Failed or Running.
		phaseIsResuming := manifest.PhaseStatuses[phase] == PhaseStatusFailed || manifest.PhaseStatuses[phase] == PhaseStatusRunning

		if err := o.runPhase(runPhaseParams{
			InterruptCtx:  interruptCtx,
			Store:         store,
			Manifest:      &manifest,
			Lock:          lock,
			WorktreePath:  worktree.Path,
			PlanningPath:  planningPath,
			Phase:         phase,
			PreviousPhase: previousPhase,
			Resuming:      phaseIsResuming,
		}); err != nil {
			return manifest, err
		}
		previousPhase = phase
	}

	o.finalizeRun(ctx, &manifest, planningPath)

	return manifest, nil
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

func ensureResumeWorktree(ctx context.Context, projectRoot string, identity RunIdentity, branch string) (Worktree, error) {
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
			StartPoint: defaultWorktreeStartPoint,
		}, nil
	} else if !os.IsNotExist(err) {
		return Worktree{}, fmt.Errorf("stat worktree: %w", err)
	}

	return ProvisionWorktree(ctx, projectRoot, identity)
}
