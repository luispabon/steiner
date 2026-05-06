package interactive

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
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
		runController:       &ActiveRunController{},
		skills:              NewSkills(deps.SkillNames),
		snapshots:           snaps,
		approvalCoordinator: &ApprovalCoordinator{},
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

// Approver returns an tool.ApprovalResponder that routes tool approval
// requests through the session's ApprovalCoordinator and emits approval
// events to the given sink.
func (s *Session) Approver(eventSink output.EventSink) tool.ApprovalResponder {
	return agent.NewEventingApprover(eventSink, newApprovalResponder(s.approvalCoordinator))
}

// CurrentModelConfig returns the currently active model config.
func (s *Session) CurrentModelConfig() config.ModelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deps.Config.Model
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

// Handle processes an interactive action. Handles SubmitPrompt,
// InterruptActiveRun, ClearConversation, RequestContextReport,
// RequestConfigReport, TriggerManualCompaction, RequestExit, SetSkillEnabled,
// SwitchModel, SubmitApproval, LoadSession, and RequestSessionPicker.
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
		s.exitOnce.Do(func() { close(s.done) })
		return nil
	case SetSkillEnabled:
		s.skills.Set(a.Name, a.Enabled)
		return nil
	case SubmitApproval:
		s.approvalCoordinator.Submit(a)
		return nil
	case SwitchModel:
		_, err := config.SwitchModelConfigByAlias(&s.deps.Config, a.Name)
		if err != nil {
			s.events.Emit(output.NewContextReportEvent(fmt.Sprintf("Model switch failed: %v", err)))
			return err
		}
		return nil
	case LoadSession:
		return s.loadSession(ctx, a.SessionID)
	case RequestSessionPicker:
		return nil
	default:
		return fmt.Errorf("handle: unknown action type %T", action)
	}
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

	sess, err := session.NewSession(s.deps.Config.Model.Model, s.lineage)
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

	for _, msg := range msgs {
		if msg.Content == "" {
			continue
		}
		switch msg.Role {
		case agent.MessageRoleUser:
			s.events.Emit(output.NewUserInputEvent(msg.Content, "resume"))
		case agent.MessageRoleAssistant:
			s.events.Emit(output.NewAssistantMessageEvent(0, string(msg.Role), msg.Content))
		}
	}
	return nil
}
