package interactive

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
)

// compile-time interface checks.
var (
	_ Action = SubmitPrompt{}
	_ Action = RequestContextReport{}
	_ Action = RequestConfigReport{}
	_ Action = SubmitApproval{}
	_ Action = SubmitWorkflowHandoff{}
	_ Action = InterruptActiveRun{}
	_ Action = RequestExit{}
	_ Action = SetSkillEnabled{}
	_ Action = SwitchModel{}
	_ Action = ClearConversation{}
	_ Action = TriggerManualCompaction{}
	_ Action = ToggleCavemanMode{}
	_ Action = LoadSession{}
	_ Action = RotateSession{}
	_ Action = requestSessionPicker{}
)

// testNewSession is a helper that creates a new session and fails the test if it returns an error.
func testNewSession(t *testing.T, deps Dependencies) *Session {
	s, err := NewSession(deps)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	return s
}

func TestNewSession(t *testing.T) {
	t.Parallel()
	s, err := NewSession(Dependencies{})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if s == nil {
		t.Fatal("NewSession returned nil session")
	}
	if s.events == nil {
		t.Error("expected non-nil event sink")
	}
	if s.displaySink == nil {
		t.Error("expected non-nil display sink")
	}
	if s.runController == nil {
		t.Error("expected non-nil run controller")
	}
	if s.skills == nil {
		t.Error("expected non-nil skills")
	}
	if s.snapshots == nil {
		t.Error("expected non-nil snapshot store")
	}
	if s.approvalCoordinator == nil {
		t.Error("expected non-nil approval coordinator")
	}
	if s.handoffCoordinator == nil {
		t.Error("expected non-nil workflow handoff coordinator")
	}
	if s.sessionID == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestNewSessionWithSkillNames(t *testing.T) {
	t.Parallel()
	s, err := NewSession(Dependencies{
		SkillNames: []string{"go-code-audit", "slop-detector"},
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if s == nil {
		t.Fatal("NewSession returned nil session")
	}
	names := s.Skills().Snapshot()
	if got := len(names); got != 0 {
		t.Fatalf("initial enabled skill count = %d, want 0", got)
	}
}

func TestActiveRunControllerInterrupt(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	ctrl := &ActiveRunController{}
	ctrl.Set(cancel)

	ctrl.Interrupt()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected interrupt to cancel the context")
	}
}

func TestActiveRunControllerClear(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	ctrl := &ActiveRunController{}
	ctrl.Set(cancel)
	ctrl.Clear()

	if ctrl.HasCancel() {
		t.Fatal("expected HasCancel to be false after Clear")
	}

	ctrl.Interrupt()
	select {
	case <-ctx.Done():
		t.Fatal("expected Interrupt to be a no-op after Clear")
	default:
	}
}

func TestSkillsSnapshot(t *testing.T) {
	t.Parallel()
	skills := NewSkills([]string{"a", "b", "c"})
	if got := len(skills.Snapshot()); got != 0 {
		t.Fatalf("initial snapshot length = %d, want 0", got)
	}
	skills.Set("a", true)
	skills.Set("c", true)
	skills.Set("b", false)
	skills.Set("d", true)
	got := skills.Snapshot()
	want := []string{"a", "c"}
	if len(got) != len(want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("snapshot = %v, want %v", got, want)
		}
	}
}

func TestSkillsReset(t *testing.T) {
	t.Parallel()

	t.Run("nil safe", func(_ *testing.T) {
		var skills *Skills
		skills.Reset()
	})

	t.Run("clears enabled skills", func(t *testing.T) {
		skills := NewSkills([]string{"a", "b", "c"})
		skills.Set("a", true)
		skills.Set("c", true)
		skills.Set("unknown", true)

		skills.Reset()

		if got := skills.Snapshot(); len(got) != 0 {
			t.Fatalf("snapshot after reset = %v, want empty", got)
		}
		if got := skills.Active(); got != "" {
			t.Fatalf("active after reset = %q, want empty", got)
		}
	})
}

func TestSkillsActive(t *testing.T) {
	t.Parallel()

	t.Run("nil safe", func(t *testing.T) {
		var skills *Skills
		if got := skills.Active(); got != "" {
			t.Fatalf("active on nil = %q, want empty", got)
		}
	})

	t.Run("returns first enabled skill in order", func(t *testing.T) {
		skills := NewSkills([]string{"a", "b", "c"})
		skills.Set("c", true)
		skills.Set("a", true)

		if got, want := skills.Active(), "a"; got != want {
			t.Fatalf("active = %q, want %q", got, want)
		}
	})

	t.Run("returns empty when none enabled", func(t *testing.T) {
		skills := NewSkills([]string{"a", "b"})

		if got := skills.Active(); got != "" {
			t.Fatalf("active = %q, want empty", got)
		}
	})
}

func TestSkillsSetNilSafe(t *testing.T) {
	t.Parallel()
	var s *Skills
	s.Set("anything", true)
}

func TestSnapshotStoreStoreAndSnapshot(t *testing.T) {
	t.Parallel()
	store := &SnapshotStore{}
	_, ok := store.Snapshot()
	if ok {
		t.Fatal("expected ok=false for empty store")
	}

	store.Store(RequestContextSnapshot{
		Model: "test-model",
	})
	snapshot, ok := store.Snapshot()
	if !ok {
		t.Fatal("expected ok=true after Store")
	}
	if snapshot.Model != "test-model" {
		t.Fatalf("model = %q, want %q", snapshot.Model, "test-model")
	}
}

func TestSnapshotStoreDeepClonesProviderOwnedFields(t *testing.T) {
	t.Parallel()

	maxTokens := 32
	store := &SnapshotStore{}
	snapshot := RequestContextSnapshot{
		Model: "test-model",
		Messages: []provider.Message{
			{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{
					{
						ID:        "call-1",
						Name:      "read",
						Arguments: map[string]any{"path": "a.go"},
					},
				},
				ProviderMetadata: &provider.MessageProviderMetadata{
					Anthropic: &provider.AnthropicMessageMetadata{ThinkingSignature: "sig"},
				},
			},
		},
		Tools: []provider.ToolSpec{
			{
				Type: "function",
				Function: provider.ToolFunctionSpec{
					Name:       "read",
					Parameters: map[string]any{"type": "object"},
				},
			},
		},
		MaxTokens: &maxTokens,
		Blocks: []prompt.ContextBlock{
			{Source: prompt.ContextSourceProjectContext, Path: "README.md", Content: "content"},
		},
	}

	store.Store(snapshot)
	got, ok := store.Snapshot()
	if !ok {
		t.Fatal("Snapshot() ok = false, want true")
	}

	got.Messages[0].ToolCalls[0].Arguments["path"] = "b.go"
	got.Messages[0].ProviderMetadata.Anthropic.ThinkingSignature = "changed"
	got.Tools[0].Function.Parameters["type"] = "array"

	if original := snapshot.Messages[0].ToolCalls[0].Arguments["path"]; original != "a.go" {
		t.Fatalf("original path = %v, want a.go", original)
	}
	if original := snapshot.Messages[0].ProviderMetadata.Anthropic.ThinkingSignature; original != "sig" {
		t.Fatalf("original signature = %q, want sig", original)
	}
	if original := snapshot.Tools[0].Function.Parameters["type"]; original != "object" {
		t.Fatalf("original tool parameter = %v, want object", original)
	}
}

func TestApprovalCoordinatorLifecycle(t *testing.T) {
	t.Parallel()
	coord := &ApprovalCoordinator{}
	if coord.HasPending() {
		t.Fatal("expected no pending initially")
	}

	ch := coord.Begin("write", "auto")
	if !coord.HasPending() {
		t.Fatal("expected pending after Begin")
	}

	coord.Submit(SubmitApproval{
		Tool:     "write",
		Mode:     "auto",
		Decision: "allow_once",
	})

	select {
	case sub := <-ch:
		if sub.Decision != "allow_once" {
			t.Fatalf("decision = %q, want %q", sub.Decision, "allow_once")
		}
	default:
		t.Fatal("expected submission on channel")
	}

	coord.Finish(ch)
	if coord.HasPending() {
		t.Fatal("expected no pending after Finish")
	}
}

func TestApprovalCoordinatorMismatch(t *testing.T) {
	t.Parallel()
	coord := &ApprovalCoordinator{}
	ch := coord.Begin("write", "auto")

	coord.Submit(SubmitApproval{
		Tool: "not-write",
		Mode: "auto",
	})
	select {
	case <-ch:
		t.Fatal("expected mismatch to block submission")
	default:
	}

	coord.Submit(SubmitApproval{
		Tool:     "write",
		Mode:     "auto",
		Decision: "deny",
	})
	select {
	case sub := <-ch:
		if sub.Decision != "deny" {
			t.Fatalf("decision = %q, want %q", sub.Decision, "deny")
		}
	default:
		t.Fatal("expected submission on channel")
	}
}

func TestWorkflowHandoffCoordinatorLifecycle(t *testing.T) {
	t.Parallel()
	coord := &WorkflowHandoffCoordinator{}
	if coord.HasPending() {
		t.Fatal("expected no pending initially")
	}

	ch := coord.Begin(tool.WorkflowHandoffRequest{
		Next:   "implement",
		Target: ".steiner/plans/step-2",
	})
	if !coord.HasPending() {
		t.Fatal("expected pending after Begin")
	}

	coord.Submit(SubmitWorkflowHandoff{Decision: "accept"})

	select {
	case sub := <-ch:
		if sub.Decision != "accept" {
			t.Fatalf("decision = %q, want accept", sub.Decision)
		}
	default:
		t.Fatal("expected submission on channel")
	}

	coord.Finish(ch)
	if coord.HasPending() {
		t.Fatal("expected no pending after Finish")
	}
}

func TestSessionHandleNoop(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			DefaultModel: "gpt-4",
			Models: map[string]config.ModelConfig{
				"gpt-4": {ID: "gpt-4"},
			},
		},
	})
	ctx := context.Background()

	tests := []struct {
		name   string
		action Action
	}{
		{"RequestContextReport", RequestContextReport{}},
		{"RequestConfigReport", RequestConfigReport{}},
		{"SubmitApproval", SubmitApproval{Tool: "write", Mode: "auto", Decision: "allow"}},
		{"SubmitWorkflowHandoff", SubmitWorkflowHandoff{Decision: "dismiss"}},
		{"RequestExit", RequestExit{}},
		{"SetSkillEnabled", SetSkillEnabled{Name: "go-code-audit", Enabled: true}},
		{"RotateSession", RotateSession{}},
		{"ClearConversation", ClearConversation{}},
		{"ToggleCavemanMode", ToggleCavemanMode{}},
		{"SwitchModel", SwitchModel{Name: "gpt-4"}},
		{"TriggerManualCompaction", TriggerManualCompaction{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Handle(ctx, tt.action); err != nil {
				t.Errorf("Handle(%T) = %v, want nil", tt.action, err)
			}
		})
	}
}

