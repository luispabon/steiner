package interactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// Handle processes an interactive action. Handles SubmitPrompt, SteerPrompt,
// InterruptActiveRun, ClearConversation, RequestContextReport,
// RequestConfigReport, TriggerManualCompaction, RequestExit, SetSkillEnabled,
// SwitchMode, SwitchModel, SwitchProfile, SubmitApproval, SubmitWorkflowHandoff,
// LoadSession, and requestSessionPicker.
func (s *Session) Handle(ctx context.Context, action Action) error {
	if s.handleImmediateAction(ctx, action) {
		return nil
	}
	if handled, err := s.handleStateAction(ctx, action); handled {
		return err
	}
	return fmt.Errorf("handle: unknown action type %T", action)
}

func (s *Session) handleImmediateAction(ctx context.Context, action Action) bool {
	switch a := action.(type) {
	case SubmitPrompt:
		s.runs.Add(1)
		go func() {
			defer s.runs.Done()
			s.submitPrompt(ctx, a.Text, a.Images)
		}()
		return true
	case SteerPrompt:
		s.runController.Steer(a.Text, a.Images)
		return true
	case InterruptActiveRun:
		s.runController.Interrupt()
		return true
	case RequestContextReport:
		s.emitContextReport(ctx)
		return true
	case RequestConfigReport:
		s.emitConfigReport()
		return true
	case TriggerManualCompaction:
		s.runs.Add(1)
		go func() {
			defer s.runs.Done()
			s.manualCompaction(ctx, a.Steering)
		}()
		return true
	case RequestExit:
		s.exitOnce.Do(func() { close(s.done) })
		return true
	case requestSessionPicker:
		return true
	}
	return false
}

func (s *Session) handleStateAction(ctx context.Context, action Action) (bool, error) {
	switch a := action.(type) {
	case ClearConversation:
		s.SetConversation(nil)
		s.skills.Reset()
		return true, nil
	case SetSkillEnabled:
		s.skills.Set(a.Name, a.Enabled)
		return true, nil
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
	case LoadSession:
		return true, s.loadSession(ctx, a.SessionID)

	case RotateSession:
		return true, s.rotateSession("", false)
	case RotateSessionWithGroup:
		return true, s.rotateSession(a.Group, true)
	case ForkSession:
		return true, s.handleForkSession(ctx)
	case ForkSavedSession:
		return true, s.handleForkSavedSession(ctx, a.SessionID)
	}
	return false, nil
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
		// Stats-write failure must never fail the model switch.
		_ = s.deps.RecordModelSwitch(providerAlias, modelID)
	}
	return nil
}

func (s *Session) handleSwitchProfile(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		err := fmt.Errorf("switch profile: profile name is required")
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
	}
	return err
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
