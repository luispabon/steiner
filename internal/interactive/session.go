package interactive

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// Session is an interactive-mode session that owns conversation state,
// run lifecycle, approvals, model switches, execution mode, compaction, enabled skills,
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
	reasoningOverrides  map[string]provider.ReasoningOverride
	mode                config.ExecutionMode
	modeListener        func(config.ExecutionMode)
	done                chan struct{}
	runs                sync.WaitGroup
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

	mode := deps.Config.Modes.Default
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
		reasoningOverrides:  make(map[string]provider.ReasoningOverride),
		mode:                mode,
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

// ApprovalHeadIdentity returns the identity of the next FIFO approval.
func (s *Session) ApprovalHeadIdentity() string {
	return s.approvalCoordinator.HeadIdentity()
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
	return newWorkflowHandoffResponder(s.handoffCoordinator, eventSink, s)
}

// CurrentModelAlias returns the currently active model alias.
func (s *Session) CurrentModelAlias() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.Config.Models.Default
}

// WorkflowHandoffModelSelection returns the configured handoff model for the
// destination when present, otherwise the current session model alias.
func (s *Session) WorkflowHandoffModelSelection(destination string) WorkflowHandoffModelSelection {
	s.mu.RLock()
	defer s.mu.RUnlock()

	current := strings.TrimSpace(s.deps.Config.Models.Default)
	selection := WorkflowHandoffModelSelection{
		ModelAlias:  current,
		SourceLabel: "current session",
	}

	destination = strings.TrimSpace(destination)
	if destination == "" {
		return selection
	}

	alias := strings.TrimSpace(s.deps.Config.Models.WorkflowHandoff[destination])
	if alias == "" {
		return selection
	}
	if _, ok := s.deps.Config.Models.Definitions[alias]; !ok {
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
	return s.deps.Config.Models.Definitions[s.deps.Config.Models.Default]
}

// CurrentReasoningOverride returns the session's runtime reasoning override
// for the currently active model alias, or the zero value (no override) if
// none has been set for that alias.
func (s *Session) CurrentReasoningOverride() provider.ReasoningOverride {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reasoningOverrides[s.deps.Config.Models.Default]
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

// Mode returns the current execution mode.
func (s *Session) Mode() config.ExecutionMode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

// SetMode updates the execution mode. If m is the same as the current mode,
// this is a no-op. Otherwise, it stores m, emits a mode-changed event, and
// notifies the mode listener if one is set.
func (s *Session) SetMode(m config.ExecutionMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m == s.mode {
		return
	}
	s.mode = m
	s.events.Emit(output.NewModeChangedEvent(string(m)))
	if s.modeListener != nil {
		s.modeListener(m)
	}
}

// SetModeListener registers a callback invoked after a mode change, outside of
// the no-op path. It replaces any previously registered listener. Safe to call
// with a nil listener.
func (s *Session) SetModeListener(listener func(config.ExecutionMode)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modeListener = listener
}

// WaitRuns blocks until all run goroutines launched by this session have exited,
// or the context is done. It reports whether the runs finished before ctx was done.
func (s *Session) WaitRuns(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		s.runs.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// modeNotice returns the mode notice string for injection into user messages.
// The notice is sticky: it is returned on every call in both plan and build
// mode, so run_flow prepends it to every outgoing user message. Returns empty
// string for any mode ModeNotice does not cover.
func (s *Session) modeNotice() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if notice := prompt.ModeNotice(s.mode); notice != "" {
		return notice + "\n\n"
	}
	return ""
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
