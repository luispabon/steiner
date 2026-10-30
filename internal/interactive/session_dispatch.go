package interactive

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// Handle processes an interactive action. Handles SubmitPrompt, NotifySteer,
// RecordPromptHistory, InterruptActiveRun, CancelDelegate, CancelAllDelegates,
// ClearConversation, RequestContextReport,
// RequestConfigReport, TriggerManualCompaction, RequestExit, SetSkillEnabled,
// SwitchMode, SwitchOrchestrationLevel, SwitchModel, SwitchProfile,
// SubmitApproval, SubmitWorkflowHandoff, LoadSession, RequestHistoryRecall,
// and requestSessionPicker.
func (s *Session) Handle(ctx context.Context, action Action) error {
	if handled, err := s.handleImmediateAction(ctx, action); handled {
		return err
	}
	if handled, err := s.handleStateAction(ctx, action); handled {
		return err
	}
	return fmt.Errorf("handle: unknown action type %T", action)
}

func (s *Session) handleImmediateAction(ctx context.Context, action Action) (bool, error) {
	if handled, err := s.routeToPhase(action); handled {
		return true, err
	}
	switch a := action.(type) {
	case SubmitPrompt:
		s.submitPrompt(ctx, a.Text, a.Images)
		return true, nil
	case NotifySteer:
		s.currentDriver().NotifySteer()
		return true, nil
	case RecordPromptHistory:
		s.runs.Add(1)
		go func() {
			defer s.runs.Done()
			s.recordHistory(a.Text)
		}()
		return true, nil
	case InterruptActiveRun:
		s.runController.Interrupt()
		s.currentDriver().StopTurn()
		return true, nil
	case CancelDelegate:
		return true, s.cancelDelegate(a)
	case CancelAllDelegates:
		return true, s.cancelAllDelegates()
	case RequestContextReport:
		s.emitContextReport(ctx)
		return true, nil
	case RequestConfigReport:
		s.emitConfigReport()
		return true, nil
	case TriggerManualCompaction:
		s.mu.Lock()
		drv := s.driver.drv
		if state, _ := drv.State(); state == agent.DriverGenerating {
			s.mu.Unlock()
			s.events.Emit(output.NewOverlayReportEvent("Context Report", errRunInProgress.Error()))
			return true, fmt.Errorf("compact: %w", errRunInProgress)
		}
		s.driverAdmissions++
		hook := s.manualAdmissionHook
		s.mu.Unlock()
		if hook != nil {
			hook()
		}
		drv.RequestCompaction(s.manualCompaction(drv, a.Steering))
		s.mu.Lock()
		s.driverAdmissions--
		s.mu.Unlock()
		return true, nil
	case RequestExit:
		s.exitOnce.Do(func() { close(s.done) })
		return true, nil
	case requestSessionPicker:
		return true, nil
	}
	return false, nil
}

func (s *Session) cancelDelegate(a CancelDelegate) error {
	if s.delegateCanceller == nil {
		return fmt.Errorf("no active delegate cancellation available")
	}
	return s.delegateCanceller.CancelAgent(a.AgentID, a.Discard)
}

func (s *Session) cancelAllDelegates() error {
	if s.delegateCanceller == nil {
		return fmt.Errorf("no active delegate cancellation available")
	}
	return s.delegateCanceller.CancelAll()
}

func (s *Session) handleStateAction(ctx context.Context, action Action) (bool, error) {
	switch a := action.(type) {
	case ClearConversation:
		return true, s.clearConversation()
	case SetSkillEnabled:
		return true, s.setSkillEnabled(ctx, a.Name, a.Enabled)
	case SubmitApproval:
		s.approvalCoordinator.Submit(a)
		return true, nil
	case SubmitWorkflowHandoff:
		s.handoffCoordinator.Submit(a)
		return true, nil
	case SwitchModel:
		return true, s.handleSwitchModel(a.Name, a.Reasoning)
	case SwitchProfile:
		return true, s.handleSwitchProfile(a.Name)
	case SwitchMode:
		s.SetMode(a.Mode)
		return true, nil
	case SwitchOrchestrationLevel:
		return true, s.SetOrchestrationLevel(a.Level)
	case LoadSession:
		return true, s.loadSession(ctx, a.SessionID)

	case RotateSession:
		return true, s.rotateGuarded("", false)
	case RotateSessionWithGroup:
		return true, s.rotateGuarded(a.Group, true)
	case ForkSession:
		return true, s.handleForkSession(ctx)
	case ForkSavedSession:
		return true, s.handleForkSavedSession(ctx, a.SessionID)
	case RequestHistoryRecall:
		s.emitHistoryRecall()
		return true, nil
	}
	return false, nil
}

func (s *Session) clearConversation() error {
	s.mu.Lock()
	if err := s.clearGuardLocked("clear conversation"); err != nil {
		s.mu.Unlock()
		return s.reportReplacementGuardError("clear conversation", err)
	}
	var id string
	if s.deps.SessionStore != nil {
		var err error
		id, err = generateSessionID()
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("clear conversation: generate session id: %w", err)
		}
	}
	old := s.swapDriverLocked(func() {
		s.resetConversationLocked()
		s.skills.Reset()
		if id != "" {
			s.applyRotationLocked(id, "", false)
		}
	})
	s.mu.Unlock()
	s.retireDriver(old)
	return nil
}

