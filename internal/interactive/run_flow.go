package interactive

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

// submitPrompt handles a user-submitted prompt during an interactive session.
// It appends the user message, starts a cancellable model run, records history
// on success, updates session lineage and title, and saves the session.
// Emits stop/error and history events consistently.
func (s *Session) submitPrompt(ctx context.Context, text string, images []agent.ImageBlock) {
	s.mu.Lock()
	isFirstPrompt := len(s.conversation) == 0
	s.conversation = append(s.conversation, agent.Message{Role: agent.MessageRoleUser, Content: text, Images: images})
	s.mu.Unlock()

	err := s.runWithInterruptOwnership(ctx, func(runCtx context.Context) error {
		drainSteers := func() []agent.SteerMessage {
			return s.runController.DrainSteers()
		}
		conversation := s.Conversation()
		notice := s.modeNotice()
		if notice != "" && len(conversation) > 0 && conversation[len(conversation)-1].Role == agent.MessageRoleUser {
			conversation[len(conversation)-1].Content = notice + conversation[len(conversation)-1].Content
		}
		result, err := s.deps.Runner.Run(runCtx, conversation, s.skills.Snapshot(), drainSteers)

		s.mu.Lock()
		if len(result.Conversation) > 0 {
			if result.WorkflowHandoff == nil {
				s.conversation = result.Conversation
			}
			s.lineage = agent.ConversationLineage{
				Generations: []agent.ConversationGeneration{
					{ID: 1, SummaryPrefix: nil, Messages: cloneMessages(result.Conversation)},
				},
				NextGenerationID: 2,
			}
		}
		s.mu.Unlock()

		if err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		s.events.Emit(output.NewStopReasonEvent(0, fmt.Sprintf("Error: %v", err), err))
		return
	}

	if isFirstPrompt && s.deps.SessionStore != nil {
		s.mu.Lock()
		s.sessionTitle = session.TitleFromPrompt(text)
		s.mu.Unlock()
	}

	if s.deps.SessionStore != nil {
		if err := s.saveSession(); err != nil {
			s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("save session: %v", err)},
			}))
		}
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

// cloneMessages returns a deep copy of a message slice.
func cloneMessages(messages []agent.Message) []agent.Message {
	if messages == nil {
		return nil
	}
	out := make([]agent.Message, len(messages))
	copy(out, messages)
	return out
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
