package interactive

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
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
	_ Action = SwitchMode{}
	_ Action = ClearConversation{}
	_ Action = TriggerManualCompaction{}
	_ Action = LoadSession{}
	_ Action = RotateSession{}
	_ Action = RotateSessionWithGroup{}
	_ Action = ForkSession{}
	_ Action = ForkSavedSession{}
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

	ch := coord.Begin("write", "write", "auto", string(tool.ApprovalKindPath), "")
	if !coord.HasPending() {
		t.Fatal("expected pending after Begin")
	}

	coord.Submit(SubmitApproval{
		Identity: "write",
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
	ch := coord.Begin("write", "write", "auto", string(tool.ApprovalKindPath), "")

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
		Identity: "write",
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
			Providers: map[string]config.ProviderConfig{"local": {}},
			Models: config.ModelsConfig{
				Default: "gpt-4",
				Definitions: map[string]config.ModelConfig{
					"gpt-4": {Provider: "local", ID: "gpt-4"},
				},
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

func TestSessionWaitRunsWaitsForSubmittedPrompt(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	s := testNewSession(t, Dependencies{
		Runner: newRunExecutorFunc(func(ctx context.Context, _ []agent.Message, _ []string) (RunResult, error) {
			close(started)
			select {
			case <-release:
				return RunResult{}, nil
			case <-ctx.Done():
				return RunResult{}, ctx.Err()
			}
		}),
		HistoryWriter: &recordingHistoryWriter{
			recordFn: func(string) error {
				close(finished)
				return nil
			},
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := s.Handle(ctx, SubmitPrompt{Text: "wait for me"}); err != nil {
		t.Fatalf("Handle(SubmitPrompt) = %v, want nil", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("submitted prompt did not start")
	}

	waitStarted := make(chan struct{})
	waitResult := make(chan bool, 1)
	go func() {
		close(waitStarted)
		waitResult <- s.WaitRuns(context.Background())
	}()
	<-waitStarted
	select {
	case <-waitResult:
		t.Fatal("WaitRuns returned before the run finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("submitted prompt did not finish")
	}
	select {
	case got := <-waitResult:
		if !got {
			t.Fatal("WaitRuns returned false after the run finished")
		}
	case <-time.After(time.Second):
		t.Fatal("WaitRuns did not return after the run finished")
	}
	select {
	case <-finished:
	default:
		t.Fatal("WaitRuns returned before the run completion side effect")
	}
}

func TestSessionWaitRunsReturnsFalseWhenContextDone(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := testNewSession(t, Dependencies{
		Runner: newRunExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
			close(started)
			<-release
			return RunResult{}, nil
		}),
	})

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "wait for cancellation"}); err != nil {
		t.Fatalf("Handle(SubmitPrompt) = %v, want nil", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("submitted prompt did not start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waitResult := make(chan bool, 1)
	go func() {
		waitResult <- s.WaitRuns(ctx)
	}()
	select {
	case got := <-waitResult:
		if got {
			t.Fatal("WaitRuns returned true for an already-cancelled context")
		}
	case <-time.After(time.Second):
		t.Fatal("WaitRuns did not return promptly for an already-cancelled context")
	}

	close(release)
	if !s.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns returned false after the blocked run was released")
	}
}

func TestSessionWaitRunsPrefersFinishedRunWithCancelledContext(t *testing.T) {
	s := testNewSession(t, Dependencies{
		Runner: newRunExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
			return RunResult{}, nil
		}),
	})

	if err := s.Handle(context.Background(), SubmitPrompt{Text: "quick run"}); err != nil {
		t.Fatalf("Handle(SubmitPrompt) = %v, want nil", err)
	}
	if !s.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns returned false after the run finished")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		runtime.Gosched()
		if s.WaitRuns(ctx) {
			return
		}
	}
	t.Fatal("WaitRuns did not prefer the finished run with a cancelled context")
}

func TestRotateSession(t *testing.T) {
	t.Parallel()

	t.Run("generates new ID and clears title", func(t *testing.T) {
		s := testNewSession(t, Dependencies{
			SessionStore: newMockSessionStore(),
			Config: config.Config{
				Models: config.ModelsConfig{
					Default:     "test",
					Definitions: map[string]config.ModelConfig{"test": {ID: "test-model"}},
				},
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

	t.Run("stamps supplied group", func(t *testing.T) {
		mockStore := newMockSessionStore()
		s := testNewSession(t, Dependencies{
			SessionStore: mockStore,
			Config: config.Config{
				Models: config.ModelsConfig{
					Default:     "test",
					Definitions: map[string]config.ModelConfig{"test": {ID: "test-model"}},
				},
			},
		})
		oldID := s.SessionID()

		if err := s.Handle(context.Background(), RotateSessionWithGroup{Group: "run-123"}); err != nil {
			t.Fatalf("RotateSessionWithGroup: %v", err)
		}
		if s.SessionID() == oldID {
			t.Fatal("expected new session ID after group rotation")
		}
		if got, want := s.sessionGroup, "run-123"; got != want {
			t.Fatalf("session group = %q, want %q", got, want)
		}
		if err := s.saveSession(); err != nil {
			t.Fatalf("saveSession after group rotation: %v", err)
		}
		saved, ok := mockStore.savedSessions[s.SessionID()]
		if !ok {
			t.Fatalf("savedSessions missing session %q", s.SessionID())
		}
		if got, want := saved.Group, "run-123"; got != want {
			t.Fatalf("saved group = %q, want %q", got, want)
		}
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
		Runner: newRunExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
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
		Runner: newRunExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
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
		Runner: newRunExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
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
		Runner: newRunExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
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
		Runner: newRunExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
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
		Runner: newRunExecutorFunc(func(_ context.Context, _ []agent.Message, _ []string) (RunResult, error) {
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
		Runner: newRunExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
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
		Runner: newRunExecutorFunc(func(ctx context.Context, _ []agent.Message, _ []string) (RunResult, error) {
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

// runExecutorFunc adapts functions to the runExecutor interface.
type runExecutorFunc struct {
	run             func(context.Context, []agent.Message, []string) (RunResult, error)
	compact         func(context.Context, []agent.Message, []string, []provider.ToolSpec) ([]agent.Message, error)
	compactSteering string
}

func newRunExecutorFunc(run func(context.Context, []agent.Message, []string) (RunResult, error)) *runExecutorFunc {
	return &runExecutorFunc{run: run}
}

func (f *runExecutorFunc) Run(ctx context.Context, conversation []agent.Message, skillNames []string, _ func() []agent.SteerMessage) (RunResult, error) {
	return f.run(ctx, conversation, skillNames)
}

func (f *runExecutorFunc) Compact(ctx context.Context, conversation []agent.Message, skillNames []string, tools []provider.ToolSpec, steering string) ([]agent.Message, error) {
	f.compactSteering = steering
	if f.compact == nil {
		return conversation, nil
	}
	return f.compact(ctx, conversation, skillNames, tools)
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
		if output.ContextDiagnosticKind(event.Payload) == "session_health" && output.ContextDiagnosticSeverity(event.Payload) == "warning" {
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

	tests := []struct {
		name         string
		model        string
		wantProvider string
		wantModel    string
		wantDefault  string
	}{
		{
			name:         "alias",
			model:        "fast",
			wantProvider: "new",
			wantModel:    "new-model",
			wantDefault:  "fast",
		},
		{
			name:         "provider model reference",
			model:        "openrouter/openai/gpt-4",
			wantProvider: "openrouter",
			wantModel:    "openai/gpt-4",
			wantDefault:  "openrouter/openai/gpt-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var events []output.Event
			var recordedProvider, recordedModel string
			recorded := 0
			s := testNewSession(t, Dependencies{
				BaseEvents: output.SinkFunc(func(event output.Event) {
					events = append(events, event)
				}),
				RecordModelSwitch: func(providerAlias, modelID string) error {
					recorded++
					recordedProvider = providerAlias
					recordedModel = modelID
					return nil
				},
				Config: config.Config{
					Providers: map[string]config.ProviderConfig{
						"old":        {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://old.example/v1"},
						"new":        {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://new.example/v1"},
						"openrouter": {Type: config.ProviderTypeOpenRouter, BaseURL: "http://openrouter.example/v1"},
					},
					Models: config.ModelsConfig{
						Default: "current",
						Definitions: map[string]config.ModelConfig{
							"current": {Provider: "old", ID: "old-model"},
							"fast":    {Provider: "new", ID: "new-model"},
						},
					},
				},
			})

			if err := s.Handle(context.Background(), SwitchModel{Name: tt.model}); err != nil {
				t.Fatalf("Handle(SwitchModel) = %v, want nil", err)
			}
			if got := s.deps.Config.Models.Default; got != tt.wantDefault {
				t.Fatalf("config default_model = %q, want %q", got, tt.wantDefault)
			}
			if recorded != 1 {
				t.Fatalf("RecordModelSwitch call count = %d, want 1", recorded)
			}
			if recordedProvider != tt.wantProvider || recordedModel != tt.wantModel {
				t.Fatalf("RecordModelSwitch args = %q, %q, want %q, %q", recordedProvider, recordedModel, tt.wantProvider, tt.wantModel)
			}

			for _, event := range events {
				if payload, ok := event.Payload.(output.ContextReportEvent); ok {
					if strings.Contains(payload.Content, "failed") {
						t.Fatalf("unexpected error event: %q", payload.Content)
					}
				}
			}
		})
	}
}

func TestSwitchModelNilRecorder(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			Providers: map[string]config.ProviderConfig{"local": {}},
			Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
				"model": {Provider: "local", ID: "model-id"},
			}},
		},
	})

	if err := s.Handle(context.Background(), SwitchModel{Name: "model"}); err != nil {
		t.Fatalf("Handle(SwitchModel) = %v, want nil", err)
	}
}

func TestSwitchModelFailure(t *testing.T) {
	t.Parallel()
	var events []output.Event
	recorded := 0
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
		RecordModelSwitch: func(string, string) error {
			recorded++
			return nil
		},
		Config: config.Config{
			Models: config.ModelsConfig{
				Default:     "current",
				Definitions: map[string]config.ModelConfig{"current": {ID: "current-id"}},
			},
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

	if got, want := s.deps.Config.Models.Default, "current"; got != want {
		t.Fatalf("config default_model after failed switch = %q, want %q", got, want)
	}
	if recorded != 0 {
		t.Fatalf("RecordModelSwitch call count = %d, want 0", recorded)
	}
}

func TestCurrentModelConfig(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			Models: config.ModelsConfig{
				Default:     "mymodel",
				Definitions: map[string]config.ModelConfig{"mymodel": {Provider: "local", ID: "test-model"}},
			},
			Providers: map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://example/v1"}},
		},
	})
	got := s.CurrentModelConfig()
	if got.ID != "test-model" {
		t.Fatalf("CurrentModelConfig().ID = %q, want %q", got.ID, "test-model")
	}
}

func TestCurrentModelConfigResolvesDefaultReference(t *testing.T) {
	tests := []struct {
		name         string
		defaultModel string
		definitions  map[string]config.ModelConfig
		providers    map[string]config.ProviderConfig
		wantProvider string
		wantID       string
	}{
		{
			name:         "configured alias",
			defaultModel: "alias",
			definitions:  map[string]config.ModelConfig{"alias": {Provider: "local", ID: "configured-id"}},
			providers:    map[string]config.ProviderConfig{"local": {}},
			wantProvider: "local",
			wantID:       "configured-id",
		},
		{
			name:         "raw reference",
			defaultModel: "openrouter/openai/gpt-4",
			providers:    map[string]config.ProviderConfig{"openrouter": {}},
			wantProvider: "openrouter",
			wantID:       "openai/gpt-4",
		},
		{
			name:         "unknown reference",
			defaultModel: "garbage",
			wantProvider: "",
			wantID:       "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testNewSession(t, Dependencies{Config: config.Config{
				Models:    config.ModelsConfig{Default: tt.defaultModel, Definitions: tt.definitions},
				Providers: tt.providers,
			}})
			got := s.CurrentModelConfig()
			if got.Provider != tt.wantProvider || got.ID != tt.wantID {
				t.Fatalf("CurrentModelConfig() = provider=%q id=%q, want provider=%q id=%q", got.Provider, got.ID, tt.wantProvider, tt.wantID)
			}
		})
	}
}

func TestCurrentModelAliasTracksSwitchModel(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			Models: config.ModelsConfig{
				Default: "current",
				Definitions: map[string]config.ModelConfig{
					"current": {Provider: "local", ID: "current-id"},
					"fast":    {Provider: "local", ID: "fast-id"},
				},
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

func TestWorkflowHandoffModelSelectionUsesDestinationDefault(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			Models: config.ModelsConfig{
				Default: "current",
				Definitions: map[string]config.ModelConfig{
					"current":           {Provider: "local", ID: "current-id"},
					"implement-default": {Provider: "local", ID: "implement-id"},
					"review-default":    {Provider: "local", ID: "review-id"},
				},
			},
		},
	})
	s.deps.Config.Models.WorkflowHandoff = map[string]string{
		"implement": "implement-default",
		"review":    "review-default",
	}

	if got, want := s.WorkflowHandoffModelSelection("implement"), (WorkflowHandoffModelSelection{
		ModelAlias:  "implement-default",
		SourceLabel: "from handoff default",
	}); got != want {
		t.Fatalf("WorkflowHandoffModelSelection(implement) = %#v, want %#v", got, want)
	}
	if got, want := s.WorkflowHandoffModelSelection("review"), (WorkflowHandoffModelSelection{
		ModelAlias:  "review-default",
		SourceLabel: "from handoff default",
	}); got != want {
		t.Fatalf("WorkflowHandoffModelSelection(review) = %#v, want %#v", got, want)
	}
	if got, want := s.CurrentModelAlias(), "current"; got != want {
		t.Fatalf("CurrentModelAlias() = %q, want %q", got, want)
	}
}

func TestWorkflowHandoffModelSelectionFallsBackToCurrentSession(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Config: config.Config{
			Models: config.ModelsConfig{
				Default: "current",
				Definitions: map[string]config.ModelConfig{
					"current": {Provider: "local", ID: "current-id"},
				},
			},
		},
	})

	if got, want := s.WorkflowHandoffModelSelection("implement"), (WorkflowHandoffModelSelection{
		ModelAlias:  "current",
		SourceLabel: "current session",
	}); got != want {
		t.Fatalf("WorkflowHandoffModelSelection(implement) = %#v, want %#v", got, want)
	}
	if got, want := s.WorkflowHandoffModelSelection("review"), (WorkflowHandoffModelSelection{
		ModelAlias:  "current",
		SourceLabel: "current session",
	}); got != want {
		t.Fatalf("WorkflowHandoffModelSelection(review) = %#v, want %#v", got, want)
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
		Runner: newRunExecutorFunc(func(_ context.Context, conversation []agent.Message, skillNames []string) (RunResult, error) {
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
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
	s.sessionGroup = "run-123"
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
	if got, want := saved.Group, "run-123"; got != want {
		t.Fatalf("saved group = %q, want %q", got, want)
	}
	if got, want := saved.Lineage.FullMessages(), lineage.FullMessages(); len(got) != len(want) || got[0].Content != want[0].Content {
		t.Fatalf("saved lineage = %#v, want %#v", got, want)
	}
}

func TestSaveSessionPersistsRawModelReference(t *testing.T) {
	mockStore := newMockSessionStore()
	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		Config: config.Config{
			Models:    config.ModelsConfig{Default: "openrouter/openai/gpt-4"},
			Providers: map[string]config.ProviderConfig{"openrouter": {}},
		},
	})
	if err := s.saveSession(); err != nil {
		t.Fatalf("saveSession() = %v, want nil", err)
	}
	if got := mockStore.savedSessions[s.SessionID()].Model; got != "openai/gpt-4" {
		t.Fatalf("saved model = %q, want %q", got, "openai/gpt-4")
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
	m.loadedSessions[s.ID] = s
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

func TestLoadSessionRestoresMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		persisted string
		want      config.ExecutionMode
	}{
		{name: "empty uses configured default", persisted: "", want: config.ExecutionModePlan},
		{name: "plan accepted", persisted: "plan", want: config.ExecutionModePlan},
		{name: "build accepted", persisted: "build", want: config.ExecutionModeBuild},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockStore := newMockSessionStore()
			mockStore.loadedSessions["mode-session"] = session.Session{
				ID:    "mode-session",
				Title: "Mode Session",
				Model: "test-model",
				Mode:  tc.persisted,
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

			var capturedConversations [][]agent.Message
			deps := Dependencies{
				SessionStore: mockStore,
				Config: config.Config{
					Modes: config.ModesConfig{Default: config.ExecutionModePlan},
				},
			}
			if tc.want == config.ExecutionModeBuild {
				// Capture the next-turn runner submission so the test proves a
				// restored session re-announces its mode through the real
				// submission path, not only via the private helper.
				deps.Runner = newRunExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
					capturedConversations = append(capturedConversations, cloneMessages(conversation))
					result := append(append([]agent.Message(nil), conversation...), agent.Message{
						Role:    agent.MessageRoleAssistant,
						Content: "response",
					})
					return RunResult{Conversation: result}, nil
				})
			}
			s := testNewSession(t, deps)

			var listenerCalled bool
			s.SetModeListener(func(config.ExecutionMode) { listenerCalled = true })

			if err := s.Handle(context.Background(), LoadSession{SessionID: "mode-session"}); err != nil {
				t.Fatalf("Handle(LoadSession) = %v, want nil", err)
			}
			if got := s.Mode(); got != tc.want {
				t.Fatalf("Mode() = %q, want %q", got, tc.want)
			}
			if !listenerCalled {
				t.Fatal("mode listener not called on successful restore")
			}
			wantNotice := prompt.ModeNotice(tc.want) + "\n\n"
			if notice := s.modeNotice(); notice != wantNotice {
				t.Fatalf("modeNotice() after restore = %q, want %q (restored session must re-announce its mode)", notice, wantNotice)
			}

			if tc.want == config.ExecutionModeBuild {
				s.submitPrompt(context.Background(), "next prompt", nil)

				if len(capturedConversations) != 1 {
					t.Fatalf("expected 1 captured conversation after next turn, got %d", len(capturedConversations))
				}
				buildNotice := prompt.ModeNotice(config.ExecutionModeBuild)
				received := capturedConversations[0]
				foundNoticeInReceived := false
				for _, msg := range received {
					if msg.Role == agent.MessageRoleUser && strings.HasPrefix(msg.Content, buildNotice) {
						foundNoticeInReceived = true
						break
					}
				}
				if !foundNoticeInReceived {
					t.Fatalf("runner-received conversation = %#v, want a user message prefixed with build notice %q", received, buildNotice)
				}

				storedConv := s.Conversation()
				foundNoticeInStored := false
				for _, msg := range storedConv {
					if msg.Role == agent.MessageRoleUser && strings.Contains(msg.Content, "[execution mode: build]") {
						foundNoticeInStored = true
						break
					}
				}
				if !foundNoticeInStored {
					t.Fatal("stored conversation does not retain build mode notice after the next turn")
				}
			}
		})
	}
}

func TestLoadSessionRejectsUnknownMode(t *testing.T) {
	t.Parallel()
	var events []output.Event
	mockStore := newMockSessionStore()
	mockStore.loadedSessions["bad-mode-session"] = session.Session{
		ID:    "bad-mode-session",
		Title: "Bad Mode Session",
		Model: "test-model",
		Mode:  "readwrite",
	}

	s := testNewSession(t, Dependencies{
		BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		SessionStore: mockStore,
		Config: config.Config{
			Modes: config.ModesConfig{Default: config.ExecutionModePlan},
		},
	})

	var listenerCalls int
	s.SetModeListener(func(config.ExecutionMode) { listenerCalls++ })

	err := s.Handle(context.Background(), LoadSession{SessionID: "bad-mode-session"})
	if err == nil {
		t.Fatal("Handle(LoadSession) = nil, want error for unknown mode")
	}
	if !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("error = %v, want substring 'unknown mode'", err)
	}
	if got := s.Mode(); got != config.ExecutionModePlan {
		t.Fatalf("Mode() = %q, want unchanged %q", got, config.ExecutionModePlan)
	}
	if listenerCalls != 0 {
		t.Fatalf("mode listener calls = %d, want 0 (rejected restore must not mutate state)", listenerCalls)
	}
	var foundReport bool
	for _, event := range events {
		if event.Type == output.EventTypeContextReport {
			if payload, ok := event.Payload.(output.ContextReportEvent); ok &&
				strings.Contains(payload.Content, "load session failed") &&
				strings.Contains(payload.Content, "unknown mode") {
				foundReport = true
			}
		}
	}
	if !foundReport {
		t.Fatalf("events = %#v, want Context Report 'load session failed: unknown mode' event", events)
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
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

func TestLoadSessionSkipsOrphanedToolCallWithNoResult(t *testing.T) {
	t.Parallel()

	// Simulate a session that ended with an accepted workflow_handoff: the
	// assistant message has a tool call but no paired tool result exists because
	// WorkflowHandoffAccepted stops the run without appending one.
	const sessionID = "handoff-session"
	var events []output.Event
	mockStore := newMockSessionStore()
	mockStore.loadedSessions[sessionID] = session.Session{
		ID:    sessionID,
		Title: "Handoff Session",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID: 1,
					Messages: []agent.Message{
						{Role: agent.MessageRoleUser, Content: "hand off"},
						{
							Role: agent.MessageRoleAssistant,
							ToolCalls: []agent.ToolCall{
								{ID: "call_handoff_1", Name: "workflow_handoff", Arguments: map[string]any{"next": "implement", "target": ".steiner/plans/foo"}},
							},
						},
						// no tool result: workflow_handoff accepted stops run without one
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
			},
		},
	})

	if err := s.Handle(context.Background(), LoadSession{SessionID: sessionID}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	for _, event := range events {
		if event.Type == output.EventTypeToolCallStarted {
			payload, _ := event.Payload.(output.ToolCallStartedEvent)
			t.Errorf("unexpected ToolCallStarted event for orphaned tool call %q; TUI would show it as still-running", payload.Tool)
		}
		if event.Type == output.EventTypeToolCallFinished {
			payload, _ := event.Payload.(output.ToolCallFinishedEvent)
			t.Errorf("unexpected ToolCallFinished event for orphaned tool call %q", payload.Tool)
		}
	}
}

func TestSubmitPromptWithImages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		images []agent.ImageBlock
	}{
		{
			name:   "images are attached to the user message",
			images: []agent.ImageBlock{{MediaType: "image/png", Data: "base64encodeddata"}},
		},
		{
			name:   "text-only prompt carries no images",
			images: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []agent.Message
			s := testNewSession(t, Dependencies{
				Runner: newRunExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
					got = conversation
					return RunResult{Conversation: conversation}, nil
				}),
			})

			s.submitPrompt(context.Background(), "hello", tt.images)

			if len(got) != 1 {
				t.Fatalf("runner conversation length = %d, want 1", len(got))
			}
			if !reflect.DeepEqual(got[0].Images, tt.images) {
				t.Fatalf("runner user message Images = %+v, want %+v", got[0].Images, tt.images)
			}
			if !reflect.DeepEqual(s.Conversation()[0].Images, tt.images) {
				t.Fatalf("stored user message Images = %+v, want %+v", s.Conversation()[0].Images, tt.images)
			}
		})
	}
}

