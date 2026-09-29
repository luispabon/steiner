package interactive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

func replayEvents(t *testing.T, msgs []agent.Message) []output.Event {
	t.Helper()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})
	s.replaySessionMessages(msgs)
	return events
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func delegateCall(id, name, task string) agent.Message {
	return agent.Message{
		Role: agent.MessageRoleAssistant,
		ToolCalls: []agent.ToolCall{
			{ID: id, Name: name, Arguments: map[string]any{"task": task}},
		},
	}
}

func ackResult(t *testing.T, callID, name, agentID, status string) agent.Message {
	t.Helper()
	return agent.Message{
		Role:       agent.MessageRoleTool,
		ToolCallID: callID,
		Name:       name,
		Content: mustJSON(t, agent.DelegationResultEnvelope{
			Output:       "Sub-agent started; its result will arrive as a message.",
			Status:       status,
			Continuation: &agent.DelegationContinuation{AgentID: agentID},
		}),
		Retention: &agent.MessageRetention{Kind: tool.RetentionKindDelegateSummary, AgentID: agentID, Status: status},
	}
}

func resultEnvelope(t *testing.T, callID, status, result string, seq uint64) string {
	t.Helper()
	body := mustJSON(t, agent.DelegationResultEnvelope{
		Output:       result,
		Continuation: &agent.DelegationContinuation{AgentID: "agent-a"},
	})
	if status == "failed" || status == "lost" {
		body = agent.FailureBody(status, result)
	}
	return agent.RenderSubAgentResultEnvelope(agent.SubAgentCompletion{
		Seq:              seq,
		ParentCallID:     callID,
		AgentID:          "agent-a",
		AgentType:        "explore",
		Status:           status,
		ObjectivePreview: "objective",
		TurnCount:        3,
		TokenCount:       120,
		Duration:         2 * time.Second,
		Body:             body,
	})
}