func (s *Session) rotateGuarded(group string, updateGroup bool) error {
	return s.rotateSession(group, updateGroup)
}

// errClearPending is the refusal a wired supervisor clear hook returns when it
// cannot atomically discard every pending sub-agent.
var errClearPending = errors.New("sub-agents from another scope or still running or queued; wait for them or stop them first")

// clearGuardLocked checks whether clear may replace the conversation. It keeps
// the controller admission and driver Busy checks shared by every replacement,
// then either applies the existing pending refusal or, when the supervisor
// clear hook is wired, holds completions and lets the supervisor atomically
// discard finished jobs from the current scope. The caller must hold s.mu and
// keep it held through the swap.
func (s *Session) clearGuardLocked(action string) error {
	if s.driverAdmissions > 0 || s.driver.drv.Busy() {
		return fmt.Errorf("%s: %w", action, errRunInProgress)
	}
	if s.deps.ClearFinishedSubAgents == nil {
		return s.pendingRefusalLocked(action)
	}
	if err := s.clearPendingRefusalLocked(action); err != nil {
		return err
	}
	// Hold completions before the atomic discard, so a completion already in
	// flight cannot wake the waiting driver while the clear swaps drivers.
	s.driver.drv.StopTurn()
	// StopTurn cancels a run that started after the Busy check above; its state
	// remains generating until the run returns, so this refuses that race.
	if s.driver.drv.Busy() {
		return fmt.Errorf("%s: %w", action, errRunInProgress)
	}
	if s.deps.ClearFinishedSubAgents(s.driver.groupScope) != 0 {
		return fmt.Errorf("%s: %w", action, errClearPending)
	}
	return nil
}

// clearPendingRefusalLocked refuses a clear while a sub-agent is still running
// or queued, before StopTurn can change the driver's observable hold state.
// Finished jobs are left to ClearFinishedSubAgents, which decides atomically
// whether every pending job is discardable. The caller must hold s.mu.
func (s *Session) clearPendingRefusalLocked(action string) error {
	if s.deps.Background == nil {
		return nil
	}
	running, queued := 0, 0
	for _, pending := range s.deps.Background.Pending() {
		switch pending.State {
		case agent.SubAgentRunning:
			running++
		case agent.SubAgentQueued:
			queued++
		}
	}
	if running == 0 && queued == 0 {
		return nil
	}
	parts := make([]string, 0, 2)
	if running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", running))
	}
	if queued > 0 {
		parts = append(parts, fmt.Sprintf("%d queued", queued))
	}
	return fmt.Errorf("%s: sub-agents still pending (%s); wait for them or stop them first", action, strings.Join(parts, ", "))
}

func (s *Session) handleSwitchModel(name string, reasoning *provider.ReasoningOverride) error {
	s.mu.Lock()
	providerAlias, modelID, err := config.ParseModelReference(&s.deps.Config, name)
	if err != nil {
		s.mu.Unlock()
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("Model switch failed: %v", err)))
		return err
	}
	s.deps.Config.Models.Effective.ActiveOrchestratorModel = name
	if reasoning != nil {
		s.reasoningOverrides[name] = *reasoning
	}
	s.mu.Unlock()

	if s.deps.RecordModelSwitch != nil {
		// Stats-write failure must never fail the model switch; warn instead.
		if err := s.deps.RecordModelSwitch(providerAlias, modelID); err != nil {
			s.events.Emit(output.NewConfigWarningEvent(fmt.Sprintf("could not record model switch to %q for popularity stats: %v", name, err)))
		}
	}
	return nil
}

func (s *Session) handleSwitchProfile(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		err := errors.New("switch profile: profile name is required")
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("Profile switch failed: %v", err)))
		return err
	}

	s.mu.Lock()
	candidate, err := config.ResolveEffectiveAssignments(&s.deps.Config, name)
	if err == nil {
		candidate.ActiveOrchestratorModel = s.deps.Config.Models.Effective.ActiveOrchestratorModel
		s.deps.Config.Models.Effective = candidate
	}
	s.mu.Unlock()
	if err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("Profile switch failed: %v", err)))
		return err
	}
	if s.deps.OnEffectiveAssignmentsChanged != nil {
		s.deps.OnEffectiveAssignmentsChanged(candidate)
	}
	return nil
}

// Run enters the interactive session loop. It loads history if a writer is
// configured, then blocks until the context is cancelled or RequestExit is
// handled.
func (s *Session) Run(ctx context.Context) error {
	if s.deps.HistoryWriter != nil {
		prompts, err := s.deps.HistoryWriter.Load()
		if err != nil {
			s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("failed to load history: %v", err)},
			}))
		}
		s.events.Emit(output.NewHistoryLoadedEvent(prompts))
	}

	select {
	case <-ctx.Done():
	case <-s.done:
	}
	return nil
}