func TestForkSession(t *testing.T) {
	t.Parallel()

	t.Run("forks current session and switches to fork", func(t *testing.T) {
		var events []output.Event
		mockStore := newMockSessionStore()
		s := testNewSession(t, Dependencies{
			BaseEvents: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			}),
			SessionStore: mockStore,
			Config: config.Config{
				Models: config.ModelsConfig{
					Default: "test",
					Definitions: map[string]config.ModelConfig{
						"test": {ID: "test-model"},
					},
				},
			},
		})

		// Set up session with title and conversation
		originalID := s.SessionID()
		originalTitle := "Test Session"
		s.mu.Lock()
		s.sessionTitle = originalTitle
		s.conversation = []agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}}
		s.lineage = agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID:       1,
					Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "hello"}},
				},
			},
			NextGenerationID: 2,
		}
		s.mu.Unlock()

		// Execute ForkSession
		if err := s.Handle(context.Background(), ForkSession{}); err != nil {
			t.Fatalf("Handle(ForkSession) = %v, want nil", err)
		}

		// Verify new session was created with different ID
		if newID := s.SessionID(); newID == originalID {
			t.Fatalf("session ID unchanged after fork: %q", newID)
		}

		// Verify new title has "Fork of:" prefix
		newTitle := s.SessionTitle()
		if !strings.HasPrefix(newTitle, "Fork of:") {
			t.Fatalf("fork session title = %q, want prefix 'Fork of:'", newTitle)
		}

		// Verify context report event was emitted
		var found bool
		for _, event := range events {
			if payload, ok := event.Payload.(output.ContextReportEvent); ok {
				if strings.Contains(payload.Content, "Forked from:") && strings.Contains(payload.Content, originalTitle) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatalf("events = %#v, want ContextReportEvent with 'Forked from'", events)
		}

		// Verify original session was saved
		savedOrig, ok := mockStore.savedSessions[originalID]
		if !ok {
			t.Fatal("original session was not saved before fork")
		}
		if savedOrig.Title != originalTitle {
			t.Fatalf("saved original title = %q, want %q", savedOrig.Title, originalTitle)
		}
	})

	t.Run("noop when SessionStore is nil", func(t *testing.T) {
		s := testNewSession(t, Dependencies{})
		originalID := s.SessionID()

		if err := s.Handle(context.Background(), ForkSession{}); err != nil {
			t.Fatalf("Handle(ForkSession) with nil store = %v, want nil", err)
		}

		// Session ID should not change if store is nil
		if s.SessionID() != originalID {
			t.Fatalf("session ID changed with nil store: %q -> %q", originalID, s.SessionID())
		}
	})
}

