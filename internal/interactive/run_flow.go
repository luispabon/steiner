package interactive

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// submitPrompt handles a user-submitted prompt during an interactive session.
// It appends the user message, starts a cancellable model run, records history
// on success, and emits stop/error and history events consistently.
func (s *Session) submitPrompt(ctx context.Context, text string) {
	s.mu.Lock()
	s.conversation = append(s.conversation, agent.Message{Role: agent.MessageRoleUser, Content: text})
	s.mu.Unlock()

	err := s.runWithInterruptOwnership(ctx, func(runCtx context.Context) error {
		result, err := s.deps.Runner.Run(runCtx, s.Conversation(), s.skills.Snapshot())
		if err != nil {
			return err
		}
		s.SetConversation(result)
		return nil
	})

	if err != nil {
		s.events.Emit(output.NewStopReasonEvent(0, fmt.Sprintf("Error: %v", err), err))
		return
	}

	if s.deps.HistoryWriter != nil {
		if err := s.deps.HistoryWriter.Record(text); err != nil {
			s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("history record: %v", err)},
			}))
		}
		prompts, err := s.deps.HistoryWriter.Load()
		if err != nil {
			s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("history load: %v", err)},
			}))
			prompts = nil
		}
		s.events.Emit(output.NewHistoryLoadedEvent(prompts))
	}
}

// runWithInterruptOwnership executes a run function with a cancellable context
// and manages the lifecycle of the run controller. It creates a derived context,
// registers the cancel function, runs the function, and ensures cleanup via
// cancel and controller clear.
func (s *Session) runWithInterruptOwnership(ctx context.Context, run func(context.Context) error) error {
	runCtx, cancel := context.WithCancel(ctx)
	s.runController.Set(cancel)
	defer func() {
		cancel()
		s.runController.Clear()
	}()
	return run(runCtx)
}
