package interactive

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
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

// generateSessionID creates a random hex ID using crypto/rand.
func generateSessionID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return fmt.Sprintf("%032x", b), nil
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

// CavemanMode returns whether caveman-style terse prompting is enabled.
func (s *Session) CavemanMode() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.Config.CavemanMode
}

// HumanizerMode returns whether humanizer-style prompting is enabled.
func (s *Session) HumanizerMode() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.Config.HumanizerMode
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

// LoadSessionByID loads a saved session with the given ID, replacing the current
// conversation with the restored lineage.
func (s *Session) LoadSessionByID(ctx context.Context, sessionID string) error {
	return s.loadSession(ctx, sessionID)
}

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
		s.runController.Steer(a.Text)
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
	case ToggleCavemanMode:
		s.handleToggleCavemanMode()
		return true, nil
	case ToggleHumanizerMode:
		s.handleToggleHumanizerMode()
		return true, nil
	case SwitchModel:
		return true, s.handleSwitchModel(a.Name)
	case LoadSession:
		return true, s.loadSession(ctx, a.SessionID)

	case RotateSession:
		s.mu.Lock()
		if s.deps.SessionStore != nil {
			id, err := generateSessionID()
			if err != nil {
				s.mu.Unlock()
				return true, fmt.Errorf("rotate session id: %w", err)
			}
			s.sessionID = id
			s.sessionTitle = ""
		}
		s.mu.Unlock()
		return true, nil
	case ForkSession:
		return true, s.handleForkSession(ctx)
	case ForkSavedSession:
		return true, s.handleForkSavedSession(ctx, a.SessionID)
	}
	return false, nil
}

func (s *Session) handleToggleHumanizerMode() {
	s.mu.Lock()
	s.deps.Config.HumanizerMode = !s.deps.Config.HumanizerMode
	state := "off"
	if s.deps.Config.HumanizerMode {
		state = "on"
	}
	s.mu.Unlock()
	s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("Humanizer mode: %s", state)))
}

func (s *Session) handleToggleCavemanMode() {
	s.mu.Lock()
	s.deps.Config.CavemanMode = !s.deps.Config.CavemanMode
	state := "off"
	if s.deps.Config.CavemanMode {
		state = "on"
	}
	s.mu.Unlock()
	s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("Caveman mode: %s", state)))
}

func (s *Session) handleSwitchModel(name string) error {
	s.mu.Lock()
	if _, ok := s.deps.Config.Models[name]; !ok {
		s.mu.Unlock()
		err := fmt.Errorf("model %q not found in config", name)
		s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("Model switch failed: %v", err)))
		return err
	}
	s.deps.Config.DefaultModel = name
	s.mu.Unlock()
	return nil
}

// handleForkSession forks the current live session after saving it, then switches to the fork.
func (s *Session) handleForkSession(ctx context.Context) error {
	if s.deps.SessionStore == nil {
		s.events.Emit(output.NewContextReportEvent("session store not configured"))
		return nil
	}

	if err := s.saveSession(); err != nil {
		s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("fork session: save failed: %v", err)))
		return err
	}

	s.mu.RLock()
	currentSession := session.Session{
		ID:      s.sessionID,
		Title:   s.sessionTitle,
		Model:   s.deps.Config.Models[s.deps.Config.DefaultModel].ID,
		Lineage: s.lineage,
	}
	originalTitle := s.sessionTitle
	s.mu.RUnlock()

	forked, err := session.Fork(currentSession)
	if err != nil {
		s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("fork session: %v", err)))
		return err
	}

	if err := s.deps.SessionStore.Save(forked); err != nil {
		s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("fork session: save fork failed: %v", err)))
		return err
	}

	s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("Forked from: %s", originalTitle)))
	return s.loadSession(ctx, forked.ID)
}

// handleForkSavedSession forks a saved session by ID, saves the fork, then switches to it.
func (s *Session) handleForkSavedSession(ctx context.Context, sessionID string) error {
	if s.deps.SessionStore == nil {
		s.events.Emit(output.NewContextReportEvent("session store not configured"))
		return nil
	}

	loadedSession, err := s.deps.SessionStore.Load(sessionID)
	if err != nil {
		s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("fork saved session failed: %v", err)))
		return err
	}

	forked, err := session.Fork(loadedSession)
	if err != nil {
		s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("fork saved session: %v", err)))
		return err
	}

	if err := s.deps.SessionStore.Save(forked); err != nil {
		s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("fork saved session: save failed: %v", err)))
		return err
	}

	s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("Forked from: %s", loadedSession.Title)))
	return s.loadSession(ctx, forked.ID)
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

// saveSession saves the current session state to disk with the current title and lineage.
func (s *Session) saveSession() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.deps.SessionStore == nil {
		return nil
	}

	sess, err := session.NewSession(s.deps.Config.Models[s.deps.Config.DefaultModel].ID, s.lineage)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	sess.ID = s.sessionID
	if s.sessionTitle != "" {
		sess = sess.WithTitle(s.sessionTitle)
	}

	return s.deps.SessionStore.Save(sess)
}

