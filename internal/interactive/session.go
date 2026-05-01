package interactive

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/output"
)

// Session is an interactive-mode session that owns conversation state,
// approvals, model switches, compaction, and run lifecycle.
// Currently a skeleton; behavior will be moved here in later stages.
type Session struct {
	deps Dependencies
	sink *output.ForwardSink
}

// NewSession creates a new interactive Session with the given dependencies.
func NewSession(deps Dependencies) *Session {
	return &Session{
		deps: deps,
		sink: deps.DisplaySink,
	}
}

// EventSink returns the session's event sink for external consumers to attach
// to the session's event stream.
func (s *Session) EventSink() output.EventSink {
	return s.sink
}

// Handle processes an interactive action. Currently a no-op placeholder that
// returns nil for all recognized action types. Behavior will be implemented
// in later stages.
func (s *Session) Handle(ctx context.Context, action Action) error {
	switch action.(type) {
	case SubmitPrompt,
		RequestContextReport,
		RequestConfigReport,
		SubmitApproval,
		InterruptActiveRun,
		RequestExit,
		SetSkillEnabled,
		SwitchModel,
		ClearConversation,
		TriggerManualCompaction:
		return nil
	default:
		return fmt.Errorf("handle: unknown action type %T", action)
	}
}

// Run enters the interactive session loop. Currently a no-op placeholder.
func (s *Session) Run(ctx context.Context) error {
	return nil
}

// Close releases any resources held by the session. Currently a no-op
// placeholder.
func (s *Session) Close() error {
	return nil
}
