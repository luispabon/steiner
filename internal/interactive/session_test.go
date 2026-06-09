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

func TestSubmitPromptAppendsUserMessage(t *testing.T) {
	t.Parallel()
	s := testNewSession(t, Dependencies{
		Runner: runExecutorFunc(func(_ context.Context, conversation []agent.Message, _ []string) (RunResult, error) {
			return RunResult{Conversation: conversation}, nil
		}),
	})

	s.submitPrompt(context.Background(), "hello")

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

	s.submitPrompt(context.Background(), "hello")

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

	s.submitPrompt(context.Background(), "hello")

	if got := s.Conversation(); len(got) != 2 {
		t.Fatalf("conversation length = %d, want 2", len(got))
	}
}

func TestSubmitPromptSkipsConversationPersistenceOnWorkflowHandoff(t *testing.T) {
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

	s.submitPrompt(context.Background(), "hello")

	if got := s.Conversation(); len(got) != 1 || got[0].Role != agent.MessageRoleUser {
		t.Fatalf("conversation after handoff = %#v, want only the submitted user prompt retained until clear", got)
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

	s.submitPrompt(context.Background(), "hello")

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

	s.submitPrompt(context.Background(), "hello")

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

	s.submitPrompt(context.Background(), "hello")

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

	s.submitPrompt(context.Background(), "hey")

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
	for _, event := range events {
		if event.Type == output.EventTypeAssistantMessage {
			assistantEvents++
		}
	}
	if got, want := assistantEvents, 2; got != want {
		t.Fatalf("assistant message events = %d, want %d for tool-call transcript display", got, want)
	}
}