// loadSession replaces the current conversation and lineage with a previously
// saved session, following the ClearConversation pattern but seeding from stored lineage.
func (s *Session) loadSession(ctx context.Context, sessionID string) error {
	if s.deps.SessionStore == nil {
		s.events.Emit(output.NewContextReportEvent("session store not configured"))
		return nil
	}

	sess, err := s.deps.SessionStore.Load(sessionID)
	if err != nil {
		s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("load session failed: %v", err)))
		return err
	}

	s.mu.Lock()
	s.lineage = sess.Lineage
	s.conversation = sess.Lineage.FullMessages()
	s.sessionID = sess.ID
	s.sessionTitle = sess.Title
	msgs := append([]agent.Message(nil), s.conversation...)
	s.mu.Unlock()

	s.replaySessionMessages(msgs)

	// Emit a context-diagnostics event so the TUI can populate the sidebar
	// token bar and the status bar with the model's context budget.
	currentModel := s.deps.Config.Models[s.deps.Config.DefaultModel]
	var promptTokens int
	for _, msg := range msgs {
		t, err := provider.EstimateMessageTokens(ctx, currentModel.ID, provider.Message{
			Role:    provider.MessageRole(msg.Role),
			Content: msg.Content,
		})
		if err != nil {
			_ = err
			promptTokens += 4
		} else {
			promptTokens += t
		}
	}
	turnCount := promptTokens / 2
	if turnCount < 1 {
		turnCount = 1
	}
	s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:                "session_loaded",
		ContextWindow:       currentModel.Advanced.Limits.ContextWindow,
		ContextTokens:       currentModel.Advanced.Limits.ContextWindow,
		PromptTokens:        promptTokens,
		ContextUsagePercent: usagePercent(promptTokens, currentModel.Advanced.Limits.ContextWindow),
		Status:              sessionLoadedStatus(currentModel.Advanced.Limits.ContextWindow),
		TotalTokens:         promptTokens,
		Turn:                turnCount,
	}))

	return nil
}

// isDelegateToolCall returns true if the tool name is a known delegate tool.
func isDelegateToolCall(name string) bool {
	switch name {
	case "delegate", "explore", "research", "code", "plan", "verify":
		return true
	}
	return false
}

// taskFromArgs extracts the "task" string from a tool call arguments map.
func taskFromArgs(args map[string]any) string {
	if args == nil {
		return ""
	}
	if v, ok := args["task"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// replaySessionMessages replays conversation messages and emits display events
// so the TUI can reconstruct the session view on resume. Delegate tool calls
// emit delegation events; regular tool calls emit tool call events.
func (s *Session) replaySessionMessages(msgs []agent.Message) {
	startedToolCalls := map[string]struct{}{}
	pendingDelegates := map[string]agent.ToolCall{}
	for _, msg := range msgs {
		if msg.Content == "" && len(msg.ToolCalls) == 0 {
			continue
		}
		switch msg.Role {
		case agent.MessageRoleUser:
			s.events.Emit(output.NewUserInputEvent(msg.Content, "resume"))
		case agent.MessageRoleAssistant:
			s.events.Emit(output.NewAssistantMessageEvent(0, string(msg.Role), msg.Content))
			s.replayAssistantToolCalls(msg.ToolCalls, pendingDelegates, startedToolCalls)
		case agent.MessageRoleTool:
			s.replayToolResult(msg, pendingDelegates, startedToolCalls)
		}
	}
}

// replayAssistantToolCalls emits events for each tool call in an assistant message.
func (s *Session) replayAssistantToolCalls(calls []agent.ToolCall, pendingDelegates map[string]agent.ToolCall, startedToolCalls map[string]struct{}) {
	for _, call := range calls {
		if isDelegateToolCall(call.Name) {
			s.events.Emit(output.NewDelegationStartedEvent(call.ID, taskFromArgs(call.Arguments)))
			pendingDelegates[call.ID] = call
		} else {
			s.events.Emit(output.NewToolCallStartedEvent(0, call.Name, call.ID, call.Arguments))
			startedToolCalls[call.ID] = struct{}{}
		}
	}
}

// replayToolResult emits the completion event for a tool result message.
func (s *Session) replayToolResult(msg agent.Message, pendingDelegates map[string]agent.ToolCall, startedToolCalls map[string]struct{}) {
	if pending, ok := pendingDelegates[msg.ToolCallID]; ok {
		agentID := "agent-" + msg.ToolCallID
		status := "complete"
		turns, tokens := 0, 0
		if msg.Retention != nil {
			agentID = msg.Retention.AgentID
			status = msg.Retention.Status
			turns = msg.Retention.TurnCount
			tokens = msg.Retention.TokenCount
		}
		if status == "failed" {
			s.events.Emit(output.NewDelegationFailedEvent(agentID, taskFromArgs(pending.Arguments), msg.Content))
		} else {
			s.events.Emit(output.NewDelegationCompleteEvent(agentID, status, turns, tokens, 0, msg.Content))
		}
		delete(pendingDelegates, msg.ToolCallID)
	} else if _, ok := startedToolCalls[msg.ToolCallID]; ok {
		s.events.Emit(output.NewToolCallFinishedEvent(0, msg.Name, msg.ToolCallID, msg.Content, nil))
	}
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
