package oneshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/agent"
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
		o.reportFailureIfNeeded(ctx, &manifest, err)
	}()

	store := o.manifestStore()

	lock, err := o.deps.RunLockFactory(o.deps.ProjectRoot, o.deps.Identity)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
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
		WorktreeBase:  worktree.StartPoint,
		ModelSnapshot: phaseModelSnapshot(o.deps.Config),
		PhaseStatuses: map[Phase]PhaseStatus{
			PhasePlan:      PhaseStatusPending,
			PhaseImplement: PhaseStatusPending,
			PhaseReview:    PhaseStatusPending,
		},
		PhaseSessionIDs: map[Phase]string{},
		// A new run starts with a report pending so an interrupted
		// finalization is detectable before any external effect.
		FinalizationVersion: finalizationVersion,
		ReportState:         reportStatePending,
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

		if err := o.runPhase(runPhaseParams{
			InterruptCtx:  interruptCtx,
			Store:         store,
			Manifest:      &manifest,
			Lock:          lock,
			WorktreePath:  worktreePath,
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

// reportFailureIfNeeded generates a failure report if Run is returning with
// a non-nil error on a manifest that has already been assigned a RunID.
func (o *Orchestrator) reportFailureIfNeeded(ctx context.Context, manifest *Manifest, err error) {
	if err != nil && manifest.RunID != "" {
		o.tryFailureReport(ctx, o.manifestStore(), manifest)
	}
}

func (o *Orchestrator) manifestStore() *ManifestStore {
	if o.deps.ManifestStore != nil {
		return o.deps.ManifestStore
	}
	return NewManifestStore(o.deps.Identity.ManifestPath(o.deps.ProjectRoot))
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
}

type phaseFailureOptions struct {
	IndicatorState       string
	IndicatorMessage     string
	ModelAlias           string
	SessionID            string
	Transition           bool
	IndicatorBeforeWrite bool
	// SkipManifestWrite leaves run.json untouched, for when the run lock was
	// lost and the manifest belongs to another process.
	SkipManifestWrite bool
}

func (o *Orchestrator) finalizePhaseFailure(p runPhaseParams, cancel context.CancelFunc, failure error, opts phaseFailureOptions) error {
	if cancel != nil {
		cancel()
	}
	p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusFailed
	if opts.IndicatorBeforeWrite {
		emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, opts.IndicatorState, opts.IndicatorMessage)
	}
	// After a lost lock run.json belongs to whichever process reclaimed it,
	// so emit the events but leave the manifest alone.
	if !opts.SkipManifestWrite && !errors.Is(failure, errLockLost) {
		if err := p.Store.Write(*p.Manifest); err != nil {
			return err
		}
	}
	if !opts.IndicatorBeforeWrite {
		emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, opts.IndicatorState, opts.IndicatorMessage)
	}
	if opts.Transition {
		emitPhaseTransition(o.deps.Events, p.Manifest.RunID, p.Phase, p.Phase, phaseTransitionFailed, opts.ModelAlias, opts.SessionID)
	}
	return failure
}

