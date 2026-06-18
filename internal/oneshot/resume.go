package oneshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

	return o.resumeFromManifest(ctx, store, manifest, worktree)
}

//nolint:gocyclo
func (o *Orchestrator) resumeFromManifest(ctx context.Context, store *ManifestStore, manifest Manifest, worktree Worktree) (Manifest, error) {
	startPhase, ok := firstIncompletePhase(manifest)
	if !ok {
		return Manifest{}, fmt.Errorf("resume run: %s is already complete", manifest.RunID)
	}

	interruptCtx, interruptStop := o.deps.InterruptFactory(ctx)
	defer interruptStop()

	planningPath := o.deps.Identity.PlanningPath(worktree.Path)
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

		modelAlias := phaseModelAlias(o.deps.Config, phase)
		advisorCfg := phaseAdvisorConfig(o.deps.Config, phase)
		emitPhaseTransition(o.deps.Events, manifest.RunID, previousPhase, phase, phaseTransitionStarting, modelAlias, "")
		emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorStarting, "phase starting")

		manifest.CurrentPhase = phase
		manifest.PhaseStatuses[phase] = PhaseStatusRunning
		if err := store.Write(manifest); err != nil {
			manifest.PhaseStatuses[phase] = PhaseStatusFailed
			return manifest, err
		}

		phaseCtx, cancel := context.WithCancel(interruptCtx)
		runner, err := o.deps.RunnerFactory.NewPhaseRunner(phaseCtx, phase, modelAlias, NewWorktreeAutoApprover(worktree.Path), advisorCfg)
		if err != nil {
			cancel()
			manifest.PhaseStatuses[phase] = PhaseStatusFailed
			emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorBoundary, err.Error())
			if writeErr := store.Write(manifest); writeErr != nil {
				return manifest, writeErr
			}
			return manifest, err
		}

		conversation := phaseConversation(o.deps.Identity, o.deps.Task, phase, worktree.Path, planningPath)
		result, runErr := runner.RunPhase(phaseCtx, conversation, nil, o.deps.SteerCh)

		sessionID, saveErr := o.persistPhaseSession(phase, modelAlias, result)
		if saveErr != nil {
			cancel()
			manifest.PhaseStatuses[phase] = PhaseStatusFailed
			if err := store.Write(manifest); err != nil {
				return manifest, err
			}
			emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorBoundary, saveErr.Error())
			return manifest, saveErr
		}
		manifest.PhaseSessionIDs[phase] = sessionID

		if runErr != nil {
			cancel()
			manifest.PhaseStatuses[phase] = PhaseStatusFailed
			if err := store.Write(manifest); err != nil {
				return manifest, err
			}
			emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorCancelled, runErr.Error())
			emitPhaseTransition(o.deps.Events, manifest.RunID, phase, phase, phaseTransitionFailed, modelAlias, sessionID)
			return manifest, runErr
		}

		requiredArtifacts := []string{
			joinPlanningArtifact(planningPath, "overview.md"),
			joinPlanningArtifact(planningPath, "plan.yaml"),
		}
		if err := CheckBoundary(phaseCtx, phase, worktree.Path, requiredArtifacts); err != nil {
			cancel()
			manifest.PhaseStatuses[phase] = PhaseStatusFailed
			if err := store.Write(manifest); err != nil {
				return manifest, err
			}
			emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorBoundary, err.Error())
			emitPhaseTransition(o.deps.Events, manifest.RunID, phase, phase, phaseTransitionFailed, modelAlias, sessionID)
			return manifest, err
		}

		cancel()
		manifest.PhaseStatuses[phase] = PhaseStatusDone
		manifest.CurrentPhase = phase
		if err := store.Write(manifest); err != nil {
			return manifest, err
		}
		emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorCompleted, "phase complete")
		emitPhaseTransition(o.deps.Events, manifest.RunID, phase, phase, phaseTransitionCompleted, modelAlias, sessionID)
		previousPhase = phase
	}

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

func joinPlanningArtifact(planningPath, name string) string {
	return filepath.Join(planningPath, name)
}