func TestForkSavedSession(t *testing.T) {
	t.Parallel()

	t.Run("forks saved session and switches to fork", func(t *testing.T) {
		var events []output.Event
		mockStore := newMockSessionStore()

		// Create a mock saved session
		originalSession := session.Session{
			ID:    "saved-session-id",
			Title: "Original Saved Session",
			Model: "test-model",
			Lineage: agent.ConversationLineage{
				Generations: []agent.ConversationGeneration{
					{
						ID:       1,
						Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "old message"}},
					},
				},
				NextGenerationID: 2,
			},
		}
		mockStore.loadedSessions["saved-session-id"] = originalSession

		s := testNewSession(t, Dependencies{
			BaseEvents: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			}),
			SessionStore: mockStore,
			Config: config.Config{
				Models: config.ModelsConfig{
					Default: "test",
					Definitions: map[string]config.ModelConfig{
						"test": {ID: "test-model"},
					},
				},
			},
		})

		originalSessionID := s.SessionID()

		// Execute ForkSavedSession
		if err := s.Handle(context.Background(), ForkSavedSession{SessionID: "saved-session-id"}); err != nil {
			t.Fatalf("Handle(ForkSavedSession) = %v, want nil", err)
		}

		// Verify current session switched to fork (different ID than before)
		if newID := s.SessionID(); newID == originalSessionID {
			t.Fatalf("session ID should change on fork saved session, got %q", newID)
		}

		// Verify new title has "Fork of:" prefix
		newTitle := s.SessionTitle()
		if !strings.HasPrefix(newTitle, "Fork of:") {
			t.Fatalf("fork saved session title = %q, want prefix 'Fork of:'", newTitle)
		}

		// Verify context report event was emitted
		var found bool
		for _, event := range events {
			if payload, ok := event.Payload.(output.ContextReportEvent); ok {
				if strings.Contains(payload.Content, "Forked from:") && strings.Contains(payload.Content, "Original Saved Session") {
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatalf("events = %#v, want ContextReportEvent with correct fork message", events)
		}

		// Verify fork was saved to the store
		forkedID := s.SessionID()
		savedFork, ok := mockStore.savedSessions[forkedID]
		if !ok {
			t.Fatal("forked session was not saved to store")
		}
		if !strings.HasPrefix(savedFork.Title, "Fork of:") {
			t.Fatalf("saved fork title = %q, want prefix 'Fork of:'", savedFork.Title)
		}

		// Verify conversation was loaded from fork lineage
		conv := s.Conversation()
		if len(conv) != 1 || conv[0].Content != "old message" {
			t.Fatalf("conversation after fork = %v, want original message", conv)
		}
	})

	t.Run("error on non-existent session ID", func(t *testing.T) {
		var events []output.Event
		mockStore := newMockSessionStore()

		s := testNewSession(t, Dependencies{
			BaseEvents: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			}),
			SessionStore: mockStore,
			Config: config.Config{
				Models: config.ModelsConfig{
					Default: "test",
					Definitions: map[string]config.ModelConfig{
						"test": {ID: "test-model"},
					},
				},
			},
		})

		originalID := s.SessionID()

		// Execute ForkSavedSession with non-existent ID
		err := s.Handle(context.Background(), ForkSavedSession{SessionID: "non-existent"})
		if err == nil {
			t.Fatal("Handle(ForkSavedSession) with non-existent ID = nil, want error")
		}

		// Verify session ID did not change
		if s.SessionID() != originalID {
			t.Fatalf("session ID changed on error: %q -> %q", originalID, s.SessionID())
		}

		// Verify error event was emitted
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
	})

	t.Run("noop when SessionStore is nil", func(t *testing.T) {
		s := testNewSession(t, Dependencies{})
		originalID := s.SessionID()

		if err := s.Handle(context.Background(), ForkSavedSession{SessionID: "any-id"}); err != nil {
			t.Fatalf("Handle(ForkSavedSession) with nil store = %v, want nil", err)
		}

		// Session ID should not change if store is nil
		if s.SessionID() != originalID {
			t.Fatalf("session ID changed with nil store: %q -> %q", originalID, s.SessionID())
		}
	})
}