// runPhase executes a single phase: starting events, runner construction, execution, session
// persistence, boundary checking, and success bookkeeping. It mutates manifest and writes it via
// store at each state transition, and heartbeats lock at phase start and on successful completion.
func (o *Orchestrator) runPhase(p runPhaseParams) error {
	modelAlias := phaseModelAlias(o.deps.Config, p.Phase)
	advisorCfg := phaseAdvisorConfig(o.deps.Config, p.Phase)
	emitPhaseTransition(o.deps.Events, p.Manifest.RunID, p.PreviousPhase, p.Phase, phaseTransitionStarting, modelAlias, "")
	emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, phaseIndicatorStarting, "phase starting")

	if err := p.Lock.Heartbeat(); err != nil {
		return o.finalizePhaseFailure(p, nil, err, phaseFailureOptions{
			IndicatorState:   phaseIndicatorCancelled,
			IndicatorMessage: err.Error(),
			ModelAlias:       modelAlias,
			Transition:       true,
		})
	}
	p.Manifest.CurrentPhase = p.Phase
	p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusRunning
	if err := p.Store.Write(*p.Manifest); err != nil {
		p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusFailed
		return err
	}

	phaseCtx, cancel := context.WithCancel(p.InterruptCtx)
	// A phase has no user to answer an approval prompt, so it runs without an
	// approval responder. The executor treats a nil responder as a denial: a
	// path outside the project root fails with a policy error instead of being
	// approved, and a sandbox denial is returned without an unsandboxed retry.
	// In-root mutate calls need no approval, so they still succeed.
	runner, err := o.deps.RunnerFactory.NewPhaseRunner(phaseCtx, p.Phase, modelAlias, nil, advisorCfg)
	if err != nil {
		return o.finalizePhaseFailure(p, cancel, err, phaseFailureOptions{
			IndicatorState:       phaseIndicatorBoundary,
			IndicatorMessage:     err.Error(),
			IndicatorBeforeWrite: true,
		})
	}

	// The session exists and is linked in the manifest before the phase runs,
	// so a crash or failed phase leaves its partial conversation inspectable.
	phaseSession, err := o.newPhaseSession(p.Phase, modelAlias)
	if err != nil {
		return o.finalizePhaseFailure(p, cancel, err, phaseFailureOptions{
			IndicatorState:   phaseIndicatorBoundary,
			IndicatorMessage: err.Error(),
		})
	}
	sessionID := phaseSession.id()
	p.Manifest.PhaseSessionIDs[p.Phase] = sessionID
	if err := p.Store.Write(*p.Manifest); err != nil {
		return o.finalizePhaseFailure(p, cancel, err, phaseFailureOptions{
			IndicatorState:    phaseIndicatorBoundary,
			IndicatorMessage:  err.Error(),
			SkipManifestWrite: true,
		})
	}

	conversation := phaseConversation(o.deps.Identity, o.deps.Task, p.Phase, p.WorktreePath, p.PlanningPath)
	stopHeartbeat := o.startPhaseHeartbeat(p.Lock, cancel)
	result, runErr := runner.RunPhase(phaseCtx, PhaseRunInput{
		Conversation:    conversation,
		Session:         PhaseSession{ID: sessionID, Save: phaseSession.save},
		RegisterControl: o.deps.RegisterControl,
	})
	hbErr := stopHeartbeat()
	if hbErr != nil {
		// The heartbeat cancelled phaseCtx, so runErr is usually a context
		// error; report the root cause instead.
		runErr = hbErr
	}

	if saveErr := phaseSession.saveResult(result); saveErr != nil {
		return o.finalizePhaseFailure(p, cancel, saveErr, phaseFailureOptions{
			IndicatorState:    phaseIndicatorBoundary,
			IndicatorMessage:  saveErr.Error(),
			SkipManifestWrite: errors.Is(hbErr, errLockLost),
		})
	}

	if runErr != nil {
		return o.finalizePhaseFailure(p, cancel, runErr, phaseFailureOptions{
			IndicatorState:   phaseIndicatorCancelled,
			IndicatorMessage: runErr.Error(),
			ModelAlias:       modelAlias,
			SessionID:        sessionID,
			Transition:       true,
		})
	}

	requiredArtifacts := requiredArtifactsForPhase(p.Phase, p.PlanningPath)
	if err := CheckBoundary(phaseCtx, p.Phase, p.WorktreePath, requiredArtifacts); err != nil {
		return o.finalizePhaseFailure(p, cancel, err, phaseFailureOptions{
			IndicatorState:   phaseIndicatorBoundary,
			IndicatorMessage: err.Error(),
			ModelAlias:       modelAlias,
			SessionID:        sessionID,
			Transition:       true,
		})
	}

	cancel()
	// Heartbeat again here (in addition to phase start) so the lock stays fresh
	// across the gap between finishing this phase and starting the next one,
	// matching resumeFromManifest's original behavior before this loop was unified.
	if err := p.Lock.Heartbeat(); err != nil {
		return o.finalizePhaseFailure(p, nil, err, phaseFailureOptions{
			IndicatorState:   phaseIndicatorCancelled,
			IndicatorMessage: err.Error(),
			ModelAlias:       modelAlias,
			SessionID:        sessionID,
			Transition:       true,
		})
	}
	p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusDone
	p.Manifest.CurrentPhase = p.Phase
	if err := p.Store.Write(*p.Manifest); err != nil {
		// Not routed through finalizePhaseFailure, which would retry the write
		// that just failed. The persisted status stays Running, so resume works.
		p.Manifest.PhaseStatuses[p.Phase] = PhaseStatusFailed
		emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, phaseIndicatorCancelled, err.Error())
		emitPhaseTransition(o.deps.Events, p.Manifest.RunID, p.Phase, p.Phase, phaseTransitionFailed, modelAlias, sessionID)
		return err
	}
	emitPhaseIndicator(o.deps.Events, p.Manifest.RunID, p.Phase, phaseIndicatorCompleted, "phase complete")
	emitPhaseTransition(o.deps.Events, p.Manifest.RunID, p.Phase, p.Phase, phaseTransitionCompleted, modelAlias, sessionID)
	return nil
}

