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
func (o *Orchestrator) Run(ctx context.Context) (manifest Manifest, err error) {
	if o == nil {
		return Manifest{}, fmt.Errorf("orchestrator is required")
	}

	worktree, err := ProvisionWorktree(ctx, o.deps.ProjectRoot, o.deps.Identity)
	if err != nil {
		return Manifest{}, fmt.Errorf("provision worktree: %w", err)
	}
	worktreePath := worktree.Path
	planningPath := o.deps.Identity.PlanningPath(worktreePath)

	defer func() {
		if err != nil && manifest.RunID != "" {
			o.tryFailureReport(ctx, &manifest, planningPath)
		}
	}()

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

	manifest = Manifest{
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

		// In a fresh run, phases are never resuming; they're all first-time runs.
		if err := o.runPhase(runPhaseParams{
			InterruptCtx:  interruptCtx,
			Store:         store,
			Manifest:      &manifest,
			Lock:          lock,
			WorktreePath:  worktreePath,
			PlanningPath:  planningPath,
			Phase:         phase,
			PreviousPhase: previousPhase,
			Resuming:      false,
		}); err != nil {
			return manifest, err
		}
		previousPhase = phase
	}

	o.finalizeRun(ctx, &manifest, planningPath)

	return manifest, nil
}

// runPhaseParams bundles the arguments to runPhase. worktreePath/planningPath
// and phase/previousPhase are adjacent same-typed values, so a struct with
// named fields avoids transposition mistakes at call sites.
type runPhaseParams struct {
	InterruptCtx  context.Context
	Store         *ManifestStore
	Manifest      *Manifest
	Lock          *RunLock
	WorktreePath  string
	PlanningPath  string
	Phase         Phase
	PreviousPhase Phase
	Resuming      bool
}

// runPhase executes a single phase: starting events, runner construction, execution, session
// persistence, boundary checking, and success bookkeeping. It mutates manifest and writes it via
// store at each state transition, and heartbeats lock at phase start and on successful completion.
// p.Resuming controls which artifacts are required at the phase boundary check.
func (o *Orchestrator) runPhase(p runPhaseParams) error {
	modelAlias := phaseModelAlias(o.deps.Config, p.Phase)
	advisorCfg := phaseAdvisorConfig(o.deps.Config, p.Phase)
	emitPhaseTransition(o.deps.Events, p.Manifest.RunID, p.PreviousPhase, p.Phase, phaseTransitionStarting, modelAlias, "")
	emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, phaseIndicatorStarting, "phase starting")

	p.Lock.Heartbeat()
	p.Manifest.CurrentPhase = p.Phase
	p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusRunning
	if err := p.Store.Write(*p.Manifest); err != nil {
		p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusFailed
		return err
	}

	phaseCtx, cancel := context.WithCancel(p.InterruptCtx)
	runner, err := o.deps.RunnerFactory.NewPhaseRunner(phaseCtx, p.Phase, modelAlias, NewWorktreeAutoApprover(p.WorktreePath), advisorCfg)
	if err != nil {
		cancel()
		p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusFailed
		emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, phaseIndicatorBoundary, err.Error())
		if writeErr := p.Store.Write(*p.Manifest); writeErr != nil {
			return writeErr
		}
		return err
	}

	conversation := phaseConversation(o.deps.Identity, o.deps.Task, p.Phase, p.WorktreePath, p.PlanningPath)
	result, runErr := runner.RunPhase(phaseCtx, conversation, nil, o.deps.DrainSteers)

	sessionID, saveErr := o.persistPhaseSession(p.Phase, modelAlias, result)
	if saveErr != nil {
		cancel()
		p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusFailed
		if err := p.Store.Write(*p.Manifest); err != nil {
			return err
		}
		emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, phaseIndicatorBoundary, saveErr.Error())
		return saveErr
	}
	p.Manifest.PhaseSessionIDs[p.Phase] = sessionID

	if runErr != nil {
		cancel()
		p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusFailed
		if err := p.Store.Write(*p.Manifest); err != nil {
			return err
		}
		emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, phaseIndicatorCancelled, runErr.Error())
		emitPhaseTransition(o.deps.Events, p.Manifest.RunID, p.Phase, p.Phase, phaseTransitionFailed, modelAlias, sessionID)
		return runErr
	}

	requiredArtifacts := requiredArtifactsForPhase(p.Phase, p.PlanningPath, p.Resuming)
	if err := CheckBoundary(phaseCtx, p.Phase, p.WorktreePath, requiredArtifacts); err != nil {
		cancel()
		p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusFailed
		if err := p.Store.Write(*p.Manifest); err != nil {
			return err
		}
		emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, phaseIndicatorBoundary, err.Error())
		emitPhaseTransition(o.deps.Events, p.Manifest.RunID, p.Phase, p.Phase, phaseTransitionFailed, modelAlias, sessionID)
		return err
	}

	cancel()
	p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusDone
	// Heartbeat again here (in addition to phase start) so the lock stays fresh
	// across the gap between finishing this phase and starting the next one,
	// matching resumeFromManifest's original behavior before this loop was unified.
	p.Lock.Heartbeat()
	p.Manifest.CurrentPhase = p.Phase
	if err := p.Store.Write(*p.Manifest); err != nil {
		return err
	}
	emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, phaseIndicatorCompleted, "phase complete")
	emitPhaseTransition(o.deps.Events, p.Manifest.RunID, p.Phase, p.Phase, phaseTransitionCompleted, modelAlias, sessionID)
	return nil
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