func TestRotateSession(t *testing.T) {
	t.Parallel()

	t.Run("generates new ID and clears title", func(t *testing.T) {
		s := testNewSession(t, Dependencies{
			SessionStore: newMockSessionStore(),
			Config: config.Config{
				DefaultModel: "test",
				Models:       map[string]config.ModelConfig{"test": {ID: "test-model"}},
			},
		})
		oldID := s.SessionID()
		oldTitle := s.SessionTitle() // may be empty initially

		if err := s.Handle(context.Background(), RotateSession{}); err != nil {
			t.Fatalf("RotateSession: %v", err)
		}
		if s.SessionID() == oldID {
			t.Fatal("expected new session ID after rotation")
		}
		if s.SessionTitle() != "" {
			t.Fatalf("session title = %q, want empty", s.SessionTitle())
		}
		_ = oldTitle // used
	})

	t.Run("noop when SessionStore is nil", func(t *testing.T) {
		s := testNewSession(t, Dependencies{})
		oldID := s.SessionID()
		if err := s.Handle(context.Background(), RotateSession{}); err != nil {
			t.Fatalf("RotateSession with nil store: %v", err)
		}
		if s.SessionID() != oldID {
			t.Fatalf("session ID changed from %q to %q with nil store", oldID, s.SessionID())
		}
	})
}
func TestSubmitPromptAppendsUserMessage(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Runner: runExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
			return RunResult{Conversation: conversation}, nil
		}),
	})

	s.submitPrompt(context.Background(), "hello", nil)

	s.mu.Lock()
	conv := s.conversation
	s.mu.Unlock()

	if len(conv) != 1 {
		t.Fatalf("conversation length = %d, want 1", len(conv))
	}
	if conv[0].Role != agent.MessageRoleUser || conv[0].Content != "hello" {
		t.Fatalf("message = %+v, want user/hello", conv[0])
	}
}

