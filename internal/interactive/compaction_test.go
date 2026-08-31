package interactive

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

func TestRunManualCompactionEmitsLifecycleAndClearsControllerOnSuccess(t *testing.T) {
	var events []output.Event
	s := mustCompactionSession(t, Dependencies{BaseEvents: output.SinkFunc(func(event output.Event) { events = append(events, event) })})
	ctrl := s.ActiveRunController()
	result, err := s.runManualCompaction(context.Background(), "test-model", func(context.Context) ([]agent.Message, error) {
		s.events.Emit(output.NewAssistantChunkEventWithSource(1, "chunk", output.ChunkSourceAssistant))
		return []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, nil
	})
	if err != nil || len(result) != 1 {
		t.Fatalf("runManualCompaction() = %#v, %v", result, err)
	}
	if ctrl.HasCancel() {
		t.Fatal("run controller was not cleared")
	}
	if got, want := eventTypes(events), []string{output.EventTypeRunStarted, output.EventTypeContextDiagnostics, output.EventTypeAssistantChunk, output.EventTypeRunFinished}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	started := events[0].Payload.(output.RunStartedEvent)
	if started.Mode != "interactive" || started.Model != "test-model" {
		t.Fatalf("run started = %+v", started)
	}
	finished := events[len(events)-1].Payload.(output.RunFinishedEvent)
	if finished.Reason != "complete" || finished.Error != "" {
		t.Fatalf("run finished = %+v", finished)
	}
}

func TestRunManualCompactionEmitsRunFinishedAndClearsControllerOnError(t *testing.T) {
	var events []output.Event
	s := mustCompactionSession(t, Dependencies{BaseEvents: output.SinkFunc(func(event output.Event) { events = append(events, event) })})
	ctrl := s.ActiveRunController()
	_, err := s.runManualCompaction(context.Background(), "test-model", func(context.Context) ([]agent.Message, error) { return nil, errors.New("boom") })
	if err == nil || err.Error() != "boom" {
		t.Fatalf("error = %v, want boom", err)
	}
	if ctrl.HasCancel() {
		t.Fatal("run controller was not cleared")
	}
	finished := events[len(events)-1].Payload.(output.RunFinishedEvent)
	if finished.Reason != "error" || finished.Error != "boom" {
		t.Fatalf("run finished = %+v", finished)
	}
}

func TestRunManualCompactionCancelsAndClearsController(t *testing.T) {
	s := mustCompactionSession(t, Dependencies{})
	ctrl := s.ActiveRunController()
	started := make(chan struct{})
	go func() { <-started; ctrl.Interrupt() }()
	_, err := s.runManualCompaction(context.Background(), "test-model", func(ctx context.Context) ([]agent.Message, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if ctrl.HasCancel() {
		t.Fatal("run controller was not cleared")
	}
}

func TestManualCompactionSkipsSingleTurnConversation(t *testing.T) {
	var events []output.Event
	s := mustCompactionSession(t, Dependencies{BaseEvents: output.SinkFunc(func(event output.Event) { events = append(events, event) })})
	s.SetConversation([]agent.Message{{Role: agent.MessageRoleUser, Content: "request"}, {Role: agent.MessageRoleAssistant, Content: "answer"}})
	s.manualCompaction(context.Background())
	if len(events) != 1 || events[0].Type != output.EventTypeContextReport {
		t.Fatalf("events = %#v, want one context report", events)
	}
}

func TestManualCompactionHasSourceUsesAssistantCycles(t *testing.T) {
	assistantToolCycle := []agent.Message{
		{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call_1", Name: "read"}}},
		{Role: agent.MessageRoleTool, ToolCallID: "call_1"},
	}
	tests := []struct {
		name       string
		messages   []agent.Message
		wantSource bool
	}{
		{name: "empty", wantSource: false},
		{name: "lone user", messages: []agent.Message{{Role: agent.MessageRoleUser}}, wantSource: false},
		{name: "user and assistant", messages: []agent.Message{{Role: agent.MessageRoleUser}, {Role: agent.MessageRoleAssistant}}, wantSource: false},
		{name: "user and tool", messages: []agent.Message{{Role: agent.MessageRoleUser}, {Role: agent.MessageRoleTool}}, wantSource: false},
		{name: "one assistant tool cycle", messages: assistantToolCycle, wantSource: false},
		{
			name: "one user with multiple assistant tool cycles",
			messages: append([]agent.Message{{Role: agent.MessageRoleUser}}, append(assistantToolCycle, []agent.Message{
				{Role: agent.MessageRoleAssistant, Content: "final"},
			}...)...),
			wantSource: true,
		},
		{
			name:       "two user turns",
			messages:   twoTurnConversation(),
			wantSource: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := manualCompactionHasSource(tt.messages); got != tt.wantSource {
				t.Fatalf("manualCompactionHasSource() = %v, want %v", got, tt.wantSource)
			}
		})
	}
}

func TestManualCompactionAllowsMultipleAssistantCyclesInSingleTurn(t *testing.T) {
	var gotConversation []agent.Message
	runner := &runExecutorFunc{compact: func(_ context.Context, conversation []agent.Message, _ []string, _ []provider.ToolSpec) ([]agent.Message, error) {
		gotConversation = cloneMessages(conversation)
		return []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, nil
	}}
	s := mustCompactionSession(t, Dependencies{Runner: runner})
	s.SetConversation([]agent.Message{
		{Role: agent.MessageRoleUser, Content: "request"},
		{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "call_1", Name: "read"}}},
		{Role: agent.MessageRoleTool, ToolCallID: "call_1", Content: "result"},
		{Role: agent.MessageRoleAssistant, Content: "final"},
	})

	s.manualCompaction(context.Background())
	if got, want := len(gotConversation), 4; got != want {
		t.Fatalf("Compact conversation length = %d, want %d", got, want)
	}
	if got, want := s.Conversation()[0].Content, "summary"; got != want {
		t.Fatalf("conversation[0] = %q, want %q", got, want)
	}
}

