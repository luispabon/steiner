package interactive

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
)

func TestIsDelegateToolCall(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		want     bool
	}{
		{name: "delegate is no longer a delegate tool", toolName: "delegate", want: false},
		{name: "explore is delegate tool", toolName: "explore", want: true},
		{name: "research is delegate tool", toolName: "research", want: true},
		{name: "code is delegate tool", toolName: "code", want: true},
		{name: "evaluate is delegate tool", toolName: "evaluate", want: true},
		{name: "sanity_check is delegate tool", toolName: "sanity_check", want: true},
		{name: "follow_up is delegate tool", toolName: "follow_up", want: true},
		{name: "read is not delegate tool", toolName: "read", want: false},
		{name: "mutate is not delegate tool", toolName: "mutate", want: false},
		{name: "bash is not delegate tool", toolName: "bash", want: false},
		{name: "delegate uppercase is no longer a delegate tool", toolName: "DELEGATE", want: false},
		{name: "explore uppercase", toolName: "EXPLORE", want: true},
		{name: "follow_up uppercase", toolName: "FOLLOW_UP", want: true},
		{name: "empty string is not delegate tool", toolName: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDelegateToolCall(tt.toolName)
			if got != tt.want {
				t.Errorf("isDelegateToolCall(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestIsDelegateToolCallAllSpecializedTools(t *testing.T) {
	allSpecializedTools := delegation.AllSpecializedDelegateTools()
	for _, toolName := range allSpecializedTools {
		if !isDelegateToolCall(toolName) {
			t.Errorf("isDelegateToolCall(%q) = false, want true (from AllSpecializedDelegateTools)", toolName)
		}
	}
}

func TestReplaySessionMessagesSummaryRole(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})

	msgs := []agent.Message{
		{Role: agent.MessageRoleSummary, Content: "compaction summary text"},
		{Role: agent.MessageRoleUser, Content: "hello"},
		{Role: agent.MessageRoleAssistant, Content: "hi"},
	}
	s.replaySessionMessages(msgs)

	var foundSummary bool
	for _, e := range events {
		if e.Type != output.EventTypeContextDiagnostics {
			continue
		}
		if compaction, ok := output.AsContextCompactionEvent(e.Payload); ok {
			if compaction.SummaryText == "compaction summary text" {
				foundSummary = true
			}
		}
	}
	if !foundSummary {
		t.Fatal("summary message not replayed as compaction diagnostics event")
	}
}

func TestReplaySessionMessagesAllRoles(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})

	msgs := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "hello"},
		{
			Role:      agent.MessageRoleAssistant,
			Content:   "hi",
			ToolCalls: []agent.ToolCall{{ID: "call-1", Name: "read", Arguments: map[string]any{"path": "foo"}}},
		},
		{Role: agent.MessageRoleTool, ToolCallID: "call-1", Name: "read", Content: "file contents"},
		{Role: agent.MessageRoleSummary, Content: "compaction summary text"},
	}
	s.replaySessionMessages(msgs)

	if len(events) != 5 {
		t.Fatalf("got %d events, want 5: %+v", len(events), events)
	}

	userEvent, ok := events[0].Payload.(output.UserInputEvent)
	if !ok {
		t.Fatalf("events[0] payload = %T, want output.UserInputEvent", events[0].Payload)
	}
	if userEvent.Content != "hello" || userEvent.Mode != "resume" {
		t.Errorf("userEvent = %+v, want Content=hello Mode=resume", userEvent)
	}

	assistantEvent, ok := events[1].Payload.(output.AssistantMessageEvent)
	if !ok {
		t.Fatalf("events[1] payload = %T, want output.AssistantMessageEvent", events[1].Payload)
	}
	if assistantEvent.Role != string(agent.MessageRoleAssistant) || assistantEvent.Content != "hi" {
		t.Errorf("assistantEvent = %+v, want Role=assistant Content=hi", assistantEvent)
	}

	startedEvent, ok := events[2].Payload.(output.ToolCallStartedEvent)
	if !ok {
		t.Fatalf("events[2] payload = %T, want output.ToolCallStartedEvent", events[2].Payload)
	}
	if startedEvent.Tool != "read" || startedEvent.CallID != "call-1" || startedEvent.Arguments["path"] != "foo" {
		t.Errorf("startedEvent = %+v, want Tool=read CallID=call-1 Arguments[path]=foo", startedEvent)
	}

	finishedEvent, ok := events[3].Payload.(output.ToolCallFinishedEvent)
	if !ok {
		t.Fatalf("events[3] payload = %T, want output.ToolCallFinishedEvent", events[3].Payload)
	}
	if finishedEvent.Tool != "read" || finishedEvent.CallID != "call-1" || finishedEvent.Result != "file contents" {
		t.Errorf("finishedEvent = %+v, want Tool=read CallID=call-1 Result='file contents'", finishedEvent)
	}

	if events[4].Type != output.EventTypeContextDiagnostics {
		t.Fatalf("events[4].Type = %q, want %q", events[4].Type, output.EventTypeContextDiagnostics)
	}
	compaction, ok := output.AsContextCompactionEvent(events[4].Payload)
	if !ok || compaction.SummaryText != "compaction summary text" {
		t.Errorf("compaction = %+v, ok=%v, want SummaryText='compaction summary text'", compaction, ok)
	}
}