func TestPromptCacheKeyNewSessionMatchesSessionID(t *testing.T) {
	t.Parallel()

	s := testNewSession(t, Dependencies{})
	if got, want := s.PromptCacheKey(), s.SessionID(); got != want {
		t.Fatalf("PromptCacheKey() = %q, want byte-identical session ID %q", got, want)
	}
}

func TestPromptCacheKeyRotateMatchesNewID(t *testing.T) {
	t.Parallel()

	s := testNewSession(t, Dependencies{
		SessionStore: newMockSessionStore(),
		Config: config.Config{
			Models: config.ModelsConfig{
				Default:     "test",
				Definitions: map[string]config.ModelConfig{"test": {ID: "test-model"}},
			},
		},
	})
	oldKey := s.PromptCacheKey()

	if err := s.Handle(context.Background(), RotateSession{}); err != nil {
		t.Fatalf("RotateSession: %v", err)
	}
	newKey := s.PromptCacheKey()
	if newKey == oldKey {
		t.Fatal("PromptCacheKey unchanged after rotation, want a fresh key")
	}
	if newKey != s.SessionID() {
		t.Fatalf("PromptCacheKey() = %q after rotation, want it to match the new session ID %q", newKey, s.SessionID())
	}
}

func TestPromptCacheKeyLoadUsesStoredValue(t *testing.T) {
	t.Parallel()

	mockStore := newMockSessionStore()
	mockStore.loadedSessions["with-key"] = session.Session{
		ID:             "with-key",
		Title:          "Has Stored Key",
		PromptCacheKey: "stored-cache-key",
	}
	mockStore.loadedSessions["legacy"] = session.Session{
		ID:    "legacy",
		Title: "No Stored Key",
	}

	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		Config: config.Config{
			Models: config.ModelsConfig{
				Default:     "test",
				Definitions: map[string]config.ModelConfig{"test": {ID: "test-model"}},
			},
		},
	})

	if err := s.LoadSessionByID(context.Background(), "with-key"); err != nil {
		t.Fatalf("LoadSessionByID(with-key): %v", err)
	}
	if got, want := s.PromptCacheKey(), "stored-cache-key"; got != want {
		t.Fatalf("PromptCacheKey() = %q, want stored value %q", got, want)
	}

	// A record with no stored prompt_cache_key falls back to its session ID.
	if err := s.LoadSessionByID(context.Background(), "legacy"); err != nil {
		t.Fatalf("LoadSessionByID(legacy): %v", err)
	}
	if got, want := s.PromptCacheKey(), "legacy"; got != want {
		t.Fatalf("PromptCacheKey() = %q, want fallback to session ID %q", got, want)
	}
}

func TestPromptCacheKeyForkInheritsLiveSessionKey(t *testing.T) {
	t.Parallel()

	mockStore := newMockSessionStore()
	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		Config: config.Config{
			Models: config.ModelsConfig{
				Default:     "test",
				Definitions: map[string]config.ModelConfig{"test": {ID: "test-model"}},
			},
		},
	})

	parentKey := s.PromptCacheKey()

	if err := s.Handle(context.Background(), ForkSession{}); err != nil {
		t.Fatalf("Handle(ForkSession): %v", err)
	}

	if got := s.PromptCacheKey(); got != parentKey {
		t.Fatalf("PromptCacheKey() after fork = %q, want inherited parent key %q", got, parentKey)
	}

	// The saved fork record on disk must carry the same key, otherwise
	// loading the fork later regresses to its own ID.
	forked, ok := mockStore.savedSessions[s.SessionID()]
	if !ok {
		t.Fatal("forked session was not saved to store")
	}
	if got := forked.PromptCacheKey; got != parentKey {
		t.Fatalf("saved fork PromptCacheKey = %q, want inherited parent key %q", got, parentKey)
	}
}