func TestSubmitPromptDelegatesToRunner(t *testing.T) {
	t.Parallel()
	var called bool
	s := testNewSession(t, Dependencies{
		Runner: runExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
			called = true
			return RunResult{Conversation: conversation}, nil
		}),
	})

	s.submitPrompt(context.Background(), "hello", nil)

	if !called {
		t.Fatal("expected Runner.Run to be called")
	}
}

func TestSubmitPromptUpdatesConversationOnSuccess(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Runner: runExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
			return RunResult{Conversation: []agent.Message{
				{Role: agent.MessageRoleUser, Content: "hello"},
				{Role: agent.MessageRoleAssistant, Content: "hi there"},
			}}, nil
		}),
	})

	s.submitPrompt(context.Background(), "hello", nil)

	if got := s.Conversation(); len(got) != 2 {
		t.Fatalf("conversation length = %d, want 2", len(got))
	}
}

func TestSubmitPromptSkipsConversationUpdateOnWorkflowHandoff(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Runner: runExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
			return RunResult{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "hello"},
					{Role: agent.MessageRoleAssistant, Content: "tool call"},
				},
				WorkflowHandoff: &tool.WorkflowHandoffTransition{
					Next:   "implement",
					Target: ".steiner/plans/step-2",
				},
			}, nil
		}),
	})

	s.submitPrompt(context.Background(), "hello", nil)

	// s.conversation must NOT be updated from result on workflow_handoff.
	// The TUI sends ClearConversation before this goroutine finishes; skipping
	// the update here ensures that clear is not overwritten, so the next skill
	// run starts with a clean context.
	got := s.Conversation()
	if len(got) != 1 {
		t.Fatalf("conversation length = %d, want 1 (user message only, not overwritten by result)", len(got))
	}
	if got[0].Role != agent.MessageRoleUser || got[0].Content != "hello" {
		t.Fatalf("conversation[0] = %#v, want user 'hello'", got[0])
	}

	// Lineage IS updated from the full result so the session can be saved.
	s.mu.RLock()
	lineage := s.lineage
	s.mu.RUnlock()
	if len(lineage.Generations) != 1 {
		t.Fatalf("lineage generations = %d, want 1", len(lineage.Generations))
	}
	if lineage.NextGenerationID != 2 {
		t.Fatalf("lineage next generation id = %d, want 2", lineage.NextGenerationID)
	}
	if len(lineage.Generations[0].Messages) != 2 {
		t.Fatalf("generation messages = %d, want 2", len(lineage.Generations[0].Messages))
	}
}

