package interactive

import (
	"context"
	"fmt"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// Session is an interactive-mode session that owns conversation state,
// run lifecycle, approvals, model switches, compaction, enabled skills,
// and the core event bus composition.
type Session struct {
	mu                  sync.RWMutex
	deps                Dependencies
	events              output.EventSink
	displaySink         *output.ForwardSink
	runController       *ActiveRunController
	skills              *Skills
	snapshots           *SnapshotStore
	approvalCoordinator *ApprovalCoordinator
	conversation        []agent.Message
}

// NewSession creates a new interactive Session with the given dependencies.
// It composes the session-level event bus: display_file forwarding, API
// request snapshot capture, and any caller-provided base events.
func NewSession(deps Dependencies) *Session {
	displaySink := output.NewForwardSink()
	snaps := &SnapshotStore{}

	events := output.NewMultiSink(
		deps.BaseEvents,
		displaySink,
		&snapshotSink{store: snaps},
	)

	return &Session{
		deps:                deps,
		events:              events,
		displaySink:         displaySink,
		runController:       &ActiveRunController{},
		skills:              NewSkills(deps.SkillNames),
		snapshots:           snaps,
		approvalCoordinator: &ApprovalCoordinator{},
	}
}

// EventSink returns the session's composed event sink for external consumers
// to attach to the session's event stream.
func (s *Session) EventSink() output.EventSink {
	return s.events
}

// DisplaySink returns the session's ForwardSink, which forwards events to
// whatever target is set via Set. The display_file tool uses this to emit
// display events before the TUI sink is wired in.
func (s *Session) DisplaySink() *output.ForwardSink {
	return s.displaySink
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

// Approver returns an tool.ApprovalResponder that routes tool approval
// requests through the session's ApprovalCoordinator and emits approval
// events to the given sink.
func (s *Session) Approver(eventSink output.EventSink) tool.ApprovalResponder {
	return agent.NewEventingApprover(eventSink, newApprovalResponder(s.approvalCoordinator))
}

// Conversation returns the current conversation message slice. Callers must
// not mutate the returned slice; use SetConversation to replace it.
func (s *Session) Conversation() []agent.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conversation
}

// SetConversation replaces the current conversation with the given messages.
func (s *Session) SetConversation(conversation []agent.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversation = conversation
}

// Handle processes an interactive action. SubmitPrompt, InterruptActiveRun,
// and ClearConversation are handled by the session; all other actions are
// currently no-ops and return nil.
func (s *Session) Handle(ctx context.Context, action Action) error {
	switch a := action.(type) {
	case SubmitPrompt:
		go s.submitPrompt(ctx, a.Text)
		return nil
	case InterruptActiveRun:
		s.runController.Interrupt()
		return nil
	case ClearConversation:
		s.SetConversation(nil)
		return nil
	case RequestContextReport:
		s.emitContextReport(ctx)
		return nil
	case RequestConfigReport:
		s.emitConfigReport()
		return nil
	case TriggerManualCompaction:
		go s.manualCompaction(ctx)
		return nil
	case RequestExit:
		return nil
	case SetSkillEnabled:
		s.skills.Set(a.Name, a.Enabled)
		return nil
	case SubmitApproval,
		SwitchModel:
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
