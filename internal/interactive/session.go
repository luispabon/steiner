package interactive

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// Session is an interactive-mode session that owns conversation state,
// run lifecycle, approvals, model switches, compaction, and enabled skills.
// Currently a skeleton; behavior will be moved here in later stages.
type Session struct {
	deps                Dependencies
	sink                *output.ForwardSink
	runController       *ActiveRunController
	skills              *Skills
	snapshots           *SnapshotStore
	approvalCoordinator *ApprovalCoordinator
	conversation        []agent.Message
}

// NewSession creates a new interactive Session with the given dependencies.
func NewSession(deps Dependencies) *Session {
	return &Session{
		deps:                deps,
		sink:                deps.DisplaySink,
		runController:       &ActiveRunController{},
		skills:              NewSkills(deps.SkillNames),
		snapshots:           &SnapshotStore{},
		approvalCoordinator: &ApprovalCoordinator{},
	}
}

// EventSink returns the session's event sink for external consumers to attach
// to the session's event stream.
func (s *Session) EventSink() output.EventSink {
	return s.sink
}

// ActiveRunController returns the session's run controller, which manages
// cancellation of the currently active model run.
func (s *Session) ActiveRunController() *ActiveRunController {
	return s.runController
}

// Skills returns the session's skills tracker, which records which skills are
// enabled.
func (s *Session) Skills() *Skills {
	return s.skills
}

// SnapshotStore returns the session's request-context snapshot store.
func (s *Session) SnapshotStore() *SnapshotStore {
	return s.snapshots
}

// ApprovalCoordinator returns the session's approval coordinator, which
// manages pending approval requests.
func (s *Session) ApprovalCoordinator() *ApprovalCoordinator {
	return s.approvalCoordinator
}

// Conversation returns the current conversation message slice. Callers must
// not mutate the returned slice; use SetConversation to replace it.
func (s *Session) Conversation() []agent.Message {
	return s.conversation
}

// SetConversation replaces the current conversation with the given messages.
func (s *Session) SetConversation(conversation []agent.Message) {
	s.conversation = conversation
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