func TestSubmitPromptSavesSessionOnWorkflowHandoff(t *testing.T) {
	t.Parallel()
	mockStore := newMockSessionStore()
	s := testNewSession(t, Dependencies{
		Runner: runExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
			return RunResult{
				Conversation: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "hello"},
					{Role: agent.MessageRoleAssistant, Content: "tool call"},
				},
				WorkflowHandoff: &tool.WorkflowHandoffTransition{
					Next:   "implement",
					Target: ".steiner/plans/step-2",
				},
			}, nil
		}),
		SessionStore: mockStore,
		Config: config.Config{
			DefaultModel: "test",
			Models: map[string]config.ModelConfig{
				"test": {ID: "test-model"},
			},
		},
	})

	s.submitPrompt(context.Background(), "hello", nil)

	saved, ok := mockStore.savedSessions[s.SessionID()]
	if !ok {
		t.Fatal("expected session to be saved on workflow handoff")
	}
	if saved.ID != s.SessionID() {
		t.Fatalf("saved session ID = %q, want %q", saved.ID, s.SessionID())
	}
	if saved.Title == "" {
		t.Fatal("expected non-empty session title after first prompt")
	}
	if len(saved.Lineage.Generations) != 1 {
		t.Fatalf("saved session lineage generations = %d, want 1", len(saved.Lineage.Generations))
	}
	msgs := saved.Lineage.Generations[0].Messages
	if len(msgs) != 2 {
		t.Fatalf("saved session generation messages = %d, want 2", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Fatalf("saved msg[0].Content = %q, want %q", msgs[0].Content, "hello")
	}
	if msgs[1].Content != "tool call" {
		t.Fatalf("saved msg[1].Content = %q, want %q", msgs[1].Content, "tool call")
	}
}

func TestSubmitPromptEmitsStopReasonOnError(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		Runner: runExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
			return RunResult{}, fmt.Errorf("run failed")
		}),
	})

	s.submitPrompt(context.Background(), "hello", nil)

	var found bool
	for _, event := range events {
		if event.Type == output.EventTypeStopReason {
			found = true
			if payload, ok := event.Payload.(output.StopReasonEvent); ok {
				if payload.Reason != "Error: run failed" {
					t.Fatalf("stop reason = %q, want %q", payload.Reason, "Error: run failed")
				}
			}
			break
		}
	}
	if !found {
		t.Fatalf("events = %#v, want StopReason event", events)
	}
}

func TestSubmitPromptEmitsHistoryOnSuccess(t *testing.T) {
	t.Parallel()
	var events []output.Event
	recorded := ""
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		Runner: runExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
			return RunResult{Conversation: append(conversation, agent.Message{Role: agent.MessageRoleAssistant, Content: "ok"})}, nil
		}),
		HistoryWriter: &recordingHistoryWriter{
			recordFn: func(prompt string) error {
				recorded = prompt
				return nil
			},
			loadFn: func() ([]string, error) {
				return []string{"prev", recorded}, nil
			},
		},
	})

	s.submitPrompt(context.Background(), "hello", nil)

	if recorded != "hello" {
		t.Fatalf("recorded prompt = %q, want %q", recorded, "hello")
	}

	var foundHistory bool
	for _, event := range events {
		if event.Type == output.EventTypeHistoryLoaded {
			foundHistory = true
			if payload, ok := event.Payload.(output.HistoryLoadedEvent); ok {
				if len(payload.Prompts) != 2 || payload.Prompts[0] != "prev" || payload.Prompts[1] != "hello" {
					t.Fatalf("history prompts = %v, want [prev hello]", payload.Prompts)
				}
			}
			break
		}
	}
	if !foundHistory {
		t.Fatalf("events = %#v, want HistoryLoaded event", events)
	}
}

func TestSubmitPromptRunWithInterruptOwnershipCancelsActiveRun(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	cancelled := false
	s := testNewSession(t, Dependencies{
		Runner: runExecutorFunc(func(ctx context.Context, _ []agent.Message, _ []string) (RunResult, error) {
			close(block)
			<-ctx.Done()
			cancelled = true
			return RunResult{}, ctx.Err()
		}),
	})

	go func() {
		<-block
		if err := s.Handle(context.Background(), InterruptActiveRun{}); err != nil {
			t.Errorf("Handle(InterruptActiveRun) error = %v", err)
		}
	}()

	s.submitPrompt(context.Background(), "hello", nil)

	if !cancelled {
		t.Fatal("expected active run to be cancelled on interrupt")
	}
}