func TestPromptCacheKeyForkOfForkOfSavedSessionMaterializesOnFirstFork(t *testing.T) {
	t.Parallel()

	mockStore := newMockSessionStore()
	mockStore.loadedSessions["legacy-root"] = session.Session{
		ID:    "legacy-root",
		Title: "Legacy",
	}

	s := testNewSession(t, Dependencies{
		SessionStore: mockStore,
		Config: config.Config{
			Models: config.ModelsConfig{
				Default:     "test",
				Definitions: map[string]config.ModelConfig{"test": {ID: "test-model"}},
			},
		},
	})

	if err := s.Handle(context.Background(), ForkSavedSession{SessionID: "legacy-root"}); err != nil {
		t.Fatalf("Handle(ForkSavedSession): %v", err)
	}
	firstForkKey := s.PromptCacheKey()
	if firstForkKey != "legacy-root" {
		t.Fatalf("first fork PromptCacheKey() = %q, want healed to root ID %q", firstForkKey, "legacy-root")
	}

	if err := s.Handle(context.Background(), ForkSession{}); err != nil {
		t.Fatalf("Handle(ForkSession) on fork-of-fork: %v", err)
	}
	if got := s.PromptCacheKey(); got != firstForkKey {
		t.Fatalf("fork-of-fork PromptCacheKey() = %q, want propagated key %q", got, firstForkKey)
	}
}