func TestReplaySessionMessagesOrphanedToolCallSkipped(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})

	msgs := []agent.Message{
		// Orphaned tool call: no matching tool result follows, so it must not
		// emit a started event (e.g. an accepted workflow_handoff that stops
		// the run before appending a result).
		{
			Role:      agent.MessageRoleAssistant,
			Content:   "acting",
			ToolCalls: []agent.ToolCall{{ID: "call-orphan", Name: "read"}},
		},
		// Orphaned tool result: no assistant tool call started this ID, so it
		// must not emit a finished event and must not corrupt subsequent replay.
		{Role: agent.MessageRoleTool, ToolCallID: "call-other", Name: "read", Content: "unmatched result"},
		{
			Role:      agent.MessageRoleAssistant,
			Content:   "acting again",
			ToolCalls: []agent.ToolCall{{ID: "call-2", Name: "read"}},
		},
		{Role: agent.MessageRoleTool, ToolCallID: "call-2", Name: "read", Content: "second result"},
	}
	s.replaySessionMessages(msgs)

	var toolCallIDs []string
	for _, e := range events {
		switch p := e.Payload.(type) {
		case output.ToolCallStartedEvent:
			toolCallIDs = append(toolCallIDs, "started:"+p.CallID)
		case output.ToolCallFinishedEvent:
			toolCallIDs = append(toolCallIDs, "finished:"+p.CallID)
		}
	}

	want := []string{"started:call-2", "finished:call-2"}
	if len(toolCallIDs) != len(want) {
		t.Fatalf("tool call events = %v, want %v", toolCallIDs, want)
	}
	for i, id := range want {
		if toolCallIDs[i] != id {
			t.Errorf("tool call events[%d] = %q, want %q", i, toolCallIDs[i], id)
		}
	}
}

func TestReplaySessionMessagesDelegateEvent(t *testing.T) {
	t.Parallel()

	t.Run("successful delegation", func(t *testing.T) {
		t.Parallel()
		var events []output.Event
		s := testNewSession(t, Dependencies{
			BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
		})

		msgs := []agent.Message{
			{
				Role:    agent.MessageRoleAssistant,
				Content: "delegating",
				ToolCalls: []agent.ToolCall{
					{ID: "call-d1", Name: "explore", Arguments: map[string]any{"task": "investigate bug"}},
				},
			},
			{
				Role:       agent.MessageRoleTool,
				ToolCallID: "call-d1",
				Name:       "explore",
				Content:    `{"agent_id":"agent-99","status":"complete","output":"found it","turn_count":2,"token_count":50,"tool_call_count":1}`,
			},
		}
		s.replaySessionMessages(msgs)

		var started *output.DelegationStartedEvent
		var complete *output.DelegationCompleteEvent
		for i := range events {
			switch p := events[i].Payload.(type) {
			case output.DelegationStartedEvent:
				started = &p
			case output.DelegationCompleteEvent:
				complete = &p
			}
		}

		if started == nil {
			t.Fatal("expected a DelegationStartedEvent")
		}
		if started.AgentID != "agent-99" || started.TaskPreview != "investigate bug" {
			t.Errorf("started = %+v, want AgentID=agent-99 TaskPreview='investigate bug'", started)
		}

		if complete == nil {
			t.Fatal("expected a DelegationCompleteEvent")
		}
		if complete.AgentID != "agent-99" || complete.Status != "complete" || complete.TurnCount != 2 ||
			complete.TokenCount != 50 || complete.ToolCallCount != 1 || complete.Output != "found it" {
			t.Errorf("complete = %+v, want agent-99/complete/2/50/1/'found it'", complete)
		}
	})

	t.Run("failed delegation", func(t *testing.T) {
		t.Parallel()
		var events []output.Event
		s := testNewSession(t, Dependencies{
			BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
		})

		msgs := []agent.Message{
			{
				Role:    agent.MessageRoleAssistant,
				Content: "delegating",
				ToolCalls: []agent.ToolCall{
					{ID: "call-d2", Name: "explore", Arguments: map[string]any{"task": "investigate other bug"}},
				},
			},
			{
				Role:       agent.MessageRoleTool,
				ToolCallID: "call-d2",
				Name:       "explore",
				Content:    `{"agent_id":"agent-100","status":"failed","error":"delegate crashed"}`,
			},
		}
		s.replaySessionMessages(msgs)

		var failed *output.DelegationFailedEvent
		for i := range events {
			if p, ok := events[i].Payload.(output.DelegationFailedEvent); ok {
				failed = &p
			}
		}

		if failed == nil {
			t.Fatal("expected a DelegationFailedEvent")
		}
		if failed.AgentID != "agent-100" || failed.Error != "delegate crashed" {
			t.Errorf("failed = %+v, want AgentID=agent-100 Error='delegate crashed'", failed)
		}
	})
}

func TestIsDelegateToolCallCaseInsensitive(t *testing.T) {
	testCases := delegation.AllSpecializedDelegateTools()

	for _, toolName := range testCases {
		lower := strings.ToLower(toolName)
		upper := strings.ToUpper(toolName)
		mixed := strings.ToUpper(toolName[:1]) + strings.ToLower(toolName[1:])

		if !isDelegateToolCall(lower) {
			t.Errorf("isDelegateToolCall(%q) = false, want true", lower)
		}
		if !isDelegateToolCall(upper) {
			t.Errorf("isDelegateToolCall(%q) = false, want true", upper)
		}
		if !isDelegateToolCall(mixed) {
			t.Errorf("isDelegateToolCall(%q) = false, want true", mixed)
		}
	}
}