func TestInterruptActiveRunCancelsRun(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	s := testNewSession(t, Dependencies{})
	s.runController.Set(cancel)

	if err := s.Handle(context.Background(), InterruptActiveRun{}); err != nil {
		t.Fatalf("Handle(InterruptActiveRun) = %v, want nil", err)
	}

	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected InterruptActiveRun to cancel the active run")
	}
}

func TestClearConversationResetsSkills(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	s.SetConversation([]agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}})
	s.Skills().Set("skill-a", true)

	if err := s.Handle(context.Background(), ClearConversation{}); err != nil {
		t.Fatalf("Handle(ClearConversation) = %v, want nil", err)
	}
	if got := s.Conversation(); got != nil {
		t.Fatalf("conversation after ClearConversation = %v, want nil", got)
	}
	if got := s.Skills().Snapshot(); len(got) != 0 {
		t.Fatalf("enabled skills after ClearConversation = %v, want empty", got)
	}
}

// runExecutorFunc adapts a function to the runExecutor interface.
type runExecutorFunc func(context.Context, []agent.Message, []string) (RunResult, error)

func (f runExecutorFunc) Run(ctx context.Context, conversation []agent.Message, skillNames []string, _ <-chan string) (RunResult, error) {
	return f(ctx, conversation, skillNames)
}

// recordingHistoryWriter implements historyWriter for testing.
type recordingHistoryWriter struct {
	recordFn func(string) error
	loadFn   func() ([]string, error)
}

func (w *recordingHistoryWriter) Record(prompt string) error {
	if w.recordFn == nil {
		return nil
	}
	return w.recordFn(prompt)
}

func (w *recordingHistoryWriter) Load() ([]string, error) {
	if w.loadFn == nil {
		return nil, nil
	}
	return w.loadFn()
}

func TestSessionRunReturnsOnCancel(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Run(ctx); err != nil {
		t.Errorf("Run() = %v, want nil", err)
	}
}

func TestSessionRunBlocksUntilRequestExit(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx)
	}()

	if err := s.Handle(ctx, RequestExit{}); err != nil {
		t.Fatalf("Handle(RequestExit) error = %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after RequestExit")
	}
}

func TestSessionRunLoadsHistory(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		HistoryWriter: &recordingHistoryWriter{
			loadFn: func() ([]string, error) {
				return []string{"prompt-1", "prompt-2"}, nil
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}

	var found bool
	for _, event := range events {
		if event.Type == output.EventTypeHistoryLoaded {
			found = true
			if payload, ok := event.Payload.(output.HistoryLoadedEvent); ok {
				if len(payload.Prompts) != 2 || payload.Prompts[0] != "prompt-1" || payload.Prompts[1] != "prompt-2" {
					t.Fatalf("history prompts = %v, want [prompt-1 prompt-2]", payload.Prompts)
				}
			}
			break
		}
	}
	if !found {
		t.Fatalf("events = %#v, want HistoryLoaded event", events)
	}
}

func TestSessionRunEmitsWarningOnHistoryLoadError(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		HistoryWriter: &recordingHistoryWriter{
			loadFn: func() ([]string, error) {
				return nil, fmt.Errorf("load error")
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}

	var found bool
	for _, event := range events {
		payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
		if !ok {
			continue
		}
		if payload.Kind == "session_health" && payload.Severity == "warning" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("events = %#v, want session_health warning diagnostic", events)
	}
}

func TestSessionRunLoadsEmptyHistory(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		HistoryWriter: &recordingHistoryWriter{
			loadFn: func() ([]string, error) {
				return nil, nil
			},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}

	var found bool
	for _, event := range events {
		if event.Type == output.EventTypeHistoryLoaded {
			found = true
			if payload, ok := event.Payload.(output.HistoryLoadedEvent); ok {
				if len(payload.Prompts) != 0 {
					t.Fatalf("history prompts = %v, want empty", payload.Prompts)
				}
			}
			break
		}
	}
	if !found {
		t.Fatalf("events = %#v, want HistoryLoaded event", events)
	}
}

func TestSessionConversationAccessors(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	if got := s.Conversation(); got != nil {
		t.Fatalf("initial conversation = %v, want nil", got)
	}

	msgs := []agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}}
	s.SetConversation(msgs)
	if got := len(s.Conversation()); got != 1 {
		t.Fatalf("conversation length = %d, want 1", got)
	}
}

func TestSwitchModelSuccess(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		Config: config.Config{
			DefaultModel: "current",
			Providers: map[string]config.ProviderConfig{
				"old": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://old.example/v1"},
				"new": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://new.example/v1"},
			},
			Models: map[string]config.ModelConfig{
				"current": {Provider: "old", ID: "old-model"},
				"fast":    {Provider: "new", ID: "new-model"},
			},
		},
	})

	err := s.Handle(context.Background(), SwitchModel{Name: "fast"})
	if err != nil {
		t.Fatalf("Handle(SwitchModel) = %v, want nil", err)
	}

	if got, want := s.deps.Config.DefaultModel, "fast"; got != want {
		t.Fatalf("config default_model = %q, want %q", got, want)
	}

	for _, event := range events {
		if payload, ok := event.Payload.(output.ContextReportEvent); ok {
			if strings.Contains(payload.Content, "failed") {
				t.Fatalf("unexpected error event: %q", payload.Content)
			}
		}
	}
}