// tryFailureReport attempts to generate a failure report on error.
func (o *Orchestrator) tryFailureReport(ctx context.Context, manifest *Manifest, _ string) {
	if manifest == nil || strings.TrimSpace(manifest.RunID) == "" {
		return
	}
	outcome := ReviewOutcome{
		Passed:    false,
		Attempted: string(manifest.CurrentPhase),
	}
	report, err := GenerateFinalReport(ctx, *manifest, outcome)
	if err != nil {
		emitPhaseIndicator(o.deps.Events, manifest.RunID, "", phaseIndicatorBoundary, fmt.Sprintf("generate failure report: %v", err))
		return
	}
	manifest.ReportPath = report.ReportPath
}

// finalizeRun generates the final report and runs closeout for a successful run.
func (o *Orchestrator) finalizeRun(ctx context.Context, manifest *Manifest, planningPath string) {
	if manifest == nil || strings.TrimSpace(manifest.RunID) == "" {
		return
	}
	outcome := ReviewOutcome{
		Passed:  manifest.PhaseStatuses[PhaseReview] == PhaseStatusDone,
		Summary: "review phase completed",
	}
	if !outcome.Passed {
		outcome.Attempted = string(manifest.CurrentPhase)
	}

	report, err := GenerateFinalReport(ctx, *manifest, outcome)
	if err != nil {
		emitPhaseIndicator(o.deps.Events, manifest.RunID, "", phaseIndicatorBoundary, fmt.Sprintf("generate report: %v", err))
		return
	}
	manifest.ReportPath = report.ReportPath

	overview := ""
	if data, readErr := os.ReadFile(filepath.Join(planningPath, "overview.md")); readErr == nil {
		overview = string(data)
	}
	review := ""
	if data, readErr := os.ReadFile(filepath.Join(planningPath, "review.md")); readErr == nil {
		review = string(data)
	}

	input := CloseoutInput{
		Manifest: *manifest,
		Report:   report,
		Overview: overview,
		Review:   review,
	}
	result, closeoutErr := Closeout(ctx, o.deps.Config, input)
	if closeoutErr != nil {
		emitPhaseIndicator(o.deps.Events, manifest.RunID, "", phaseIndicatorBoundary, fmt.Sprintf("closeout: %v", closeoutErr))
		return
	}
	_ = result
}