func TestManualCompactionUsesRunnerCompact(t *testing.T) {
	var gotConversation []agent.Message
	var gotSkills []string
	runner := &runExecutorFunc{compact: func(_ context.Context, conversation []agent.Message, skills []string, _ []provider.ToolSpec) ([]agent.Message, error) {
		gotConversation = cloneMessages(conversation)
		gotSkills = append([]string(nil), skills...)
		return []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, nil
	}}
	s := mustCompactionSession(t, Dependencies{Runner: runner, SkillNames: []string{"skill-a"}})
	s.Skills().Set("skill-a", true)
	s.SetConversation(twoTurnConversation())
	s.manualCompaction(context.Background(), "focus on auth")
	if got, want := runner.compactSteering, "focus on auth"; got != want {
		t.Fatalf("Compact steering = %q, want %q", got, want)
	}
	if len(gotConversation) != 4 {
		t.Fatalf("Compact conversation length = %d, want 4", len(gotConversation))
	}
	if !reflect.DeepEqual(gotSkills, []string{"skill-a"}) {
		t.Fatalf("Compact skills = %v", gotSkills)
	}
	if s.Conversation()[0].Content != "summary" {
		t.Fatal("conversation was not replaced by compact result")
	}
}

func TestHandleManualCompactionPassesSteeringToRunner(t *testing.T) {
	const marker = "focus on auth handoff"
	runner := &runExecutorFunc{}
	s := mustCompactionSession(t, Dependencies{Runner: runner})
	s.SetConversation(twoTurnConversation())

	if err := s.Handle(context.Background(), TriggerManualCompaction{Steering: marker}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if !s.WaitRuns(context.Background()) {
		t.Fatal("WaitRuns() returned false, want compaction run to finish")
	}
	if got := runner.compactSteering; got != marker {
		t.Fatalf("Compact steering = %q, want %q", got, marker)
	}
}

func TestManualCompactionPassesSnapshotToolsToRunner(t *testing.T) {
	var gotTools []provider.ToolSpec
	runner := &runExecutorFunc{compact: func(_ context.Context, _ []agent.Message, _ []string, tools []provider.ToolSpec) ([]agent.Message, error) {
		gotTools = provider.CloneTools(tools)
		return []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, nil
	}}
	s := mustCompactionSession(t, Dependencies{Runner: runner})
	s.SnapshotStore().Store(RequestContextSnapshot{Tools: []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "read"}}}})
	s.SetConversation(twoTurnConversation())
	s.manualCompaction(context.Background())
	if len(gotTools) != 1 || gotTools[0].Function.Name != "read" {
		t.Fatalf("Compact tools = %#v", gotTools)
	}
}

func TestManualCompactionPersistsCompactSessionWithoutFollowupPrompt(t *testing.T) {
	const modelID = "openrouter/test-model"
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conversation := twoTurnConversation()
	lineage := agent.ConversationLineage{Generations: []agent.ConversationGeneration{{ID: 1, Messages: cloneMessages(conversation)}}, NextGenerationID: 2}
	initial, err := session.NewSession(modelID, lineage)
	if err != nil {
		t.Fatal(err)
	}
	initial.ID, initial.Title = "persisted", "Persist me"
	if err := store.Save(initial); err != nil {
		t.Fatal(err)
	}
	runner := &runExecutorFunc{compact: func(context.Context, []agent.Message, []string, []provider.ToolSpec) ([]agent.Message, error) {
		return []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, nil
	}}
	s := mustCompactionSession(t, Dependencies{Runner: runner, SessionStore: store, Config: config.Config{Models: config.ModelsConfig{Effective: config.EffectiveModelAssignments{DefaultModel: "test"}, Definitions: map[string]config.ModelConfig{"test": {ID: modelID}}}}})
	s.mu.Lock()
	s.sessionID, s.sessionTitle, s.lineage, s.conversation = initial.ID, initial.Title, lineage, cloneMessages(conversation)
	s.mu.Unlock()
	s.manualCompaction(context.Background())
	loaded, err := store.Load(initial.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != initial.ID || loaded.Model != modelID || loaded.Title != initial.Title {
		t.Fatalf("loaded session = %+v", loaded)
	}
	if !reflect.DeepEqual(loaded.Lineage.FullMessages(), s.Conversation()) {
		t.Fatal("persisted conversation differs")
	}
}

func mustCompactionSession(t *testing.T, deps Dependencies) *Session {
	t.Helper()
	s, err := NewSession(deps)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func twoTurnConversation() []agent.Message {
	return []agent.Message{{Role: agent.MessageRoleUser, Content: "first"}, {Role: agent.MessageRoleAssistant, Content: "answer"}, {Role: agent.MessageRoleUser, Content: "second"}, {Role: agent.MessageRoleAssistant, Content: "answer"}}
}
func eventTypes(events []output.Event) []string {
	types := make([]string, len(events))
	for i, event := range events {
		types[i] = event.Type
	}
	return types
}