func TestSwitchModelFailure(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		Config: config.Config{
			DefaultModel: "current",
			Models:       map[string]config.ModelConfig{"current": {ID: "current-id"}},
		},
	})

	err := s.Handle(context.Background(), SwitchModel{Name: "unknown"})
	if err == nil {
		t.Fatal("Handle(SwitchModel) = nil, want error")
	}

	var found bool
	for _, event := range events {
		if payload, ok := event.Payload.(output.ContextReportEvent); ok {
			if strings.Contains(payload.Content, "failed") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("events = %#v, want ContextReportEvent with error", events)
	}

	if got, want := s.deps.Config.DefaultModel, "current"; got != want {
		t.Fatalf("config default_model after failed switch = %q, want %q", got, want)
	}
}

func TestCurrentModelConfig(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			DefaultModel: "mymodel",
			Providers:    map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://example/v1"}},
			Models:       map[string]config.ModelConfig{"mymodel": {Provider: "local", ID: "test-model"}},
		},
	})
	got := s.CurrentModelConfig()
	if got.ID != "test-model" {
		t.Fatalf("CurrentModelConfig().ID = %q, want %q", got.ID, "test-model")
	}
}

func TestCurrentModelAliasTracksSwitchModel(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			DefaultModel: "current",
			Models: map[string]config.ModelConfig{
				"current": {Provider: "local", ID: "current-id"},
				"fast":    {Provider: "local", ID: "fast-id"},
			},
		},
	})

	if got, want := s.CurrentModelAlias(), "current"; got != want {
		t.Fatalf("CurrentModelAlias() before switch = %q, want %q", got, want)
	}
	if err := s.Handle(context.Background(), SwitchModel{Name: "fast"}); err != nil {
		t.Fatalf("Handle(SwitchModel) = %v, want nil", err)
	}
	if got, want := s.CurrentModelAlias(), "fast"; got != want {
		t.Fatalf("CurrentModelAlias() after switch = %q, want %q", got, want)
	}
}

func TestHandleToggleCavemanMode(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			CavemanMode: false,
		},
	})

	if got := s.CavemanMode(); got != false {
		t.Fatalf("CavemanMode() before toggle = %v, want false", got)
	}

	if err := s.Handle(context.Background(), ToggleCavemanMode{}); err != nil {
		t.Fatalf("Handle(ToggleCavemanMode) = %v, want nil", err)
	}
	if got := s.CavemanMode(); got != true {
		t.Fatalf("CavemanMode() after first toggle = %v, want true", got)
	}

	if err := s.Handle(context.Background(), ToggleCavemanMode{}); err != nil {
		t.Fatalf("Handle(ToggleCavemanMode) = %v, want nil", err)
	}
	if got := s.CavemanMode(); got != false {
		t.Fatalf("CavemanMode() after second toggle = %v, want false", got)
	}
}

func TestHandleSetSkillEnabledDisablesSkill(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		SkillNames: []string{"review", "test"},
	})

	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: true}); err != nil {
		t.Fatalf("Handle(SetSkillEnabled enable review) = %v, want nil", err)
	}
	if err := s.Handle(context.Background(), SetSkillEnabled{Name: "test", Enabled: true}); err != nil {
		t.Fatalf("Handle(SetSkillEnabled enable test) = %v, want nil", err)
	}

	snap := s.Skills().Snapshot()
	if len(snap) != 2 {
		t.Fatalf("initial skills = %v, want 2", snap)
	}

	err := s.Handle(context.Background(), SetSkillEnabled{Name: "review", Enabled: false})
	if err != nil {
		t.Fatalf("Handle(SetSkillEnabled) = %v, want nil", err)
	}

	snap = s.Skills().Snapshot()
	if len(snap) != 1 || snap[0] != "test" {
		t.Fatalf("skills after disable = %v, want [test]", snap)
	}
}

func TestSubmitPromptDoesNotPassSkillsUntilEnabled(t *testing.T) {
	t.Parallel()
	var gotSkillNames []string
	s := testNewSession(t, Dependencies{
		SkillNames: []string{"review"},
		Runner: runExecutorFunc(func(_ context.Context, conversation []agent.Message, skillNames []string) (RunResult, error) {
			gotSkillNames = append([]string(nil), skillNames...)
			return RunResult{Conversation: conversation}, nil
		}),
	})

	s.submitPrompt(context.Background(), "hey", nil)

	if len(gotSkillNames) != 0 {
		t.Fatalf("runner skill names = %v, want none", gotSkillNames)
	}
}