// startPhaseHeartbeat refreshes lock every heartbeat interval until the
// returned stop function is called. stop halts the goroutine, waits for it to
// exit and returns the first heartbeat error. On a heartbeat failure (including
// errLockLost) the goroutine calls cancel so the running phase aborts.
func (o *Orchestrator) startPhaseHeartbeat(lock *RunLock, cancel context.CancelFunc) func() error {
	interval := o.heartbeatInterval
	if interval <= 0 {
		interval = defaultLockStaleAfter / 3
	}
	done := make(chan struct{})
	exited := make(chan struct{})
	var hbErr error
	go func() {
		defer close(exited)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := lock.Heartbeat(); err != nil {
					hbErr = err
					cancel()
					return
				}
			}
		}
	}()
	return func() error {
		close(done)
		<-exited
		return hbErr
	}
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
func (o *Orchestrator) tryFailureReport(ctx context.Context, store *ManifestStore, manifest *Manifest) {
	if manifest == nil || strings.TrimSpace(manifest.RunID) == "" {
		return
	}
	// The final report is already on disk, so a later closeout or persistence
	// failure must not overwrite it with a failure report.
	if manifest.ReportState == reportStateWritten {
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
	if store != nil {
		if err := store.Write(*manifest); err != nil {
			emitPhaseIndicator(o.deps.Events, manifest.RunID, "", phaseIndicatorBoundary, fmt.Sprintf("write manifest with failure report: %v", err))
		}
	}
}

// closeoutOutcomeUnknown reports whether an external closeout may have run
// without a recorded result. Legacy manifests with no marker cannot prove it
// did not run.
func closeoutOutcomeUnknown(manifest Manifest) bool {
	if manifest.CloseoutIntent == closeoutIntentPending {
		return true
	}
	// A persisted unknown state outranks the version marker: a versioned run
	// records it when a manual-required finalization aborts, and the status must
	// survive later attempts.
	if manifest.CloseoutState == closeoutStateUnknown {
		return true
	}
	if manifest.FinalizationVersion > 0 {
		return false
	}
	return manifest.CloseoutState == "" || manifest.CloseoutState == closeoutStateFailed
}

// finalizeRun generates the final report and runs closeout for a successful run.
//
// Progress is persisted before each external effect: the written report is
// always on disk before closeout runs, and a closeout that can reach a provider
// runner records its intent first, so an interrupted or failed finalization is
// never retried blindly.
func (o *Orchestrator) finalizeRun(ctx context.Context, store *ManifestStore, manifest *Manifest, planningPath string) error {
	if manifest == nil || strings.TrimSpace(manifest.RunID) == "" {
		return nil
	}
	// Decide before upgrading the version fields: writing them records that the
	// outcome of an earlier closeout is unknown.
	manualRequired := closeoutOutcomeUnknown(*manifest)

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
		return fmt.Errorf("generate final report: %w", err)
	}
	// Decide whether closeout can reach a provider runner before persisting:
	// the intent belongs in the same write as the report state, so a crash
	// cannot leave a versioned manifest that looks settled while an external
	// request is still possible.
	attemptsExternal, _ := closeoutExternalAttempt(o.deps.Config, report)

	manifest.ReportPath = report.ReportPath
	manifest.ReportState = reportStateWritten
	manifest.FinalizationVersion = finalizationVersion
	if !manualRequired && attemptsExternal {
		manifest.CloseoutIntent = closeoutIntentPending
	}
	if err := store.Write(*manifest); err != nil {
		emitPhaseIndicator(o.deps.Events, manifest.RunID, "", phaseIndicatorBoundary, fmt.Sprintf("persist report state: %v", err))
		return fmt.Errorf("persist report state: %w", err)
	}
	// Invariant: for a run that is not already ambiguous (no manualRequired)
	// and whose closeout can reach a provider runner (attemptsExternal), a
	// report state of written with no closeout intent and no closeout result is
	// unreachable, because both fields land in the write above. An ambiguous
	// run instead proceeds to the second write below that records closeout_state
	// unknown; a crash inside that narrower window relies on the operator-facing
	// unknown marker being written next. For a run whose closeout is provably
	// skipped (no auto_pr or no completed report) a crash right here leaves the
	// run settled, which is safe: no external request was possible. A crash
	// after this write but before Closeout leaves the intent pending, so an
	// operator resolves the run manually even though no request may exist.
	// Over-reporting is the intended conservative direction.

	if manualRequired {
		// Persist the unknown state before returning: the report write above also
		// stamped the finalization version, so without this marker a second
		// attempt would treat the run as settled.
		manifest.CloseoutState = closeoutStateUnknown
		manifest.CloseoutNote = "closeout outcome unknown; manual resolution required"
		if err := store.Write(*manifest); err != nil {
			emitPhaseIndicator(o.deps.Events, manifest.RunID, "", phaseIndicatorBoundary, fmt.Sprintf("persist unknown closeout state: %v", err))
			return fmt.Errorf("persist unknown closeout state: %w", err)
		}
		emitPhaseIndicator(o.deps.Events, manifest.RunID, "", phaseIndicatorBoundary, "closeout outcome unknown; manual resolution required")
		return fmt.Errorf("closeout outcome for run %s is unknown; record it or authorize a retry with steiner oneshot --resolve", manifest.RunID)
	}

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
		// The intent stays pending: closeout may have created the PR/MR before
		// failing, so only an operator can resolve this run.
		manifest.CloseoutState = closeoutStateUnknown
		manifest.CloseoutNote = closeoutErr.Error()
		closeoutFailure := fmt.Errorf("closeout run %s: %w", manifest.RunID, closeoutErr)
		if err := store.Write(*manifest); err != nil {
			emitPhaseIndicator(o.deps.Events, manifest.RunID, "", phaseIndicatorBoundary, fmt.Sprintf("persist unknown closeout state: %v", err))
			return errors.Join(closeoutFailure, fmt.Errorf("persist unknown closeout state: %w", err))
		}
		return closeoutFailure
	}

	manifest.CloseoutURL = result.URL
	manifest.CloseoutProvider = result.Provider
	manifest.CloseoutState = result.State
	manifest.CloseoutNote = result.Note
	manifest.CloseoutIntent = ""
	if err := store.Write(*manifest); err != nil {
		emitPhaseIndicator(o.deps.Events, manifest.RunID, "", phaseIndicatorBoundary, fmt.Sprintf("persist closeout result: %v", err))
		return fmt.Errorf("persist closeout result: %w", err)
	}
	return nil
}