func TestLoadSessionRestoresDelegationBoxes(t *testing.T) {
	t.Parallel()

	const sessionID = "delegation-session"
	var events []output.Event
	mockStore := newMockSessionStore()
	mockStore.loadedSessions[sessionID] = session.Session{
		ID:    sessionID,
		Title: "Delegation Session",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID: 1,
					Messages: []agent.Message{
						{Role: agent.MessageRoleUser, Content: "explore the codebase"},
						{
							Role: agent.MessageRoleAssistant,
							ToolCalls: []agent.ToolCall{
								{ID: "call_explore_1", Name: "explore", Arguments: map[string]any{"task": "find auth files"}},
							},
						},
						{
							Role:       agent.MessageRoleTool,
							Content:    "found 3 files",
							ToolCallID: "call_explore_1",
							Name:       "explore",
							Retention: &agent.MessageRetention{
								AgentID:    "agent-abc123",
								Status:     "complete",
								TurnCount:  5,
								TokenCount: 1200,
							},
						},
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
			},
		},
	})

	if err := s.Handle(context.Background(), LoadSession{SessionID: sessionID}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	// Verify DelegationStarted event
	var delegationStarted []output.DelegationStartedEvent
	for _, event := range events {
		if event.Type == output.EventTypeDelegationStarted {
			payload, ok := event.Payload.(output.DelegationStartedEvent)
			if !ok {
				t.Fatalf("delegation started payload type = %T, want output.DelegationStartedEvent", event.Payload)
			}
			delegationStarted = append(delegationStarted, payload)
		}
	}
	if got, want := len(delegationStarted), 1; got != want {
		t.Fatalf("delegation started events = %d, want %d", got, want)
	}
	if got, want := delegationStarted[0].AgentID, "agent-abc123"; got != want {
		t.Fatalf("delegation started agent id = %q, want %q", got, want)
	}
	if got, want := delegationStarted[0].TaskPreview, "find auth files"; got != want {
		t.Fatalf("delegation started task preview = %q, want %q", got, want)
	}

	// Verify DelegationComplete event with retention data
	var delegationComplete []output.DelegationCompleteEvent
	for _, event := range events {
		if event.Type == output.EventTypeDelegationComplete {
			payload, ok := event.Payload.(output.DelegationCompleteEvent)
			if !ok {
				t.Fatalf("delegation complete payload type = %T, want output.DelegationCompleteEvent", event.Payload)
			}
			delegationComplete = append(delegationComplete, payload)
		}
	}
	if got, want := len(delegationComplete), 1; got != want {
		t.Fatalf("delegation complete events = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].AgentID, "agent-abc123"; got != want {
		t.Fatalf("delegation complete agent id = %q, want %q", got, want)
	}
	if got, want := delegationComplete[0].Status, "complete"; got != want {
		t.Fatalf("delegation complete status = %q, want %q", got, want)
	}
	if got, want := delegationComplete[0].TurnCount, 5; got != want {
		t.Fatalf("delegation complete turn count = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].TokenCount, 1200; got != want {
		t.Fatalf("delegation complete token count = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].Output, "found 3 files"; got != want {
		t.Fatalf("delegation complete output = %q, want %q", got, want)
	}

	// Verify ToolCallStarted event for the delegate call so replay uses the live TUI path
	var toolStarted []output.ToolCallStartedEvent
	for _, event := range events {
		if event.Type == output.EventTypeToolCallStarted {
			payload, ok := event.Payload.(output.ToolCallStartedEvent)
			if !ok {
				t.Fatalf("tool started payload type = %T, want output.ToolCallStartedEvent", event.Payload)
			}
			toolStarted = append(toolStarted, payload)
		}
	}
	if got, want := len(toolStarted), 1; got != want {
		t.Fatalf("tool started events = %d, want %d", got, want)
	}
	if got, want := toolStarted[0].CallID, "call_explore_1"; got != want {
		t.Fatalf("tool started call id = %q, want %q", got, want)
	}
	if got, want := toolStarted[0].Tool, "explore"; got != want {
		t.Fatalf("tool started tool = %q, want %q", got, want)
	}

	// Verify NO ToolCallFinished events for the delegate call
	var toolFinished []output.ToolCallFinishedEvent
	for _, event := range events {
		if event.Type == output.EventTypeToolCallFinished {
			payload, ok := event.Payload.(output.ToolCallFinishedEvent)
			if !ok {
				t.Fatalf("tool finished payload type = %T, want output.ToolCallFinishedEvent", event.Payload)
			}
			toolFinished = append(toolFinished, payload)
		}
	}
	for _, tf := range toolFinished {
		if tf.CallID == "call_explore_1" {
			t.Fatalf("unexpected ToolCallFinished for delegation call %q", tf.CallID)
		}
	}
}

func TestLoadSessionRestoresDelegationBoxesWithoutRetention(t *testing.T) {
	t.Parallel()

	const sessionID = "delegation-no-retention-session"
	var events []output.Event
	mockStore := newMockSessionStore()
	mockStore.loadedSessions[sessionID] = session.Session{
		ID:    sessionID,
		Title: "Delegation Without Retention Session",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID: 1,
					Messages: []agent.Message{
						{Role: agent.MessageRoleUser, Content: "do something"},
						{
							Role: agent.MessageRoleAssistant,
							ToolCalls: []agent.ToolCall{
								{ID: "call_delegate_2", Name: "explore", Arguments: map[string]any{"task": "handle this"}},
							},
						},
						{
							Role:       agent.MessageRoleTool,
							Content:    "done",
							ToolCallID: "call_delegate_2",
							Name:       "explore",
							Retention:  nil,
						},
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
			},
		},
	})

	if err := s.Handle(context.Background(), LoadSession{SessionID: sessionID}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	// Verify DelegationStarted event
	var delegationStarted []output.DelegationStartedEvent
	for _, event := range events {
		if event.Type == output.EventTypeDelegationStarted {
			payload, ok := event.Payload.(output.DelegationStartedEvent)
			if !ok {
				t.Fatalf("delegation started payload type = %T, want output.DelegationStartedEvent", event.Payload)
			}
			delegationStarted = append(delegationStarted, payload)
		}
	}
	if got, want := len(delegationStarted), 1; got != want {
		t.Fatalf("delegation started events = %d, want %d", got, want)
	}
	if got, want := delegationStarted[0].AgentID, "agent-call_delegate_2"; got != want {
		t.Fatalf("delegation started agent id = %q, want %q", got, want)
	}

	// Verify DelegationComplete event with synthetic data (no retention)
	var delegationComplete []output.DelegationCompleteEvent
	for _, event := range events {
		if event.Type == output.EventTypeDelegationComplete {
			payload, ok := event.Payload.(output.DelegationCompleteEvent)
			if !ok {
				t.Fatalf("delegation complete payload type = %T, want output.DelegationCompleteEvent", event.Payload)
			}
			delegationComplete = append(delegationComplete, payload)
		}
	}
	if got, want := len(delegationComplete), 1; got != want {
		t.Fatalf("delegation complete events = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].AgentID, "agent-call_delegate_2"; got != want {
		t.Fatalf("delegation complete agent id = %q, want %q (synthetic)", got, want)
	}
	if got, want := delegationComplete[0].Status, "complete"; got != want {
		t.Fatalf("delegation complete status = %q, want %q", got, want)
	}
	if got, want := delegationComplete[0].TurnCount, 0; got != want {
		t.Fatalf("delegation complete turn count = %d, want %d (no retention)", got, want)
	}
	if got, want := delegationComplete[0].TokenCount, 0; got != want {
		t.Fatalf("delegation complete token count = %d, want %d (no retention)", got, want)
	}
	if got, want := delegationComplete[0].Output, "done"; got != want {
		t.Fatalf("delegation complete output = %q, want %q", got, want)
	}
}

func TestLoadSessionMixesDelegateAndRegularToolCalls(t *testing.T) {
	t.Parallel()

	const sessionID = "mixed-tools-session"
	var events []output.Event
	mockStore := newMockSessionStore()
	mockStore.loadedSessions[sessionID] = session.Session{
		ID:    sessionID,
		Title: "Mixed Tools Session",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID: 1,
					Messages: []agent.Message{
						{Role: agent.MessageRoleUser, Content: "do things"},
						{
							Role: agent.MessageRoleAssistant,
							ToolCalls: []agent.ToolCall{
								{ID: "call_reg_1", Name: "read", Arguments: map[string]any{"path": "main.go"}},
								{ID: "call_del_1", Name: "explore", Arguments: map[string]any{"task": "find tests"}},
							},
						},
						{
							Role:       agent.MessageRoleTool,
							Content:    "package main",
							ToolCallID: "call_reg_1",
							Name:       "read",
							Retention:  nil,
						},
						{
							Role:       agent.MessageRoleTool,
							Content:    "found tests",
							ToolCallID: "call_del_1",
							Name:       "explore",
							Retention: &agent.MessageRetention{
								AgentID:    "agent-exp-1",
								Status:     "complete",
								TurnCount:  2,
								TokenCount: 300,
							},
						},
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
			},
		},
	})

	if err := s.Handle(context.Background(), LoadSession{SessionID: sessionID}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	// Verify ToolCallStarted event for regular call
	var toolStarted []output.ToolCallStartedEvent
	for _, event := range events {
		if event.Type == output.EventTypeToolCallStarted {
			payload, ok := event.Payload.(output.ToolCallStartedEvent)
			if !ok {
				t.Fatalf("tool started payload type = %T, want output.ToolCallStartedEvent", event.Payload)
			}
			toolStarted = append(toolStarted, payload)
		}
	}
	if got, want := len(toolStarted), 2; got != want {
		t.Fatalf("tool started events = %d, want %d", got, want)
	}
	if got, want := toolStarted[0].CallID, "call_reg_1"; got != want {
		t.Fatalf("tool started call id = %q, want %q", got, want)
	}
	if got, want := toolStarted[0].Tool, "read"; got != want {
		t.Fatalf("tool started tool = %q, want %q", got, want)
	}
	if got, want := toolStarted[1].CallID, "call_del_1"; got != want {
		t.Fatalf("tool started call id = %q, want %q", got, want)
	}
	if got, want := toolStarted[1].Tool, "explore"; got != want {
		t.Fatalf("tool started tool = %q, want %q", got, want)
	}

	// Verify ToolCallFinished event for regular call
	var toolFinished []output.ToolCallFinishedEvent
	for _, event := range events {
		if event.Type == output.EventTypeToolCallFinished {
			payload, ok := event.Payload.(output.ToolCallFinishedEvent)
			if !ok {
				t.Fatalf("tool finished payload type = %T, want output.ToolCallFinishedEvent", event.Payload)
			}
			toolFinished = append(toolFinished, payload)
		}
	}
	if got, want := len(toolFinished), 1; got != want {
		t.Fatalf("tool finished events = %d, want %d (regular call only)", got, want)
	}
	if got, want := toolFinished[0].CallID, "call_reg_1"; got != want {
		t.Fatalf("tool finished call id = %q, want %q", got, want)
	}
	if got, want := toolFinished[0].Tool, "read"; got != want {
		t.Fatalf("tool finished tool = %q, want %q", got, want)
	}
	if got, want := toolFinished[0].Result, "package main"; got != want {
		t.Fatalf("tool finished result = %q, want %q", got, want)
	}

	// Verify DelegationStarted event for delegate call
	var delegationStarted []output.DelegationStartedEvent
	for _, event := range events {
		if event.Type == output.EventTypeDelegationStarted {
			payload, ok := event.Payload.(output.DelegationStartedEvent)
			if !ok {
				t.Fatalf("delegation started payload type = %T, want output.DelegationStartedEvent", event.Payload)
			}
			delegationStarted = append(delegationStarted, payload)
		}
	}
	if got, want := len(delegationStarted), 1; got != want {
		t.Fatalf("delegation started events = %d, want %d", got, want)
	}
	if got, want := delegationStarted[0].AgentID, "agent-exp-1"; got != want {
		t.Fatalf("delegation started agent id = %q, want %q", got, want)
	}
	if got, want := delegationStarted[0].TaskPreview, "find tests"; got != want {
		t.Fatalf("delegation started task preview = %q, want %q", got, want)
	}

	// Verify DelegationComplete event for delegate call
	var delegationComplete []output.DelegationCompleteEvent
	for _, event := range events {
		if event.Type == output.EventTypeDelegationComplete {
			payload, ok := event.Payload.(output.DelegationCompleteEvent)
			if !ok {
				t.Fatalf("delegation complete payload type = %T, want output.DelegationCompleteEvent", event.Payload)
			}
			delegationComplete = append(delegationComplete, payload)
		}
	}
	if got, want := len(delegationComplete), 1; got != want {
		t.Fatalf("delegation complete events = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].AgentID, "agent-exp-1"; got != want {
		t.Fatalf("delegation complete agent id = %q, want %q", got, want)
	}
	if got, want := delegationComplete[0].Status, "complete"; got != want {
		t.Fatalf("delegation complete status = %q, want %q", got, want)
	}
	if got, want := delegationComplete[0].TurnCount, 2; got != want {
		t.Fatalf("delegation complete turn count = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].TokenCount, 300; got != want {
		t.Fatalf("delegation complete token count = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].Output, "found tests"; got != want {
		t.Fatalf("delegation complete output = %q, want %q", got, want)
	}
}

func TestLoadSessionRestoresDelegationBoxesFromStructuredResult(t *testing.T) {
	t.Parallel()

	const sessionID = "delegation-structured-result-session"
	var events []output.Event
	mockStore := newMockSessionStore()
	mockStore.loadedSessions[sessionID] = session.Session{
		ID:    sessionID,
		Title: "Delegation Structured Result Session",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID: 1,
					Messages: []agent.Message{
						{Role: agent.MessageRoleUser, Content: "plan this work"},
						{
							Role: agent.MessageRoleAssistant,
							ToolCalls: []agent.ToolCall{
								{ID: "call_evaluate_1", Name: "evaluate", Arguments: map[string]any{"task": "plan the rollout"}},
							},
						},
						{
							Role:       agent.MessageRoleTool,
							Content:    `{"agent_id":"agent-evaluate-1","status":"partial","output":"final prose output","summary":"summary text","turn_count":3,"token_count":456,"tool_call_count":2}`,
							ToolCallID: "call_evaluate_1",
							Name:       "evaluate",
						},
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
			},
		},
	})

	if err := s.Handle(context.Background(), LoadSession{SessionID: sessionID}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	var toolStarted []output.ToolCallStartedEvent
	var delegationStarted []output.DelegationStartedEvent
	var delegationComplete []output.DelegationCompleteEvent
	for _, event := range events {
		switch event.Type {
		case output.EventTypeToolCallStarted:
			payload, ok := event.Payload.(output.ToolCallStartedEvent)
			if !ok {
				t.Fatalf("tool started payload type = %T, want output.ToolCallStartedEvent", event.Payload)
			}
			toolStarted = append(toolStarted, payload)
		case output.EventTypeDelegationStarted:
			payload, ok := event.Payload.(output.DelegationStartedEvent)
			if !ok {
				t.Fatalf("delegation started payload type = %T, want output.DelegationStartedEvent", event.Payload)
			}
			delegationStarted = append(delegationStarted, payload)
		case output.EventTypeDelegationComplete:
			payload, ok := event.Payload.(output.DelegationCompleteEvent)
			if !ok {
				t.Fatalf("delegation complete payload type = %T, want output.DelegationCompleteEvent", event.Payload)
			}
			delegationComplete = append(delegationComplete, payload)
		}
	}

	if got, want := len(toolStarted), 1; got != want {
		t.Fatalf("tool started events = %d, want %d", got, want)
	}
	if got, want := toolStarted[0].Tool, "evaluate"; got != want {
		t.Fatalf("tool started tool = %q, want %q", got, want)
	}
	if got, want := len(delegationStarted), 1; got != want {
		t.Fatalf("delegation started events = %d, want %d", got, want)
	}
	if got, want := delegationStarted[0].AgentID, "agent-evaluate-1"; got != want {
		t.Fatalf("delegation started agent id = %q, want %q", got, want)
	}
	if got, want := delegationStarted[0].TaskPreview, "plan the rollout"; got != want {
		t.Fatalf("delegation started task preview = %q, want %q", got, want)
	}
	if got, want := len(delegationComplete), 1; got != want {
		t.Fatalf("delegation complete events = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].AgentID, "agent-evaluate-1"; got != want {
		t.Fatalf("delegation complete agent id = %q, want %q", got, want)
	}
	if got, want := delegationComplete[0].Status, "partial"; got != want {
		t.Fatalf("delegation complete status = %q, want %q", got, want)
	}
	if got, want := delegationComplete[0].TurnCount, 3; got != want {
		t.Fatalf("delegation complete turn count = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].TokenCount, 456; got != want {
		t.Fatalf("delegation complete token count = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].ToolCallCount, 2; got != want {
		t.Fatalf("delegation complete tool call count = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].Output, "final prose output"; got != want {
		t.Fatalf("delegation complete output = %q, want %q", got, want)
	}
}

func TestLoadSessionRestoresFailedDelegationBoxesFromStructuredResult(t *testing.T) {
	t.Parallel()

	const sessionID = "delegation-structured-failure-session"
	var events []output.Event
	mockStore := newMockSessionStore()
	mockStore.loadedSessions[sessionID] = session.Session{
		ID:    sessionID,
		Title: "Delegation Structured Failure Session",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID: 1,
					Messages: []agent.Message{
						{Role: agent.MessageRoleUser, Content: "verify the fix"},
						{
							Role: agent.MessageRoleAssistant,
							ToolCalls: []agent.ToolCall{
								{ID: "call_sanity_check_1", Name: "sanity_check", Arguments: map[string]any{"task": "verify the fix"}},
							},
						},
						{
							Role:       agent.MessageRoleTool,
							Content:    `{"agent_id":"agent-sanity_check-1","status":"failed","error":"verification failed after tests","turn_count":4,"token_count":789}`,
							ToolCallID: "call_sanity_check_1",
							Name:       "sanity_check",
						},
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
			},
		},
	})

	if err := s.Handle(context.Background(), LoadSession{SessionID: sessionID}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	var delegationFailed []output.DelegationFailedEvent
	for _, event := range events {
		if event.Type != output.EventTypeDelegationFailed {
			continue
		}
		payload, ok := event.Payload.(output.DelegationFailedEvent)
		if !ok {
			t.Fatalf("delegation failed payload type = %T, want output.DelegationFailedEvent", event.Payload)
		}
		delegationFailed = append(delegationFailed, payload)
	}

	if got, want := len(delegationFailed), 1; got != want {
		t.Fatalf("delegation failed events = %d, want %d", got, want)
	}
	if got, want := delegationFailed[0].AgentID, "agent-sanity_check-1"; got != want {
		t.Fatalf("delegation failed agent id = %q, want %q", got, want)
	}
	if got, want := delegationFailed[0].Error, "verification failed after tests"; got != want {
		t.Fatalf("delegation failed error = %q, want %q", got, want)
	}
}

func TestLoadSessionRestoresDelegationBoxesWithMalformedStructuredResultFallback(t *testing.T) {
	t.Parallel()

	const sessionID = "delegation-malformed-result-session"
	var events []output.Event
	mockStore := newMockSessionStore()
	mockStore.loadedSessions[sessionID] = session.Session{
		ID:    sessionID,
		Title: "Delegation Malformed Result Session",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{
				{
					ID: 1,
					Messages: []agent.Message{
						{Role: agent.MessageRoleUser, Content: "research docs"},
						{
							Role: agent.MessageRoleAssistant,
							ToolCalls: []agent.ToolCall{
								{ID: "call_research_1", Name: "research", Arguments: map[string]any{"task": "research docs"}},
							},
						},
						{
							Role:       agent.MessageRoleTool,
							Content:    "{not-json",
							ToolCallID: "call_research_1",
							Name:       "research",
						},
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
			Models: config.ModelsConfig{
				Default: "test",
				Definitions: map[string]config.ModelConfig{
					"test": {ID: "test-model"},
				},
			},
		},
	})

	if err := s.Handle(context.Background(), LoadSession{SessionID: sessionID}); err != nil {
		t.Fatalf("Handle(LoadSession) = %v, want nil", err)
	}

	var toolStarted []output.ToolCallStartedEvent
	var delegationComplete []output.DelegationCompleteEvent
	for _, event := range events {
		switch event.Type {
		case output.EventTypeToolCallStarted:
			payload, ok := event.Payload.(output.ToolCallStartedEvent)
			if !ok {
				t.Fatalf("tool started payload type = %T, want output.ToolCallStartedEvent", event.Payload)
			}
			toolStarted = append(toolStarted, payload)
		case output.EventTypeDelegationComplete:
			payload, ok := event.Payload.(output.DelegationCompleteEvent)
			if !ok {
				t.Fatalf("delegation complete payload type = %T, want output.DelegationCompleteEvent", event.Payload)
			}
			delegationComplete = append(delegationComplete, payload)
		}
	}

	if got, want := len(toolStarted), 1; got != want {
		t.Fatalf("tool started events = %d, want %d", got, want)
	}
	if got, want := len(delegationComplete), 1; got != want {
		t.Fatalf("delegation complete events = %d, want %d", got, want)
	}
	if got, want := delegationComplete[0].AgentID, "agent-call_research_1"; got != want {
		t.Fatalf("delegation complete agent id = %q, want %q", got, want)
	}
	if got, want := delegationComplete[0].Output, "{not-json"; got != want {
		t.Fatalf("delegation complete output = %q, want %q", got, want)
	}
}

func TestSessionModeDefault(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		Config: config.Config{
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
	}
	s := testNewSession(t, deps)
	if got, want := s.Mode(), config.ExecutionModePlan; got != want {
		t.Fatalf("Mode() = %q, want %q", got, want)
	}
}

func TestSessionModeSet(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		Config: config.Config{
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
	}
	var modeChangedEvents []output.ModeChangedEvent
	deps.BaseEvents = output.SinkFunc(func(e output.Event) {
		if e.Type == output.EventTypeModeChanged {
			if payload, ok := e.Payload.(output.ModeChangedEvent); ok {
				modeChangedEvents = append(modeChangedEvents, payload)
			}
		}
	})
	s, err := NewSession(deps)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	s.SetMode(config.ExecutionModeBuild)
	if got, want := s.Mode(), config.ExecutionModeBuild; got != want {
		t.Fatalf("Mode() after SetMode = %q, want %q", got, want)
	}
	if len(modeChangedEvents) == 0 {
		t.Fatal("expected mode-changed event on SetMode")
	}
	if got, want := modeChangedEvents[0].Mode, string(config.ExecutionModeBuild); got != want {
		t.Fatalf("mode-changed event mode = %q, want %q", got, want)
	}
}

func TestSessionModeSetNoOp(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		Config: config.Config{
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
	}
	var modeChangedEvents int
	deps.BaseEvents = output.SinkFunc(func(e output.Event) {
		if e.Type == output.EventTypeModeChanged {
			modeChangedEvents++
		}
	})
	s, err := NewSession(deps)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}

	s.SetMode(config.ExecutionModePlan)
	if modeChangedEvents != 0 {
		t.Fatalf("expected no mode-changed event on SetMode to same mode, got %d", modeChangedEvents)
	}
}

func TestSessionModeListener(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		Config: config.Config{
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
	}
	s := testNewSession(t, deps)

	var got []config.ExecutionMode
	s.SetModeListener(func(m config.ExecutionMode) { got = append(got, m) })

	s.SetMode(config.ExecutionModeBuild)
	if want := []config.ExecutionMode{config.ExecutionModeBuild}; !reflect.DeepEqual(got, want) {
		t.Fatalf("listener calls = %v, want %v", got, want)
	}

	s.SetMode(config.ExecutionModePlan)
	if want := []config.ExecutionMode{config.ExecutionModeBuild, config.ExecutionModePlan}; !reflect.DeepEqual(got, want) {
		t.Fatalf("listener calls = %v, want %v", got, want)
	}

	// No-op SetMode to the current mode must not notify the listener.
	s.SetMode(config.ExecutionModePlan)
	if want := []config.ExecutionMode{config.ExecutionModeBuild, config.ExecutionModePlan}; !reflect.DeepEqual(got, want) {
		t.Fatalf("listener calls after no-op = %v, want %v", got, want)
	}

	// Replacing the listener takes effect for subsequent changes.
	s.SetModeListener(nil)
	s.SetMode(config.ExecutionModeBuild)
	if want := []config.ExecutionMode{config.ExecutionModeBuild, config.ExecutionModePlan}; !reflect.DeepEqual(got, want) {
		t.Fatalf("listener calls after replacement = %v, want %v", got, want)
	}
}

func TestModeNoticeStickyBothModes(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name        string
		defaultMode config.ExecutionMode
	}{
		{name: "plan", defaultMode: config.ExecutionModePlan},
		{name: "build", defaultMode: config.ExecutionModeBuild},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			deps := Dependencies{
				Config: config.Config{
					Modes: config.ModesConfig{
						Default: tc.defaultMode,
					},
				},
			}
			s := testNewSession(t, deps)
			want := prompt.ModeNotice(tc.defaultMode) + "\n\n"
			if notice := s.modeNotice(); notice != want {
				t.Errorf("modeNotice() = %q, want %q", notice, want)
			}
			// The notice never clears: a second call returns it again.
			if notice := s.modeNotice(); notice != want {
				t.Errorf("modeNotice() second call = %q, want %q (notice must not clear)", notice, want)
			}
		})
	}
}

func TestModeNoticeStickyBuildModeRunner(t *testing.T) {
	t.Parallel()
	// A fresh build-mode session announces the mode on its first turn: the
	// conversation sent to the runner starts with the build notice and the
	// stored conversation retains it.
	capturedConversations := make([][]agent.Message, 0, 1)

	deps := Dependencies{
		Config: config.Config{
			Modes: config.ModesConfig{
				Default: config.ExecutionModeBuild,
			},
		},
		Runner: newRunExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
			capturedConversations = append(capturedConversations, cloneMessages(conversation))
			result := append(append([]agent.Message(nil), conversation...), agent.Message{
				Role:    agent.MessageRoleAssistant,
				Content: "response",
			})
			return RunResult{Conversation: result}, nil
		}),
	}
	s := testNewSession(t, deps)

	s.submitPrompt(context.Background(), "first prompt", nil)

	if len(capturedConversations) != 1 {
		t.Fatalf("expected 1 captured conversation, got %d", len(capturedConversations))
	}
	buildNotice := prompt.ModeNotice(config.ExecutionModeBuild)
	first := capturedConversations[0][0]
	if first.Role != agent.MessageRoleUser {
		t.Fatalf("first message role = %s, want user", first.Role)
	}
	if !strings.HasPrefix(first.Content, buildNotice) {
		t.Fatalf("first submitted user message does not start with build notice; got %q", first.Content)
	}

	storedConv := s.Conversation()
	foundNoticeInStored := false
	for _, msg := range storedConv {
		if msg.Role == agent.MessageRoleUser && strings.Contains(msg.Content, "[execution mode: build]") {
			foundNoticeInStored = true
			break
		}
	}
	if !foundNoticeInStored {
		t.Fatal("stored conversation does not contain mode notice; notice is not retained")
	}
}

func TestSessionSwitchModeAction(t *testing.T) {
	t.Parallel()
	deps := Dependencies{
		Config: config.Config{
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
	}
	s := testNewSession(t, deps)

	if err := s.Handle(context.Background(), SwitchMode{Mode: config.ExecutionModeBuild}); err != nil {
		t.Fatalf("Handle(SwitchMode) = %v, want nil", err)
	}
	if got, want := s.Mode(), config.ExecutionModeBuild; got != want {
		t.Fatalf("Mode() after SwitchMode action = %q, want %q", got, want)
	}
}

func TestModeNoticeStickinessPlanMode(t *testing.T) {
	t.Parallel()
	// Test #2 & #3: Plan mode notice is sticky (appears every turn) and retained in storage.
	// Captures what's sent to the runner and verifies notice presence at turn opening.
	capturedConversations := make([][]agent.Message, 0, 2)

	deps := Dependencies{
		Config: config.Config{
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
		Runner: newRunExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
			// Capture the conversation sent by this turn
			capturedConversations = append(capturedConversations, cloneMessages(conversation))
			// Return what was sent plus an assistant message (echo pattern)
			result := append(append([]agent.Message(nil), conversation...), agent.Message{
				Role:    agent.MessageRoleAssistant,
				Content: "response",
			})
			return RunResult{Conversation: result}, nil
		}),
	}
	s := testNewSession(t, deps)

	// Turn 1: submit first prompt
	s.submitPrompt(context.Background(), "first prompt", nil)

	// Turn 2: submit second prompt
	s.submitPrompt(context.Background(), "second prompt", nil)

	if len(capturedConversations) != 2 {
		t.Fatalf("expected 2 captured conversations, got %d", len(capturedConversations))
	}

	// Verify test #2: Each turn-opening user message carries the notice.
	// Turn 1: captured[0] should have notice on first user message
	turn1First := capturedConversations[0][0]
	planNotice := prompt.ModeNotice(config.ExecutionModePlan)
	if turn1First.Role != agent.MessageRoleUser {
		t.Fatalf("turn 1 first message role = %s, want user", turn1First.Role)
	}
	if !strings.HasPrefix(turn1First.Content, planNotice) {
		t.Fatalf("turn 1 user message does not start with plan notice; got %q", turn1First.Content)
	}

	// Turn 2: captured[1] should have notice on the user message that opens this turn.
	// After turn 1, there are: user + assistant. Turn 2 appends a user message.
	// So the opening user message for turn 2 is at the end (the newly appended one).
	turn2OpeningUser := capturedConversations[1][len(capturedConversations[1])-1]
	if turn2OpeningUser.Role != agent.MessageRoleUser {
		t.Fatalf("turn 2 opening message role = %s, want user", turn2OpeningUser.Role)
	}
	if !strings.HasPrefix(turn2OpeningUser.Content, planNotice) {
		t.Fatalf("turn 2 opening user message does not start with plan notice; got %q", turn2OpeningUser.Content)
	}

	// Verify test #3: Stored conversation retains the notice.
	storedConv := s.Conversation()
	foundNoticeInStored := false
	for _, msg := range storedConv {
		if msg.Role == agent.MessageRoleUser && strings.Contains(msg.Content, "[execution mode: plan]") {
			foundNoticeInStored = true
			break
		}
	}
	if !foundNoticeInStored {
		t.Fatal("stored conversation does not contain mode notice; notice is not retained")
	}
}

func TestCacheByteIdentity(t *testing.T) {
	t.Parallel()
	// Test #4: Turn N+1's sent message slice up through userN is byte-identical to
	// what turn N sent. This verifies prompt-cache integrity: messages behind the
	// cache breakpoint must never be mutated between turns. Plan mode is the case
	// that matters here, since it injects a per-turn notice that the old
	// strip-from-stored-conversation code used to mutate after the fact.
	var sentConversations [][]agent.Message

	deps := Dependencies{
		Config: config.Config{
			Modes: config.ModesConfig{
				Default: config.ExecutionModePlan,
			},
		},
		Runner: newRunExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
			sentConversations = append(sentConversations, cloneMessages(conversation))
			// Echo back the conversation plus an assistant message
			result := append(append([]agent.Message(nil), conversation...), agent.Message{
				Role:    agent.MessageRoleAssistant,
				Content: "response",
			})
			return RunResult{Conversation: result}, nil
		}),
	}
	s := testNewSession(t, deps)

	// Turn 1
	s.submitPrompt(context.Background(), "first", nil)

	// Turn 2
	s.submitPrompt(context.Background(), "second", nil)

	if len(sentConversations) != 2 {
		t.Fatalf("expected 2 sent conversations, got %d", len(sentConversations))
	}
	turn1Sent := sentConversations[0]
	turn2Sent := sentConversations[1]

	if len(turn2Sent) < len(turn1Sent) {
		t.Fatalf("turn 2 sent %d messages, shorter than turn 1's %d", len(turn2Sent), len(turn1Sent))
	}

	turn2Prefix := turn2Sent[:len(turn1Sent)]
	if !reflect.DeepEqual(turn2Prefix, turn1Sent) {
		t.Fatalf("turn 2's prefix is not byte-identical to what turn 1 sent; cached messages were mutated\nturn1Sent=%#v\nturn2Prefix=%#v", turn1Sent, turn2Prefix)
	}
}
