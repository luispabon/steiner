package interactive

import (
	"fmt"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
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
	handoffCoordinator  *WorkflowHandoffCoordinator
	conversation        []agent.Message
	lineage             agent.ConversationLineage
	sessionID           string
	sessionTitle        string
	sessionGroup        string
	done                chan struct{}
	exitOnce            sync.Once
}

// NewSession creates a new interactive Session with the given dependencies.
// It composes the session-level event bus: display_file forwarding, API
// request snapshot capture, and any caller-provided base events. It generates
// a unique session ID via crypto/rand.
func NewSession(deps Dependencies) (*Session, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}

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
		runController:       NewActiveRunController(),
		skills:              NewSkills(deps.SkillNames),
		snapshots:           snaps,
		approvalCoordinator: &ApprovalCoordinator{},
		handoffCoordinator:  &WorkflowHandoffCoordinator{},
		sessionID:           sessionID,
		lineage:             agent.ConversationLineage{},
		done:                make(chan struct{}),
	}, nil
}

// EventSink returns the session's composed event sink for external consumers
// to attach to the session's event stream.
func (s *Session) EventSink() output.EventSink {
	return s.events
}

// ProjectRoot returns the session's project root directory.
func (s *Session) ProjectRoot() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.WorkDir
}

// Config returns a copy of the session's configuration.
func (s *Session) Config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.Config
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

// WorkflowHandoffCoordinator returns the session's workflow handoff
// coordinator, which manages pending handoff requests.
func (s *Session) WorkflowHandoffCoordinator() *WorkflowHandoffCoordinator {
	return s.handoffCoordinator
}

// Approver returns an tool.ApprovalResponder that routes tool approval
// requests through the session's ApprovalCoordinator and emits approval
// events to the given sink.
func (s *Session) Approver(eventSink output.EventSink) tool.ApprovalResponder {
	return agent.NewEventingApprover(eventSink, newApprovalResponder(s.approvalCoordinator))
}

// WorkflowHandoffResponder returns a responder that routes workflow handoff
// decisions through the session's coordinator.
func (s *Session) WorkflowHandoffResponder(eventSink output.EventSink) tool.WorkflowHandoffResponder {
	return newWorkflowHandoffResponder(s.handoffCoordinator, eventSink)
}

// CurrentModelAlias returns the currently active model alias.
func (s *Session) CurrentModelAlias() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.Config.DefaultModel
}

// WorkflowHandoffModelSelection returns the configured handoff model for the
// destination when present, otherwise the current session model alias.
func (s *Session) WorkflowHandoffModelSelection(destination string) WorkflowHandoffModelSelection {
	s.mu.RLock()
	defer s.mu.RUnlock()

	current := strings.TrimSpace(s.deps.Config.DefaultModel)
	selection := WorkflowHandoffModelSelection{
		ModelAlias:  current,
		SourceLabel: "current session",
	}

	destination = strings.TrimSpace(destination)
	if destination == "" {
		return selection
	}

	alias := strings.TrimSpace(s.deps.Config.WorkflowHandoff.Models[destination])
	if alias == "" {
		return selection
	}
	if _, ok := s.deps.Config.Models[alias]; !ok {
		return selection
	}

	selection.ModelAlias = alias
	selection.SourceLabel = "from handoff default"
	return selection
}

// CaveHuman returns whether cave_human-style terse prompting is enabled.
func (s *Session) CaveHuman() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.Config.CaveHuman
}

// CurrentModelConfig returns the currently active model config.
func (s *Session) CurrentModelConfig() config.ModelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.Config.Models[s.deps.Config.DefaultModel]
}

// Conversation returns a defensive copy of the current conversation.
func (s *Session) Conversation() []agent.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMessages(s.conversation)
}

// SetConversation replaces the current conversation with a defensive copy.
func (s *Session) SetConversation(conversation []agent.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conversation = cloneMessages(conversation)
}

// SetRunner replaces the session's run executor. This allows the CLI adapter
// to create the session without a runner, build the interactive registry using
// the session's display sink, and then wire the runner in with the correct
// registry and approver.
func (s *Session) SetRunner(runner runExecutor) {
	s.deps.Runner = runner
}

// SessionID returns the current session's unique identifier.
func (s *Session) SessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

// SessionTitle returns the current session's title, which is empty until
// the first prompt is submitted or a saved session is loaded.
func (s *Session) SessionTitle() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionTitle
}

func usagePercent(promptTokens, contextWindow int) float64 {
	if promptTokens <= 0 || contextWindow <= 0 {
		return 0
	}
	return float64(promptTokens) / float64(contextWindow) * 100
}

func sessionLoadedStatus(contextWindow int) string {
	if contextWindow <= 0 {
		return "unknown_context"
	}
	return "ok"
}
