package interactive

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

func TestIsDelegateToolCall(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		want     bool
	}{
		{name: "delegate is no longer a delegate tool", toolName: "delegate", want: false},
		{name: "explore is not a delegate tool anymore", toolName: "explore", want: false},
		{name: "research is not a delegate tool anymore", toolName: "research", want: false},
		{name: "code is not a delegate tool anymore", toolName: "code", want: false},
		{name: "evaluate is not a delegate tool anymore", toolName: "evaluate", want: false},
		{name: "sanity_check is not a delegate tool anymore", toolName: "sanity_check", want: false},
		{name: "sub_agent is delegate tool", toolName: "sub_agent", want: true},
		{name: "follow_up is delegate tool", toolName: "follow_up", want: true},
		{name: "read is not delegate tool", toolName: "read", want: false},
		{name: "mutate is not delegate tool", toolName: "mutate", want: false},
		{name: "bash is not delegate tool", toolName: "bash", want: false},
		{name: "delegate uppercase is no longer a delegate tool", toolName: "DELEGATE", want: false},
		{name: "sub_agent uppercase", toolName: "SUB_AGENT", want: true},
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
					{ID: "call-d1", Name: "sub_agent", Arguments: map[string]any{"type": "explore", "task": "investigate bug"}},
				},
			},
			{
				Role:       agent.MessageRoleTool,
				ToolCallID: "call-d1",
				Name:       "sub_agent",
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
					{ID: "call-d2", Name: "sub_agent", Arguments: map[string]any{"type": "explore", "task": "investigate other bug"}},
				},
			},
			{
				Role:       agent.MessageRoleTool,
				ToolCallID: "call-d2",
				Name:       "sub_agent",
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

func TestReplaySessionMessagesAdvisorEvent(t *testing.T) {
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
				{ID: "call-a1", Name: "advisor", Arguments: map[string]any{"question": "should I refactor this?", "files": []any{"a.go", "b.go"}}},
			},
		},
		{
			Role:       agent.MessageRoleTool,
			ToolCallID: "call-a1",
			Name:       "advisor",
			Content:    "yes, refactor it",
		},
	}
	s.replaySessionMessages(msgs)

	var started *output.AdvisorStartedEvent
	var complete *output.AdvisorCompleteEvent
	for i := range events {
		switch p := events[i].Payload.(type) {
		case output.AdvisorStartedEvent:
			started = &p
		case output.AdvisorCompleteEvent:
			complete = &p
		}
	}

	if started == nil {
		t.Fatal("expected a AdvisorStartedEvent")
	}
	if started.Question != "should I refactor this?" || len(started.Files) != 2 || started.Files[0] != "a.go" || started.Files[1] != "b.go" {
		t.Errorf("started = %+v, want Question='should I refactor this?' Files=['a.go','b.go']", started)
	}

	if complete == nil {
		t.Fatal("expected a AdvisorCompleteEvent")
	}
	if complete.Note != "yes, refactor it" {
		t.Errorf("complete = %+v, want Note='yes, refactor it'", complete)
	}
}

func TestReplaySessionMessagesDisplayFileEvent(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})

	msgs := []agent.Message{
		{
			Role:      agent.MessageRoleAssistant,
			Content:   "showing file",
			ToolCalls: []agent.ToolCall{{ID: "call-df1", Name: "display_file", Arguments: map[string]any{"path": "foo.go"}}},
		},
		{
			Role:       agent.MessageRoleTool,
			ToolCallID: "call-df1",
			Name:       "display_file",
			Content:    `{"path":"foo.go","status":"displayed"}`,
		},
	}
	s.replaySessionMessages(msgs)

	var found bool
	for _, e := range events {
		if e.Type != output.EventTypeDisplayFile {
			continue
		}
		if payload, ok := e.Payload.(output.DisplayFilePayload); ok && payload.Path == "foo.go" && payload.Preview.Kind == output.PreviewFormatKindFile {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("display_file event not replayed or missing expected payload")
	}
}

