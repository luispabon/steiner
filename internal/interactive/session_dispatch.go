package interactive

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/output"
)

// Handle processes an interactive action. Handles SubmitPrompt, SteerPrompt,
// InterruptActiveRun, ClearConversation, RequestContextReport,
// RequestConfigReport, TriggerManualCompaction, RequestExit, SetSkillEnabled,
// SwitchModel, SubmitApproval, SubmitWorkflowHandoff, LoadSession, and requestSessionPicker.
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
		go s.submitPrompt(ctx, a.Text, a.Images)
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
		go s.manualCompaction(ctx)
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
		return true, s.handleSwitchModel(a.Name)
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

func (s *Session) handleSwitchModel(name string) error {
	s.mu.Lock()
	if _, ok := s.deps.Config.Models[name]; !ok {
		s.mu.Unlock()
		err := fmt.Errorf("model %q not found in config", name)
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("Model switch failed: %v", err)))
		return err
	}
	s.deps.Config.DefaultModel = name
	s.mu.Unlock()
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