func TestSessionAccessorsNonNil(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	if s.EventSink() == nil {
		t.Error("EventSink() returned nil")
	}
	if s.DisplaySink() == nil {
		t.Error("DisplaySink() returned nil")
	}
	if s.ActiveRunController() == nil {
		t.Error("ActiveRunController() returned nil")
	}
	if s.Skills() == nil {
		t.Error("Skills() returned nil")
	}
	if s.SnapshotStore() == nil {
		t.Error("SnapshotStore() returned nil")
	}
	if s.ApprovalCoordinator() == nil {
		t.Error("ApprovalCoordinator() returned nil")
	}
	if s.WorkflowHandoffCoordinator() == nil {
		t.Error("WorkflowHandoffCoordinator() returned nil")
	}
}

func TestSessionIDNonEmpty(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	id := s.SessionID()
	if id == "" {
		t.Fatal("SessionID() returned empty string")
	}
	if len(id) != 32 {
		t.Fatalf("SessionID() length = %d, want 32", len(id))
	}
}

func TestSessionTitleEmptyInitially(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})
	title := s.SessionTitle()
	if title != "" {
		t.Fatalf("SessionTitle() = %q, want empty string", title)
	}
}

func TestSaveSessionPersistsCurrentMetadata(t *testing.T) {
	t.Parallel()

	mockStore := newMockSessionStore()
	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		Config: config.Config{
			DefaultModel: "test",
			Models: map[string]config.ModelConfig{
				"test": {ID: "test-model"},
			},
		},
	})

	const sessionID = "persisted-session-id"
	const sessionTitle = "Persist me"
	lineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID:       1,
				Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}},
			},
		},
		NextGenerationID: 2,
	}

	s.mu.Lock()
	s.sessionID = sessionID
	s.sessionTitle = sessionTitle
	s.lineage = lineage
	s.mu.Unlock()

	if err := s.saveSession(); err != nil {
		t.Fatalf("saveSession() = %v, want nil", err)
	}

	saved, ok := mockStore.savedSessions[sessionID]
	if !ok {
		t.Fatalf("savedSessions = %#v, want session %q to be saved", mockStore.savedSessions, sessionID)
	}
	if got, want := saved.ID, sessionID; got != want {
		t.Fatalf("saved ID = %q, want %q", got, want)
	}
	if got, want := saved.Title, sessionTitle; got != want {
		t.Fatalf("saved title = %q, want %q", got, want)
	}
	if got, want := saved.Model, "test-model"; got != want {
		t.Fatalf("saved model = %q, want %q", got, want)
	}
	if got, want := saved.Lineage.FullMessages(), lineage.FullMessages(); len(got) != len(want) || got[0].Content != want[0].Content {
		t.Fatalf("saved lineage = %#v, want %#v", got, want)
	}
}

// mockSessionStore is a minimal mock sessionStore for testing.
type mockSessionStore struct {
	savedSessions  map[string]session.Session
	loadedSessions map[string]session.Session
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{
		savedSessions:  make(map[string]session.Session),
		loadedSessions: make(map[string]session.Session),
	}
}

func (m *mockSessionStore) Save(s session.Session) error {
	m.savedSessions[s.ID] = s
	return nil
}

func (m *mockSessionStore) Load(id string) (session.Session, error) {
	if s, ok := m.loadedSessions[id]; ok {
		return s, nil
	}
	return session.Session{}, fmt.Errorf("session not found: %s", id)
}

func (m *mockSessionStore) List() ([]session.IndexEntry, error) {
	return nil, nil
}

func TestLoadSessionReplacesConversation(t *testing.T) {
	t.Parallel()
	var events []output.Event
	mockStore := newMockSessionStore()

	// Create a mock session with some lineage
	mockSession := session.Session{
		ID:    "test-session-id",
		Title: "Test Session",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID:       1,
					Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "previous message"}},
				},
			},
			NextGenerationID: 2,
		},
	}
	mockStore.loadedSessions["test-session-id"] = mockSession

	s := testNewSession(t, Dependencies{
		BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		SessionStore: mockStore,
	})

	// Verify session starts with empty conversation
	if len(s.Conversation()) != 0 {
		t.Fatalf("initial conversation length = %d, want 0", len(s.Conversation()))
	}

	// Load the session
	err := s.Handle(context.Background(), LoadSession{SessionID: "test-session-id"})
	if err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	// Verify conversation was replaced
	conv := s.Conversation()
	if len(conv) != 1 {
		t.Fatalf("conversation length after load = %d, want 1", len(conv))
	}
	if conv[0].Content != "previous message" {
		t.Fatalf("first message = %q, want %q", conv[0].Content, "previous message")
	}

	// Verify session title was updated
	if got := s.SessionTitle(); got != "Test Session" {
		t.Fatalf("SessionTitle() = %q, want %q", got, "Test Session")
	}

	// Verify events were emitted for the loaded message
	var foundUserInputEvent bool
	for _, event := range events {
		if event.Type == output.EventTypeUserInput {
			foundUserInputEvent = true
			break
		}
	}
	if !foundUserInputEvent {
		t.Fatalf("events = %#v, want UserInput event", events)
	}
}