func TestReplaySessionMessagesDisplayFileCallsAlwaysFinish(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})

	msgs := []agent.Message{
		{
			Role:    agent.MessageRoleAssistant,
			Content: "showing files",
			ToolCalls: []agent.ToolCall{
				{ID: "call-valid", Name: "display_file", Arguments: map[string]any{"path": "valid.go"}},
				{ID: "call-bad", Name: "display_file", Arguments: map[string]any{"path": "bad.go"}},
			},
		},
		{
			Role:       agent.MessageRoleTool,
			ToolCallID: "call-valid",
			Name:       "display_file",
			Content:    `{"path":"valid.go","message":"file content"}`,
		},
		{
			Role:       agent.MessageRoleTool,
			ToolCallID: "call-bad",
			Name:       "display_file",
			Content:    `{malformed json}`,
		},
	}
	s.replaySessionMessages(msgs)

	// Collect Started/Finished events for each tool call.
	type callEvent struct {
		id    string
		count int
	}
	callEvents := make(map[string]*callEvent)
	displayFileCount := 0

	for _, e := range events {
		switch p := e.Payload.(type) {
		case output.ToolCallStartedEvent:
			if _, ok := callEvents[p.CallID]; !ok {
				callEvents[p.CallID] = &callEvent{id: p.CallID}
			}
			callEvents[p.CallID].count++
		case output.ToolCallFinishedEvent:
			if _, ok := callEvents[p.CallID]; !ok {
				callEvents[p.CallID] = &callEvent{id: p.CallID}
			}
			callEvents[p.CallID].count++
		case output.DisplayFilePayload:
			displayFileCount++
		}
	}

	// Both tool calls must have Started and Finished events (count = 2 each).
	for _, callID := range []string{"call-valid", "call-bad"} {
		ce, ok := callEvents[callID]
		if !ok {
			t.Errorf("tool call %s: no events found", callID)
			continue
		}
		if ce.count != 2 {
			t.Errorf("tool call %s: got %d events, want 2 (Started + Finished)", callID, ce.count)
		}
	}

	// Only the valid display_file should emit a DisplayFilePayload.
	if displayFileCount != 1 {
		t.Errorf("got %d DisplayFilePayload events, want 1", displayFileCount)
	}
}

func TestReplaySessionMessagesToolCallError(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})

	msgs := []agent.Message{
		{
			Role:      agent.MessageRoleAssistant,
			Content:   "running bash",
			ToolCalls: []agent.ToolCall{{ID: "call-b1", Name: "bash", Arguments: map[string]any{"command": "false"}}},
		},
		{
			Role:       agent.MessageRoleTool,
			ToolCallID: "call-b1",
			Name:       "bash",
			Content:    `{"ok":false,"error":{"kind":"tool_error","message":"boom"}}`,
		},
	}
	s.replaySessionMessages(msgs)

	var finishedEvent *output.ToolCallFinishedEvent
	for _, e := range events {
		if p, ok := e.Payload.(output.ToolCallFinishedEvent); ok && p.CallID == "call-b1" {
			finishedEvent = &p
			break
		}
	}

	if finishedEvent == nil {
		t.Fatal("expected a ToolCallFinishedEvent")
	}
	if finishedEvent.Error == "" || !strings.Contains(finishedEvent.Error, "boom") {
		t.Errorf("finishedEvent.Error = %q, want non-empty with 'boom'", finishedEvent.Error)
	}
}

func TestReplaySessionMessagesImagesAttached(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})

	msgs := []agent.Message{
		{
			Role:    agent.MessageRoleUser,
			Content: "analyze this image",
			Images: []agent.ImageBlock{
				{ID: "img1", FilePath: "/tmp/x.png"},
			},
		},
	}
	s.replaySessionMessages(msgs)

	var found bool
	for _, e := range events {
		if p, ok := e.Payload.(output.UserInputEvent); ok && len(p.Images) > 0 && p.Images[0].FilePath == "/tmp/x.png" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("user input event with images not replayed")
	}
}

func TestReplaySessionMessagesReasoningContent(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})

	msgs := []agent.Message{
		{
			Role:             agent.MessageRoleAssistant,
			Content:          "here's the fix",
			ReasoningContent: "thinking about the bug",
		},
	}
	s.replaySessionMessages(msgs)

	var thinkingEvent *output.ThinkingChunkEvent
	var assistantEvent *output.AssistantMessageEvent
	for _, e := range events {
		switch p := e.Payload.(type) {
		case output.ThinkingChunkEvent:
			thinkingEvent = &p
		case output.AssistantMessageEvent:
			assistantEvent = &p
		}
	}

	if thinkingEvent == nil {
		t.Fatal("expected a ThinkingChunkEvent")
	}
	if thinkingEvent.Content != "thinking about the bug" || thinkingEvent.Source != output.ChunkSourceAssistant {
		t.Errorf("thinkingEvent = %+v, want Content='thinking about the bug' Source=ChunkSourceAssistant", thinkingEvent)
	}

	if assistantEvent == nil {
		t.Fatal("expected an AssistantMessageEvent")
	}
	if assistantEvent.Content != "here's the fix" {
		t.Errorf("assistantEvent.Content = %q, want 'here's the fix'", assistantEvent.Content)
	}
}

