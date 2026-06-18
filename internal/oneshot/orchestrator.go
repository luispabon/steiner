package oneshot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/session"
)

// Run executes the autonomous plan -> implement -> review loop.
//
//nolint:gocyclo
func (o *Orchestrator) Run(ctx context.Context) (Manifest, error) {
	if o == nil {
		return Manifest{}, fmt.Errorf("orchestrator is required")
	}

	worktree, err := ProvisionWorktree(ctx, o.deps.ProjectRoot, o.deps.Identity)
	if err != nil {
		return Manifest{}, fmt.Errorf("provision worktree: %w", err)
	}
	worktreePath := worktree.Path
	planningPath := o.deps.Identity.PlanningPath(worktreePath)
	store := o.deps.ManifestStore
	if store == nil {
		store = NewManifestStore(o.deps.Identity.ManifestPath(o.deps.ProjectRoot))
	}

	lock, err := AcquireRunLock(o.deps.ProjectRoot, o.deps.Identity)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		_ = lock.Release()
	}()

	interruptCtx, interruptStop := o.deps.InterruptFactory(ctx)
	defer interruptStop()

	if err := os.MkdirAll(planningPath, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("create planning directory: %w", err)
	}

	manifest := Manifest{
		RunID:         o.deps.Identity.ID,
		Slug:          o.deps.Identity.Slug,
		Task:          o.deps.Task,
		Branch:        o.deps.Identity.BranchName(),
		WorktreePath:  worktreePath,
		ModelSnapshot: phaseModelSnapshot(o.deps.Config),
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusPending,
			PhaseImplement: PhaseStatusPending,
			PhaseReview:    PhaseStatusPending,
		},
		PhaseSessionIDs: map[Phase]string{},
	}
	if err := store.Write(manifest); err != nil {
		return Manifest{}, err
	}

	var previousPhase Phase
	for _, phase := range phaseOrder() {
		if err := interruptCtx.Err(); err != nil {
			manifest.PhaseStatuses[phase] = PhaseStatusFailed
			if err := store.Write(manifest); err != nil {
				return manifest, err
			}
			emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorCancelled, "run interrupted before phase start")
			return manifest, err
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
		runner, err := o.deps.RunnerFactory.NewPhaseRunner(phaseCtx, phase, modelAlias, NewWorktreeAutoApprover(worktreePath), advisorCfg)
		if err != nil {
			cancel()
			manifest.PhaseStatuses[phase] = PhaseStatusFailed
			emitPhaseIndicator(o.deps.Events, manifest.RunID, phase, phaseIndicatorBoundary, err.Error())
			if writeErr := store.Write(manifest); writeErr != nil {
				return manifest, writeErr
			}
			return manifest, err
		}

		conversation := phaseConversation(o.deps.Identity, o.deps.Task, phase, worktreePath, planningPath)
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
			filepath.Join(planningPath, "overview.md"),
			filepath.Join(planningPath, "plan.yaml"),
		}
		if err := CheckBoundary(phaseCtx, phase, worktreePath, requiredArtifacts); err != nil {
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

func (o *Orchestrator) persistPhaseSession(phase Phase, modelAlias string, result RunResult) (string, error) {
	lineage := result.Lineage
	if lineage.Empty() && len(result.Conversation) > 0 {
		lineage = agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID:       1,
					Messages: cloneAgentMessages(result.Conversation),
				},
			},
			NextGenerationID: 2,
		}
	}

	sess, err := session.NewSession(strings.TrimSpace(modelAlias), lineage, o.deps.Identity.ID)
	if err != nil {
		return "", fmt.Errorf("create phase session: %w", err)
	}
	sess = sess.WithTitle(fmt.Sprintf("%s phase: %s", phase, o.deps.Task))
	if err := o.deps.SessionStore.Save(sess); err != nil {
		return "", fmt.Errorf("save phase session: %w", err)
	}
	return sess.ID, nil
}

func phaseConversation(identity RunIdentity, task string, phase Phase, worktreePath, planningPath string) []agent.Message {
	lines := phaseSeedConversation(identity, task, string(phase), worktreePath, planningPath)
	message := strings.Join(lines, "\n")
	return []agent.Message{
		{
			Role:    agent.MessageRoleUser,
			Content: message,
		},
	}
}

func cloneAgentMessages(messages []agent.Message) []agent.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]agent.Message, len(messages))
	copy(out, messages)
	return out
}