func TestLoadSessionPreservesAssistantToolCallMessagesForDisplay(t *testing.T) {
	t.Parallel()

	const sessionID = "tool-call-session"
	var events []output.Event
	mockStore := newMockSessionStore()
	mockStore.loadedSessions[sessionID] = session.Session{
		ID:    sessionID,
		Title: "Tool Call Session",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID: 1,
					Messages: []agent.Message{
						{Role: agent.MessageRoleUser, Content: "please inspect"},
						{
							Role: agent.MessageRoleAssistant,
							ToolCalls: []agent.ToolCall{
								{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "README.md"}},
							},
						},
						{Role: agent.MessageRoleTool, Content: "file contents", ToolCallID: "call_1", Name: "read"},
						{Role: agent.MessageRoleAssistant, Content: "done"},
					},
				},
			},
			NextGenerationID: 2,
		},
	}

	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		SessionStore: mockStore,
		Config: config.Config{
			DefaultModel: "test",
			Models: map[string]config.ModelConfig{
				"test": {ID: "test-model"},
			},
		},
	})

	if err := s.Handle(context.Background(), LoadSession{SessionID: sessionID}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	conv := s.Conversation()
	if got, want := len(conv), 4; got != want {
		t.Fatalf("conversation length = %d, want %d", got, want)
	}
	if conv[1].Role != agent.MessageRoleAssistant || len(conv[1].ToolCalls) != 1 {
		t.Fatalf("assistant tool-call message = %#v, want preserved tool call", conv[1])
	}
	if conv[2].Role != agent.MessageRoleTool || conv[2].ToolCallID != "call_1" {
		t.Fatalf("tool result message = %#v, want preserved tool result", conv[2])
	}

	var assistantEvents int
	var toolStarted []output.ToolCallStartedEvent
	var toolFinished []output.ToolCallFinishedEvent
	for _, event := range events {
		if event.Type == output.EventTypeAssistantMessage {
			assistantEvents++
		}
		if event.Type == output.EventTypeToolCallStarted {
			payload, ok := event.Payload.(output.ToolCallStartedEvent)
			if !ok {
				t.Fatalf("tool started payload type = %T, want output.ToolCallStartedEvent", event.Payload)
			}
			toolStarted = append(toolStarted, payload)
		}
		if event.Type == output.EventTypeToolCallFinished {
			payload, ok := event.Payload.(output.ToolCallFinishedEvent)
			if !ok {
				t.Fatalf("tool finished payload type = %T, want output.ToolCallFinishedEvent", event.Payload)
			}
			toolFinished = append(toolFinished, payload)
		}
	}
	if got, want := assistantEvents, 2; got != want {
		t.Fatalf("assistant message events = %d, want %d for tool-call transcript display", got, want)
	}
	if got, want := len(toolStarted), 1; got != want {
		t.Fatalf("tool started events = %d, want %d", got, want)
	}
	if got, want := toolStarted[0].Tool, "read"; got != want {
		t.Fatalf("tool started tool = %q, want %q", got, want)
	}
	if got, want := toolStarted[0].CallID, "call_1"; got != want {
		t.Fatalf("tool started call id = %q, want %q", got, want)
	}
	if got, want := toolStarted[0].Arguments["path"], "README.md"; got != want {
		t.Fatalf("tool started args path = %#v, want %q", got, want)
	}
	if got, want := len(toolFinished), 1; got != want {
		t.Fatalf("tool finished events = %d, want %d", got, want)
	}
	if got, want := toolFinished[0].Tool, "read"; got != want {
		t.Fatalf("tool finished tool = %q, want %q", got, want)
	}
	if got, want := toolFinished[0].CallID, "call_1"; got != want {
		t.Fatalf("tool finished call id = %q, want %q", got, want)
	}
	if got, want := toolFinished[0].Result, "file contents"; got != want {
		t.Fatalf("tool finished result = %q, want %q", got, want)
	}
}

func TestSubmitPromptWithImages(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{})

	// Verify SubmitPrompt struct can be constructed with Images field
	images := []agent.ImageBlock{
		{MediaType: "image/png", Data: "base64encodeddata"},
	}
	action := SubmitPrompt{
		Text:   "hello",
		Images: images,
	}

	// Verify the action implements Action interface
	var _ Action = action

	// Verify Images field is properly set
	if len(action.Images) != 1 {
		t.Fatalf("action.Images length = %d, want 1", len(action.Images))
	}
	if got, want := action.Images[0].MediaType, "image/png"; got != want {
		t.Fatalf("action.Images[0].MediaType = %q, want %q", got, want)
	}
	if got, want := action.Images[0].Data, "base64encodeddata"; got != want {
		t.Fatalf("action.Images[0].Data = %q, want %q", got, want)
	}

	// Verify nil Images is acceptable
	actionNoImages := SubmitPrompt{Text: "text only"}
	if actionNoImages.Images != nil {
		t.Fatalf("expected nil Images for text-only SubmitPrompt")
	}
	_ = s // Suppress unused warning
}