// replayUserEvents replays msgs and returns the emitted events in order.
func replayUserEvents(t *testing.T, msgs []agent.Message) []output.Event {
	t.Helper()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})
	s.replaySessionMessages(msgs)
	return events
}

func TestReplaySessionMessagesSkillStates(t *testing.T) {
	t.Parallel()

	activation, _ := prompt.RenderSkillActivation("docs", "docs body")
	deactivation := prompt.RenderSkillDeactivation("docs")

	t.Run("activation with text emits state then input", func(t *testing.T) {
		t.Parallel()
		events := replayUserEvents(t, []agent.Message{{
			Role:    agent.MessageRoleUser,
			Content: prompt.PrependSkillBlocks([]string{activation}, "now do the thing"),
		}})
		if len(events) != 2 {
			t.Fatalf("events = %d, want 2: %+v", len(events), events)
		}
		state := mustEventPayload[output.SkillStateEvent](t, events[0])
		if state.Name != "docs" || state.State != output.SkillStateEnabled {
			t.Errorf("events[0] = %+v, want docs/enabled", state)
		}
		input := mustEventPayload[output.UserInputEvent](t, events[1])
		if input.Content != "now do the thing" || input.Mode != "resume" {
			t.Errorf("events[1] = %+v, want content 'now do the thing' mode resume", input)
		}
		if strings.Contains(input.Content, "<steiner-skill") {
			t.Errorf("input content leaked envelope: %q", input.Content)
		}
	})

	t.Run("skill only emits only state", func(t *testing.T) {
		t.Parallel()
		events := replayUserEvents(t, []agent.Message{{Role: agent.MessageRoleUser, Content: activation}})
		if len(events) != 1 {
			t.Fatalf("events = %d, want 1: %+v", len(events), events)
		}
		state := mustEventPayload[output.SkillStateEvent](t, events[0])
		if state.Name != "docs" || state.State != output.SkillStateEnabled {
			t.Errorf("state = %+v, want docs/enabled", state)
		}
	})

	t.Run("deactivate then activate emits both states then input", func(t *testing.T) {
		t.Parallel()
		activationB, _ := prompt.RenderSkillActivation("review", "review body")
		content := prompt.PrependSkillBlocks([]string{deactivation, activationB}, "after switch")
		events := replayUserEvents(t, []agent.Message{{Role: agent.MessageRoleUser, Content: content}})
		if len(events) != 3 {
			t.Fatalf("events = %d, want 3: %+v", len(events), events)
		}
		disabled := mustEventPayload[output.SkillStateEvent](t, events[0])
		if disabled.Name != "docs" || disabled.State != output.SkillStateDisabled {
			t.Errorf("events[0] = %+v, want docs/disabled", disabled)
		}
		enabled := mustEventPayload[output.SkillStateEvent](t, events[1])
		if enabled.Name != "review" || enabled.State != output.SkillStateEnabled {
			t.Errorf("events[1] = %+v, want review/enabled", enabled)
		}
		input := mustEventPayload[output.UserInputEvent](t, events[2])
		if input.Content != "after switch" {
			t.Errorf("events[2] content = %q, want 'after switch'", input.Content)
		}
	})

	t.Run("no blocks unchanged", func(t *testing.T) {
		t.Parallel()
		events := replayUserEvents(t, []agent.Message{{Role: agent.MessageRoleUser, Content: "plain text"}})
		if len(events) != 1 {
			t.Fatalf("events = %d, want 1: %+v", len(events), events)
		}
		input := mustEventPayload[output.UserInputEvent](t, events[0])
		if input.Content != "plain text" || input.Mode != "resume" {
			t.Errorf("input = %+v, want content 'plain text' mode resume", input)
		}
	})

	t.Run("skill only with images still emits input", func(t *testing.T) {
		t.Parallel()
		events := replayUserEvents(t, []agent.Message{{
			Role:    agent.MessageRoleUser,
			Content: activation,
			Images:  []agent.ImageBlock{{ID: "img1"}},
		}})
		if len(events) != 2 {
			t.Fatalf("events = %d, want 2: %+v", len(events), events)
		}
		input := mustEventPayload[output.UserInputEvent](t, events[1])
		if input.Content != "" || len(input.Images) != 1 {
			t.Errorf("events[1] = %+v, want empty content with 1 image", input)
		}
	})
}