func eventsOfType(events []output.Event, typ string) []output.Event {
	var out []output.Event
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func assertNoEnvelopeInUserInput(t *testing.T, events []output.Event) {
	t.Helper()
	for _, e := range eventsOfType(events, output.EventTypeUserInput) {
		p := e.Payload.(output.UserInputEvent)
		if strings.Contains(p.Content, "steiner-sub-agent") {
			t.Errorf("user input contains envelope tag: %q", p.Content)
		}
	}
}

func TestReplayAsyncResultsOnlyMessage(t *testing.T) {
	t.Parallel()
	env := resultEnvelope(t, "call-1", "completed", "found it", 1)
	events := replayEvents(t, []agent.Message{
		delegateCall("call-1", "sub_agent", "find bug"),
		ackResult(t, "call-1", "sub_agent", "agent-a", "running"),
		{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: env},
	})

	assertNoEnvelopeInUserInput(t, events)
	if n := len(eventsOfType(events, output.EventTypeUserInput)); n != 0 {
		t.Errorf("user input events = %d, want 0", n)
	}
	started := eventsOfType(events, output.EventTypeDelegationStarted)
	if len(started) != 1 {
		t.Fatalf("started events = %d, want 1", len(started))
	}
	if p := started[0].Payload.(output.DelegationStartedEvent); p.AgentID != "agent-a" || p.TaskPreview != "find bug" {
		t.Errorf("started = %+v", p)
	}
	complete := eventsOfType(events, output.EventTypeDelegationComplete)
	if len(complete) != 1 {
		t.Fatalf("complete events = %d, want 1", len(complete))
	}
	p := complete[0].Payload.(output.DelegationCompleteEvent)
	if p.AgentID != "agent-a" || p.Output != "found it" || p.TurnCount != 3 || p.TokenCount != 120 {
		t.Errorf("complete = %+v", p)
	}
	if n := len(eventsOfType(events, output.EventTypeDelegationFailed)); n != 0 {
		t.Errorf("failed events = %d, want 0 (ack resolved)", n)
	}
}

func TestReplayAsyncResultsWithResidualText(t *testing.T) {
	t.Parallel()
	env := resultEnvelope(t, "call-1", "completed", "found it", 1)
	events := replayEvents(t, []agent.Message{
		delegateCall("call-1", "sub_agent", "find bug"),
		ackResult(t, "call-1", "sub_agent", "agent-a", "running"),
		{Role: agent.MessageRoleUser, Content: "<steiner-sub-agents-pending>agent-b (explore, running)</steiner-sub-agents-pending>\n\n" + env + "\n\nplease continue"},
	})

	assertNoEnvelopeInUserInput(t, events)
	inputs := eventsOfType(events, output.EventTypeUserInput)
	if len(inputs) != 1 {
		t.Fatalf("user input events = %d, want 1", len(inputs))
	}
	if got := inputs[0].Payload.(output.UserInputEvent).Content; got != "please continue" {
		t.Errorf("user input text = %q, want %q", got, "please continue")
	}
	if n := len(eventsOfType(events, output.EventTypeDelegationComplete)); n != 1 {
		t.Errorf("complete events = %d, want 1", n)
	}
}

func TestReplayAsyncGroupedResultsInSeqOrder(t *testing.T) {
	t.Parallel()
	msg, ok := agent.BuildDeliveryMessage(agent.DeliveryParts{
		Completions: []agent.SubAgentCompletion{
			{Seq: 2, ParentCallID: "call-2", AgentID: "agent-b", AgentType: "explore", Status: "completed", Body: `{"output":"second"}`},
			{Seq: 1, ParentCallID: "call-1", AgentID: "agent-a", AgentType: "explore", Status: "completed", Body: `{"output":"first"}`},
		},
	})
	if !ok {
		t.Fatal("BuildDeliveryMessage returned false")
	}
	events := replayEvents(t, []agent.Message{
		{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{
			{ID: "call-1", Name: "sub_agent", Arguments: map[string]any{"task": "one"}},
			{ID: "call-2", Name: "sub_agent", Arguments: map[string]any{"task": "two"}},
		}},
		ackResult(t, "call-1", "sub_agent", "agent-a", "running"),
		ackResult(t, "call-2", "sub_agent", "agent-b", "queued"),
		msg,
	})

	assertNoEnvelopeInUserInput(t, events)
	complete := eventsOfType(events, output.EventTypeDelegationComplete)
	if len(complete) != 2 {
		t.Fatalf("complete events = %d, want 2", len(complete))
	}
	first := complete[0].Payload.(output.DelegationCompleteEvent)
	second := complete[1].Payload.(output.DelegationCompleteEvent)
	if first.AgentID != "agent-a" || first.Output != "first" || second.AgentID != "agent-b" || second.Output != "second" {
		t.Errorf("complete order = %+v, %+v", first, second)
	}
	if n := len(eventsOfType(events, output.EventTypeDelegationFailed)); n != 0 {
		t.Errorf("failed events = %d, want 0", n)
	}
}

func TestReplayAsyncLostEnvelope(t *testing.T) {
	t.Parallel()
	env := agent.RenderSubAgentResultEnvelope(agent.LostSubAgentCompletion(agent.SubAgentLedgerEntry{
		ParentCallID: "call-1", AgentID: "agent-a", AgentType: "explore",
	}))
	events := replayEvents(t, []agent.Message{
		delegateCall("call-1", "sub_agent", "find bug"),
		ackResult(t, "call-1", "sub_agent", "agent-a", "running"),
		{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: env},
	})

	failed := eventsOfType(events, output.EventTypeDelegationFailed)
	if len(failed) != 1 {
		t.Fatalf("failed events = %d, want 1", len(failed))
	}
	p := failed[0].Payload.(output.DelegationFailedEvent)
	if p.AgentID != "agent-a" || p.TaskPreview != "find bug" || !strings.Contains(p.Error, "lost") {
		t.Errorf("failed = %+v", p)
	}
	if n := len(eventsOfType(events, output.EventTypeDelegationComplete)); n != 0 {
		t.Errorf("complete events = %d, want 0", n)
	}
}

func TestReplayAsyncFollowUpReusingAgentIDCorrelatesByCallID(t *testing.T) {
	t.Parallel()
	events := replayEvents(t, []agent.Message{
		delegateCall("call-1", "sub_agent", "first task"),
		ackResult(t, "call-1", "sub_agent", "agent-a", "running"),
		{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: resultEnvelope(t, "call-1", "completed", "first done", 1)},
		delegateCall("call-2", "follow_up", "second task"),
		ackResult(t, "call-2", "follow_up", "agent-a", "running"),
		{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: resultEnvelope(t, "call-2", "failed", "second broke", 2)},
	})

	failed := eventsOfType(events, output.EventTypeDelegationFailed)
	if len(failed) != 1 {
		t.Fatalf("failed events = %d, want 1", len(failed))
	}
	if p := failed[0].Payload.(output.DelegationFailedEvent); p.TaskPreview != "second task" || p.Error != "second broke" {
		t.Errorf("failed = %+v, want second task/second broke", p)
	}
	complete := eventsOfType(events, output.EventTypeDelegationComplete)
	if len(complete) != 1 || complete[0].Payload.(output.DelegationCompleteEvent).Output != "first done" {
		t.Errorf("complete events = %+v", complete)
	}
}

func TestReplayAsyncUnresolvedAckShownAsNoResult(t *testing.T) {
	t.Parallel()
	events := replayEvents(t, []agent.Message{
		delegateCall("call-1", "sub_agent", "find bug"),
		ackResult(t, "call-1", "sub_agent", "agent-a", "queued"),
		{Role: agent.MessageRoleAssistant, Content: "waiting"},
	})

	if n := len(eventsOfType(events, output.EventTypeDelegationComplete)); n != 0 {
		t.Errorf("complete events = %d, want 0", n)
	}
	failed := eventsOfType(events, output.EventTypeDelegationFailed)
	if len(failed) != 1 {
		t.Fatalf("failed events = %d, want 1", len(failed))
	}
	if p := failed[0].Payload.(output.DelegationFailedEvent); p.AgentID != "agent-a" || p.Error != "no result" {
		t.Errorf("failed = %+v", p)
	}
}

func TestReplayBlockingDelegatesUnchangedFromOldFormat(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "replay_blocking_delegates.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var msgs []agent.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	var got []string
	for _, e := range replayEvents(t, msgs) {
		got = append(got, e.Type+" "+mustJSON(t, e.Payload))
	}
	want, err := os.ReadFile(filepath.Join("testdata", "replay_blocking_delegates.golden"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if strings.Join(got, "\n")+"\n" != string(want) {
		t.Errorf("replayed events changed:\n%s", strings.Join(got, "\n"))
	}
}
