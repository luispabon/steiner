package interactive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	s.replaySessionMessages(msgs, nil)
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

	queued := eventsOfType(events, output.EventTypeDelegationQueued)
	if len(queued) != 1 {
		t.Fatalf("queued events = %d, want 1", len(queued))
	}
	if p := queued[0].Payload.(output.DelegationQueuedEvent); p.AgentID != "agent-a" || p.CallID != "call-1" {
		t.Errorf("queued = %+v", p)
	}
	if n := len(eventsOfType(events, output.EventTypeDelegationStarted)); n != 0 {
		t.Errorf("started events = %d, want 0 for a queued ack", n)
	}
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

func TestReplayDuplicateIDRejectedThenLedgerOrphanEventSequence(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{
		{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{
			{ID: "same", Name: "sub_agent", Arguments: map[string]any{"task": "first"}},
			{ID: "same", Name: "sub_agent", Arguments: map[string]any{"task": "second"}},
		}},
		{Role: agent.MessageRoleTool, ToolCallID: "same", Name: "sub_agent", Content: `{"ok":false,"error":{"message":"denied"}}`, DelegationAdmission: &tool.DelegationAdmission{Status: "rejected"}},
	}
	ledger := []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-orphan", AgentType: "explore", BatchID: "batch", Group: "group"}}
	events := replayEventsWithLedger(t, msgs, ledger)
	var sequence []string
	for _, event := range events {
		switch event.Type {
		case output.EventTypeToolCallStarted:
			p := event.Payload.(output.ToolCallStartedEvent)
			sequence = append(sequence, "tool-start:"+p.CallID+":"+taskFromArgs(p.Arguments))
		case output.EventTypeDelegationAccepted:
			p := event.Payload.(output.DelegationAcceptedEvent)
			sequence = append(sequence, "accepted:"+p.CallID+":"+p.AgentID+":"+p.BatchID+":"+p.Group)
		case output.EventTypeDelegationStarted:
			p := event.Payload.(output.DelegationStartedEvent)
			sequence = append(sequence, "started:"+p.CallID+":"+p.AgentID+":"+p.TaskPreview)
		case output.EventTypeToolCallFinished:
			p := event.Payload.(output.ToolCallFinishedEvent)
			if p.DelegationAdmission == nil || p.DelegationAdmission.Status != "rejected" {
				t.Fatalf("finish admission = %#v, want rejected", p.DelegationAdmission)
			}
			sequence = append(sequence, "tool-finished:"+p.CallID+":"+p.DelegationAdmission.Status)
		}
	}
	// The rejected first occurrence is fully emitted, including its parent
	// finish, before the second (ledger orphan) occurrence starts. The orphan
	// never emits a fabricated result or finish.
	want := []string{
		"tool-start:same:first",
		"tool-finished:same:rejected",
		"tool-start:same:second",
		"accepted:same:agent-orphan:batch:group",
		"started:same:agent-orphan:second",
	}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("replayed event sequence = %v, want %v", sequence, want)
	}
}

func TestReplayDelegationBundlesEmitInAssistantOrder(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{
		{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{
			{ID: "call-1", Name: "sub_agent", Arguments: map[string]any{"task": "one"}},
			{ID: "call-2", Name: "sub_agent", Arguments: map[string]any{"task": "two"}},
		}},
		ackResult(t, "call-1", "sub_agent", "agent-a", "running"),
		ackResult(t, "call-2", "sub_agent", "agent-b", "queued"),
	}
	ledger := []agent.SubAgentLedgerEntry{
		{ParentCallID: "call-1", AgentID: "agent-a", BatchID: "batch-1", Group: "group-1"},
		{ParentCallID: "call-2", AgentID: "agent-b", BatchID: "batch-2", Group: "group-2"},
	}
	events := replayEventsWithLedger(t, msgs, ledger)
	var sequence []string
	for _, event := range events {
		switch event.Type {
		case output.EventTypeToolCallStarted:
			sequence = append(sequence, "tool-start:"+event.Payload.(output.ToolCallStartedEvent).CallID)
		case output.EventTypeDelegationAccepted:
			p := event.Payload.(output.DelegationAcceptedEvent)
			sequence = append(sequence, "accepted:"+p.AgentID+":"+p.CallID+":"+p.BatchID+":"+p.Group)
		case output.EventTypeDelegationStarted:
			p := event.Payload.(output.DelegationStartedEvent)
			sequence = append(sequence, "started:"+p.AgentID+":"+p.CallID)
		case output.EventTypeDelegationQueued:
			p := event.Payload.(output.DelegationQueuedEvent)
			sequence = append(sequence, "queued:"+p.AgentID+":"+p.CallID)
		}
	}
	// nil/unknown admission is still emitted as one serial bundle; call-1's
	// whole lifecycle lands before call-2's start.
	want := []string{
		"tool-start:call-1",
		"accepted:agent-a:call-1:batch-1:group-1",
		"started:agent-a:call-1",
		"tool-start:call-2",
		"accepted:agent-b:call-2:batch-2:group-2",
		"queued:agent-b:call-2",
	}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("bundled event sequence = %v, want %v", sequence, want)
	}
}

func TestReplayAuthoritativeBundleFullyPrecedesNextOccurrence(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{
		{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{
			{ID: "call-1", Name: "sub_agent", Arguments: map[string]any{"task": "one"}},
			{ID: "call-2", Name: "sub_agent", Arguments: map[string]any{"task": "two"}},
		}},
		{Role: agent.MessageRoleTool, ToolCallID: "call-1", Name: "sub_agent", Content: `{"output":"first"}`, DelegationAdmission: &tool.DelegationAdmission{Status: "accepted", AgentID: "agent-a", BatchID: "batch-1", Group: "group-1"}},
		{Role: agent.MessageRoleTool, ToolCallID: "call-2", Name: "sub_agent", Content: `{"output":"second"}`, DelegationAdmission: &tool.DelegationAdmission{Status: "accepted", AgentID: "agent-b", BatchID: "batch-2", Group: "group-2"}},
	}
	ledger := []agent.SubAgentLedgerEntry{
		{ParentCallID: "call-1", AgentID: "agent-a", BatchID: "batch-1", Group: "group-1"},
		{ParentCallID: "call-2", AgentID: "agent-b", BatchID: "batch-2", Group: "group-2"},
	}
	events := replayEventsWithLedger(t, msgs, ledger)
	var sequence []string
	for _, event := range events {
		switch event.Type {
		case output.EventTypeToolCallStarted:
			sequence = append(sequence, "tool-start:"+event.Payload.(output.ToolCallStartedEvent).CallID)
		case output.EventTypeDelegationAccepted:
			p := event.Payload.(output.DelegationAcceptedEvent)
			sequence = append(sequence, "accepted:"+p.AgentID+":"+p.CallID+":"+p.BatchID+":"+p.Group)
		case output.EventTypeDelegationStarted:
			p := event.Payload.(output.DelegationStartedEvent)
			sequence = append(sequence, "started:"+p.AgentID+":"+p.CallID)
		case output.EventTypeDelegationComplete:
			p := event.Payload.(output.DelegationCompleteEvent)
			sequence = append(sequence, "complete:"+p.AgentID+":"+p.Output)
		case output.EventTypeToolCallFinished:
			sequence = append(sequence, "tool-finished:"+event.Payload.(output.ToolCallFinishedEvent).CallID)
		}
	}
	// The first occurrence's complete bundle, including its actual parent
	// finish, lands before the second occurrence's tool-start.
	want := []string{
		"tool-start:call-1",
		"accepted:agent-a:call-1:batch-1:group-1",
		"started:agent-a:call-1",
		"complete:agent-a:first",
		"tool-finished:call-1",
		"tool-start:call-2",
		"accepted:agent-b:call-2:batch-2:group-2",
		"started:agent-b:call-2",
		"complete:agent-b:second",
		"tool-finished:call-2",
	}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("authoritative bundle sequence = %v, want %v", sequence, want)
	}
}

func TestReplayDelegationBundleEmitsEachEventOnce(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{
		delegateCall("call-1", "sub_agent", "task"),
		{Role: agent.MessageRoleTool, ToolCallID: "call-1", Name: "sub_agent", Content: `{"output":"done"}`, DelegationAdmission: &tool.DelegationAdmission{Status: "accepted", AgentID: "agent-a", BatchID: "batch", Group: "group"}},
	}
	ledger := []agent.SubAgentLedgerEntry{{ParentCallID: "call-1", AgentID: "agent-a", BatchID: "batch", Group: "group"}}
	events := replayEventsWithLedger(t, msgs, ledger)
	for _, tc := range []struct {
		name string
		typ  string
	}{
		{"accepted", output.EventTypeDelegationAccepted},
		{"started", output.EventTypeDelegationStarted},
		{"complete", output.EventTypeDelegationComplete},
		{"finished", output.EventTypeToolCallFinished},
	} {
		if got := len(eventsOfType(events, tc.typ)); got != 1 {
			t.Errorf("%s events = %d, want exactly 1", tc.name, got)
		}
	}
}

func TestReplayLedgerOrphanEmitsAcceptedAndStartedWithoutFinish(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{delegateCall("orphan", "sub_agent", "lost task")}
	ledger := []agent.SubAgentLedgerEntry{{ParentCallID: "orphan", AgentID: "agent-lost", BatchID: "batch", Group: "group"}}
	events := replayEventsWithLedger(t, msgs, ledger)
	if got := len(eventsOfType(events, output.EventTypeToolCallFinished)); got != 0 {
		t.Errorf("orphan fabricated %d tool finishes, want 0", got)
	}
	if got := len(eventsOfType(events, output.EventTypeDelegationComplete)); got != 0 {
		t.Errorf("orphan fabricated %d completions, want 0", got)
	}
	if got := len(eventsOfType(events, output.EventTypeDelegationFailed)); got != 0 {
		t.Errorf("orphan fabricated %d failures, want 0", got)
	}
	acceptedAt, startedAt := -1, -1
	for i, event := range events {
		switch event.Type {
		case output.EventTypeDelegationAccepted:
			acceptedAt = i
			want := output.DelegationOccurrence{CallID: "orphan", BatchID: "batch", AgentID: "agent-lost"}
			if p := event.Payload.(output.DelegationAcceptedEvent); p.DelegationOccurrence != want || p.Group != "group" {
				t.Errorf("orphan accepted = %+v, want %+v in group", p, want)
			}
		case output.EventTypeDelegationStarted:
			startedAt = i
		}
	}
	if acceptedAt < 0 || startedAt <= acceptedAt {
		t.Fatalf("orphan accepted/started positions = %d/%d", acceptedAt, startedAt)
	}
}

func TestReplayAdmissionOccurrenceLocalAndTypedFinish(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{
		delegateCall("same", "sub_agent", "accepted"),
		{Role: agent.MessageRoleTool, ToolCallID: "same", Name: "sub_agent", Content: `{"ok":false,"error":{"message":"child failed"}}`, DelegationAdmission: &tool.DelegationAdmission{Status: "accepted", AgentID: "agent-a", BatchID: "batch-a", Group: "group-a"}},
		delegateCall("same", "sub_agent", "rejected"),
		{Role: agent.MessageRoleTool, ToolCallID: "same", Name: "sub_agent", Content: `{"ok":false,"error":{"message":"denied"}}`, DelegationAdmission: &tool.DelegationAdmission{Status: "rejected", PolicyNotice: true}},
	}
	events := replayEvents(t, msgs)
	accepted := eventsOfType(events, output.EventTypeDelegationAccepted)
	if len(accepted) != 1 || accepted[0].Payload.(output.DelegationAcceptedEvent).AgentID != "agent-a" {
		t.Fatalf("accepted events = %+v", accepted)
	}
	finished := eventsOfType(events, output.EventTypeToolCallFinished)
	if len(finished) != 2 {
		t.Fatalf("finished events = %d, want 2", len(finished))
	}
	p := finished[1].Payload.(output.ToolCallFinishedEvent)
	if p.DelegationAdmission == nil || !p.DelegationAdmission.PolicyNotice || p.Result != msgs[3].Content || p.Error == "" {
		t.Fatalf("rejected finish = %+v", p)
	}
	if got := len(eventsOfType(events, output.EventTypeDelegationStarted)); got != 1 {
		t.Fatalf("delegation starts = %d, want accepted occurrence only", got)
	}
	for _, event := range events {
		if event.Type == output.EventTypeToolCallFinished {
			p := event.Payload.(output.ToolCallFinishedEvent)
			if p.CallID == "same" && p.DelegationAdmission != nil && p.DelegationAdmission.Status == "rejected" && p.Error == "" {
				t.Fatalf("rejected projected error missing: %+v", p)
			}
		}
	}
}

func TestReplayAcceptedPreparationFailureUsesAdmissionAgent(t *testing.T) {
	t.Parallel()
	body := `{"output":"","status":"failed","reason":"child setup failed"}`
	msgs := []agent.Message{delegateCall("prep", "sub_agent", "setup"), {Role: agent.MessageRoleTool, ToolCallID: "prep", Name: "sub_agent", Content: body, DelegationAdmission: &tool.DelegationAdmission{Status: "accepted", AgentID: "agent-actual"}}}
	events := replayEvents(t, msgs)
	var sequence []string
	for _, event := range events {
		if event.Type == output.EventTypeDelegationAccepted {
			sequence = append(sequence, "accepted")
		}
		if event.Type == output.EventTypeDelegationStarted {
			sequence = append(sequence, "started")
		}
		if event.Type == output.EventTypeDelegationFailed {
			p := event.Payload.(output.DelegationFailedEvent)
			sequence = append(sequence, "failed:"+p.AgentID)
		}
		if event.Type == output.EventTypeToolCallFinished {
			sequence = append(sequence, "finished")
		}
	}
	want := []string{"accepted", "failed:agent-actual", "finished"}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("event order = %v, want %v", sequence, want)
	}
	finish := eventsOfType(events, output.EventTypeToolCallFinished)[0].Payload.(output.ToolCallFinishedEvent)
	if finish.Error != "child setup failed" {
		t.Fatalf("finish error = %q", finish.Error)
	}
}

func TestReplayLedgerEmptyRetentionKeepsRunningAndQueuedProgress(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"running", "queued"} {
		t.Run(status, func(t *testing.T) {
			callID := "call-" + status
			msgs := []agent.Message{
				delegateCall(callID, "sub_agent", "work"),
				{Role: agent.MessageRoleTool, ToolCallID: callID, Name: "sub_agent", Content: `{"output":"started","status":"` + status + `"}`, Retention: new(agent.MessageRetention)},
			}
			ledger := []agent.SubAgentLedgerEntry{{ParentCallID: callID, AgentID: "agent-a", AgentType: "explore", BatchID: "batch-a", Group: "group-a"}}
			events := replayEventsWithLedger(t, msgs, ledger)
			var sequence []string
			for _, event := range events {
				switch event.Type {
				case output.EventTypeDelegationAccepted:
					p := event.Payload.(output.DelegationAcceptedEvent)
					if p.AgentID != "agent-a" || p.CallID != callID || p.BatchID != "batch-a" || p.Group != "group-a" {
						t.Errorf("accepted = %+v", p)
					}
					sequence = append(sequence, "accepted")
				case output.EventTypeDelegationStarted:
					p := event.Payload.(output.DelegationStartedEvent)
					if status != "running" || p.AgentID != "agent-a" || p.CallID != callID {
						t.Errorf("started = %+v", p)
					}
					sequence = append(sequence, "started")
				case output.EventTypeDelegationQueued:
					p := event.Payload.(output.DelegationQueuedEvent)
					if status != "queued" || p.AgentID != "agent-a" || p.CallID != callID {
						t.Errorf("queued = %+v", p)
					}
					sequence = append(sequence, "queued")
				case output.EventTypeDelegationFailed, output.EventTypeDelegationComplete:
					t.Errorf("unexpected terminal event: %s", event.Type)
				}
			}
			wantProgress := "started"
			if status == "queued" {
				wantProgress = "queued"
			}
			if !reflect.DeepEqual(sequence, []string{"accepted", wantProgress}) {
				t.Fatalf("event order = %v, want accepted then %s", sequence, wantProgress)
			}
		})
	}
}

func TestReplayLedgerRunningDoesNotAddSyntheticTerminal(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{delegateCall("run", "sub_agent", "work"), ackResult(t, "run", "sub_agent", "agent-a", "running")}
	var events []output.Event
	s := testNewSession(t, Dependencies{BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) })})
	s.replaySessionMessages(msgs, []agent.SubAgentLedgerEntry{{ParentCallID: "run", AgentID: "agent-a", AgentType: "explore"}})
	var sequence []string
	for _, event := range events {
		if event.Type == output.EventTypeDelegationAccepted {
			sequence = append(sequence, "accepted")
		}
		if event.Type == output.EventTypeDelegationStarted {
			sequence = append(sequence, "started")
		}
		if event.Type == output.EventTypeDelegationQueued {
			sequence = append(sequence, "queued")
		}
		if event.Type == output.EventTypeDelegationFailed {
			sequence = append(sequence, "failed")
		}
	}
	if !reflect.DeepEqual(sequence, []string{"accepted", "started"}) {
		t.Fatalf("running sequence = %v, want one accepted running lifecycle", sequence)
	}
}

func TestReplayOrphanAcceptedFromLedgerThenLostOnce(t *testing.T) {
	t.Parallel()
	call := agent.Message{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "orphan", Name: "sub_agent", Arguments: map[string]any{"task": "lost task"}}}}
	var events []output.Event
	s := testNewSession(t, Dependencies{BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) })})
	ledger := []agent.SubAgentLedgerEntry{{ParentCallID: "orphan", AgentID: "agent-lost", AgentType: "explore", BatchID: "batch", Group: "group"}}
	s.replaySessionMessages([]agent.Message{call}, ledger)
	if got := len(eventsOfType(events, output.EventTypeToolCallStarted)); got != 1 {
		t.Fatalf("tool starts = %d, want 1", got)
	}
	if got := len(eventsOfType(events, output.EventTypeDelegationAccepted)); got != 1 {
		t.Fatalf("accepted = %d, want 1", got)
	}
	if got := len(eventsOfType(events, output.EventTypeDelegationStarted)); got != 1 {
		t.Fatalf("delegation starts = %d, want 1", got)
	}
	if got := len(eventsOfType(events, output.EventTypeDelegationFailed)); got != 0 {
		t.Fatalf("early failures = %d, want 0", got)
	}
	lost := agent.RenderSubAgentResultEnvelope(agent.LostSubAgentCompletion(ledger[0]))
	s.replaySessionMessages([]agent.Message{{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: lost}}, nil)
	if got := len(eventsOfType(events, output.EventTypeDelegationFailed)); got != 1 {
		t.Fatalf("lost failures = %d, want 1", got)
	}
	if got := len(eventsOfType(events, output.EventTypeToolCallFinished)); got != 0 {
		t.Fatalf("synthetic tool finishes = %d, want 0", got)
	}
}

func admissionResult(callID, agentID, batch, group string) agent.Message {
	return agent.Message{Role: agent.MessageRoleTool, ToolCallID: callID, Name: "sub_agent", Content: `{"output":"started","status":"running","continuation":{"agent_id":"` + agentID + `"}}`, DelegationAdmission: &tool.DelegationAdmission{Status: "accepted", AgentID: agentID, BatchID: batch, Group: group}}
}

func TestReplayLedgerOwnershipAmbiguityMatrix(t *testing.T) {
	tests := []struct {
		name        string
		msgs        []agent.Message
		ledger      []agent.SubAgentLedgerEntry
		accept      []string
		started     []string
		ledgerIndex []int
	}{
		{name: "two unknown running same ID", msgs: []agent.Message{delegateCall("same", "sub_agent", "one"), ackResult(t, "same", "sub_agent", "unknown", "running"), delegateCall("same", "sub_agent", "two"), ackResult(t, "same", "sub_agent", "unknown", "running")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "ledger"}}, started: []string{"unknown", "unknown"}, accept: []string{"unknown", "unknown"}, ledgerIndex: []int{-1, -1}},
		{name: "one eligible two entries", msgs: []agent.Message{delegateCall("same", "sub_agent", "one"), ackResult(t, "same", "sub_agent", "unknown", "running")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "ledger-a"}, {ParentCallID: "same", AgentID: "ledger-b"}}, accept: []string{"unknown"}, started: []string{"unknown"}, ledgerIndex: []int{-1}},
		{name: "explicit A then unknown B", msgs: []agent.Message{delegateCall("same", "sub_agent", "A"), admissionResult("same", "agent-A", "", ""), delegateCall("same", "sub_agent", "B"), ackResult(t, "same", "sub_agent", "unknown", "running")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-A"}, {ParentCallID: "same", AgentID: "agent-B"}}, accept: []string{"agent-A", "agent-B"}, started: []string{"agent-A", "agent-B"}},
		{name: "unknown B then explicit A", msgs: []agent.Message{delegateCall("same", "sub_agent", "B"), ackResult(t, "same", "sub_agent", "unknown", "running"), delegateCall("same", "sub_agent", "A"), admissionResult("same", "agent-A", "", "")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-A"}, {ParentCallID: "same", AgentID: "agent-B"}}, accept: []string{"agent-B", "agent-A"}, started: []string{"agent-B", "agent-A"}},
		{name: "explicit A owns exact ledger among B A", msgs: []agent.Message{delegateCall("same", "sub_agent", "A"), admissionResult("same", "agent-A", "", "")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-B"}, {ParentCallID: "same", AgentID: "agent-A"}}, accept: []string{"agent-A"}, started: []string{"agent-A"}},
		{name: "batch and group disambiguate same-agent ledger", msgs: []agent.Message{delegateCall("same", "sub_agent", "first"), admissionResult("same", "agent-A", "batch-1", "group-1"), delegateCall("same", "sub_agent", "second"), admissionResult("same", "agent-A", "batch-2", "group-2")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-A", BatchID: "batch-2", Group: "group-2"}, {ParentCallID: "same", AgentID: "agent-A", BatchID: "batch-1", Group: "group-1"}}, accept: []string{"agent-A", "agent-A"}, started: []string{"agent-A", "agent-A"}},
		{name: "unknown running plus ambiguous orphan", msgs: []agent.Message{delegateCall("same", "sub_agent", "running"), ackResult(t, "same", "sub_agent", "unknown", "running"), delegateCall("same", "sub_agent", "orphan")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "ledger-a"}, {ParentCallID: "same", AgentID: "ledger-b"}}, accept: []string{"unknown"}, started: []string{"unknown"}},
		{name: "ambiguous explicit not borrowed by unknown", msgs: []agent.Message{delegateCall("same", "sub_agent", "A"), admissionResult("same", "agent-A", "", ""), delegateCall("same", "sub_agent", "B"), ackResult(t, "same", "sub_agent", "unknown", "running")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-A"}, {ParentCallID: "same", AgentID: "agent-A"}, {ParentCallID: "same", AgentID: "agent-B"}}, accept: []string{"agent-A", "agent-B"}, started: []string{"agent-A", "agent-B"}, ledgerIndex: []int{-1, 2}},
		{name: "batch and group do not match ledger without identity evidence", msgs: []agent.Message{delegateCall("same", "sub_agent", "one"), admissionResult("same", "agent-A", "batch-1", "group-1"), delegateCall("same", "sub_agent", "two"), admissionResult("same", "agent-A", "batch-2", "group-2")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-A"}}, accept: []string{"agent-A", "agent-A"}, started: []string{"agent-A", "agent-A"}},
		{name: "empty batch and group cannot choose identity", msgs: []agent.Message{delegateCall("same", "sub_agent", "one"), admissionResult("same", "agent-A", "batch-1", "group-1"), delegateCall("same", "sub_agent", "two"), admissionResult("same", "agent-A", "batch-2", "group-2")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-A", BatchID: "", Group: ""}}, accept: []string{"agent-A", "agent-A"}, started: []string{"agent-A", "agent-A"}},
		{name: "matching running call ID has ledger ownership", msgs: []agent.Message{delegateCall("same", "sub_agent", "running"), ackResult(t, "same", "sub_agent", "unknown", "running")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-new"}}, accept: []string{"agent-new"}, started: []string{"agent-new"}},
		{name: "explicit ambiguous result ownership", msgs: []agent.Message{delegateCall("same", "sub_agent", "one"), admissionResult("same", "agent-A", "batch", "group"), delegateCall("same", "sub_agent", "two"), admissionResult("same", "agent-A", "batch", "group")}, ledger: []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-A", BatchID: "batch", Group: "group"}, {ParentCallID: "same", AgentID: "agent-A", BatchID: "batch", Group: "group"}}, accept: []string{"agent-A", "agent-A"}, started: []string{"agent-A", "agent-A"}, ledgerIndex: []int{-1, -1}},
		{name: "reserved name ledger is not acceptance", msgs: []agent.Message{delegateCall("same", "sub_agent", "reserved")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := buildReplayOccurrencePlan(tc.msgs, tc.ledger)
			var gotOwnership []int
			for i := range tc.msgs {
				if tc.msgs[i].Role != agent.MessageRoleAssistant {
					continue
				}
				for j := range tc.msgs[i].ToolCalls {
					gotOwnership = append(gotOwnership, plan.occurrences[replayOccurrenceKey{messageIndex: i, callIndex: j}].ledgerIndex)
				}
			}
			if tc.ledgerIndex != nil && !reflect.DeepEqual(gotOwnership, tc.ledgerIndex) {
				t.Fatalf("ledger ownership = %v, want %v", gotOwnership, tc.ledgerIndex)
			}
			events := replayEventsWithLedger(t, tc.msgs, tc.ledger)
			var accepted, started []string
			for _, event := range events {
				switch event.Type {
				case output.EventTypeDelegationAccepted:
					accepted = append(accepted, event.Payload.(output.DelegationAcceptedEvent).AgentID)
				case output.EventTypeDelegationStarted:
					started = append(started, event.Payload.(output.DelegationStartedEvent).AgentID)
				}
			}
			if !reflect.DeepEqual(accepted, tc.accept) || !reflect.DeepEqual(started, tc.started) {
				t.Fatalf("accepted/started = %v/%v, want %v/%v", accepted, started, tc.accept, tc.started)
			}
		})
	}
}

func TestReplayMixedPairedAndLedgerOrphanPreservesCallOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		calls []agent.ToolCall
		want  []string
	}{
		{
			name: "paired before orphan",
			calls: []agent.ToolCall{
				{ID: "A", Name: "bash"},
				{ID: "B", Name: "sub_agent", Arguments: map[string]any{"task": "orphan"}},
			},
			want: []string{"A", "B"},
		},
		{
			name: "orphan before paired",
			calls: []agent.ToolCall{
				{ID: "B", Name: "sub_agent", Arguments: map[string]any{"task": "orphan"}},
				{ID: "A", Name: "bash"},
			},
			want: []string{"B", "A"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := []agent.Message{
				{Role: agent.MessageRoleAssistant, ToolCalls: tc.calls},
				{Role: agent.MessageRoleTool, ToolCallID: "A", Name: "bash", Content: "done"},
			}
			events := replayEventsWithLedger(t, msgs, []agent.SubAgentLedgerEntry{{ParentCallID: "B", AgentID: "agent-B"}})
			var started []string
			bStartedAt, bAcceptedAt, bLifecycleAt := -1, -1, -1
			for i, event := range events {
				switch event.Type {
				case output.EventTypeToolCallStarted:
					p := event.Payload.(output.ToolCallStartedEvent)
					started = append(started, p.CallID)
					if p.CallID == "B" {
						bStartedAt = i
					}
				case output.EventTypeDelegationAccepted:
					if event.Payload.(output.DelegationAcceptedEvent).CallID == "B" {
						bAcceptedAt = i
					}
				case output.EventTypeDelegationStarted:
					if event.Payload.(output.DelegationStartedEvent).CallID == "B" {
						bLifecycleAt = i
					}
				}
			}
			if !reflect.DeepEqual(started, tc.want) {
				t.Fatalf("tool starts = %v, want %v", started, tc.want)
			}
			if bStartedAt < 0 || bAcceptedAt <= bStartedAt || bLifecycleAt <= bAcceptedAt {
				t.Fatalf("orphan start/accept/lifecycle positions = %d/%d/%d", bStartedAt, bAcceptedAt, bLifecycleAt)
			}
		})
	}
}

func replayEventsWithLedger(t *testing.T, msgs []agent.Message, ledger []agent.SubAgentLedgerEntry) []output.Event {
	t.Helper()
	var events []output.Event
	s := testNewSession(t, Dependencies{BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) })})
	s.replaySessionMessages(msgs, ledger)
	return events
}

func TestReplayDuplicateCallsAndNoBackwardPair(t *testing.T) {
	for _, tc := range []struct {
		name string
		msgs []agent.Message
		want int
	}{
		{"same message duplicate call IDs", []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "same", Name: "sub_agent", Arguments: map[string]any{"task": "one"}}, {ID: "same", Name: "sub_agent", Arguments: map[string]any{"task": "two"}}}}, {Role: agent.MessageRoleTool, ToolCallID: "same", Name: "sub_agent", Content: `{"output":"ok"}`}}, 1},
		{"result before future call", []agent.Message{{Role: agent.MessageRoleTool, ToolCallID: "same", Name: "sub_agent", Content: `{"output":"early"}`}, delegateCall("same", "sub_agent", "later"), {Role: agent.MessageRoleTool, ToolCallID: "same", Name: "sub_agent", Content: `{"output":"paired"}`}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := replayEvents(t, tc.msgs)
			if got := len(eventsOfType(events, output.EventTypeDelegationComplete)); got != tc.want {
				t.Fatalf("complete events = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReplayAcceptedDuplicateIdentityAndAmbiguousExplicitEntries(t *testing.T) {
	msgs := []agent.Message{delegateCall("same", "sub_agent", "one"), admissionResult("same", "agent-A", "batch", "group"), delegateCall("same", "sub_agent", "two"), admissionResult("same", "agent-A", "batch", "group")}
	ledger := []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-A", BatchID: "batch", Group: "group"}, {ParentCallID: "same", AgentID: "agent-A", BatchID: "batch", Group: "group"}}
	events := replayEventsWithLedger(t, msgs, ledger)
	if got := len(eventsOfType(events, output.EventTypeDelegationAccepted)); got != 2 {
		t.Fatalf("accepted events = %d, want 2 explicit admissions", got)
	}
	if got := len(eventsOfType(events, output.EventTypeDelegationComplete)); got != 0 {
		t.Fatalf("complete events = %d, ambiguous ledger entries fabricated results: %d", got, got)
	}
}

func TestReplayLedgerDoesNotFinishUnownedDuplicateResult(t *testing.T) {
	msgs := []agent.Message{delegateCall("same", "sub_agent", "completed"), admissionResult("same", "agent-A", "", ""), {Role: agent.MessageRoleTool, ToolCallID: "same", Name: "sub_agent", Content: `{"output":"finished"}`}, delegateCall("same", "sub_agent", "orphan"), {Role: agent.MessageRoleTool, ToolCallID: "same", Name: "sub_agent", Content: `{"output":"extra"}`}}
	events := replayEventsWithLedger(t, msgs, []agent.SubAgentLedgerEntry{{ParentCallID: "same", AgentID: "agent-A"}})
	if got := len(eventsOfType(events, output.EventTypeDelegationComplete)); got != 1 {
		t.Fatalf("completion events = %d, want paired result only", got)
	}
	if got := len(eventsOfType(events, output.EventTypeDelegationFailed)); got != 0 {
		t.Fatalf("orphan finish generated failures = %d, want 0", got)
	}
	finishes := eventsOfType(events, output.EventTypeToolCallFinished)
	if len(finishes) != 1 || finishes[0].Payload.(output.ToolCallFinishedEvent).Result != msgs[1].Content {
		t.Fatalf("tool finishes = %+v, want only accepted occurrence finish", finishes)
	}
}

func TestReplayExplicitIdentityWinsConflictingPayloadAndRetention(t *testing.T) {
	msg := agent.Message{Role: agent.MessageRoleTool, ToolCallID: "identity", Name: "sub_agent", Content: `{"output":"","status":"failed","reason":"payload failure","continuation":{"agent_id":"payload-agent"}}`, Retention: &agent.MessageRetention{Status: "failed", AgentID: "retained-agent"}, DelegationAdmission: &tool.DelegationAdmission{Status: "accepted", AgentID: "authoritative-agent", BatchID: "batch", Group: "group"}}
	events := replayEvents(t, []agent.Message{delegateCall("identity", "sub_agent", "task"), msg})
	accepted := eventsOfType(events, output.EventTypeDelegationAccepted)
	if len(accepted) != 1 || accepted[0].Payload.(output.DelegationAcceptedEvent).AgentID != "authoritative-agent" {
		t.Fatalf("accepted identity = %+v", accepted)
	}
	failed := eventsOfType(events, output.EventTypeDelegationFailed)
	if len(failed) != 1 {
		t.Fatalf("failed events = %d, want 1", len(failed))
	}
	p := failed[0].Payload.(output.DelegationFailedEvent)
	if p.AgentID != "authoritative-agent" || p.Error != "payload failure" {
		t.Fatalf("failed identity/error = %+v", p)
	}
}

func TestReplayQueuedLedgerHasNoSyntheticTerminal(t *testing.T) {
	msgs := []agent.Message{delegateCall("queued", "sub_agent", "task"), ackResult(t, "queued", "sub_agent", "agent-Q", "queued")}
	events := replayEventsWithLedger(t, msgs, []agent.SubAgentLedgerEntry{{ParentCallID: "queued", AgentID: "agent-Q"}})
	if got := len(eventsOfType(events, output.EventTypeDelegationQueued)); got != 1 {
		t.Fatalf("queued lifecycle events = %d, want 1", got)
	}
	if got := len(eventsOfType(events, output.EventTypeDelegationFailed)) + len(eventsOfType(events, output.EventTypeDelegationComplete)); got != 0 {
		t.Fatalf("synthetic terminal events = %d, want 0", got)
	}
}

func TestReplayLegacyPreparationFailureWithoutStart(t *testing.T) {
	bad := agent.Message{Role: agent.MessageRoleTool, ToolCallID: "prep", Name: "sub_agent", Content: `{"output":"","status":"failed","reason":"setup failed"}`, Retention: &agent.MessageRetention{Status: "failed", AgentID: "misleading"}}
	events := replayEvents(t, []agent.Message{delegateCall("prep", "sub_agent", "setup"), bad})
	for _, typ := range []string{output.EventTypeDelegationAccepted, output.EventTypeDelegationQueued, output.EventTypeDelegationStarted, output.EventTypeDelegationFailed, output.EventTypeDelegationComplete} {
		if got := len(eventsOfType(events, typ)); got != 0 {
			t.Fatalf("%s events = %d, want 0 for a legacy setup failure", typ, got)
		}
	}
}

func TestReplayUnknownStatusIgnoresRetentionIdentity(t *testing.T) {
	msg := ackResult(t, "status", "sub_agent", "metadata-agent", "running")
	msg.DelegationAdmission = nil
	msg.Retention.AgentID = "retained-agent"
	msg.Retention.Status = "unknown"
	events := replayEvents(t, []agent.Message{delegateCall("status", "sub_agent", "task"), msg})
	for _, typ := range []string{output.EventTypeDelegationAccepted, output.EventTypeDelegationQueued, output.EventTypeDelegationStarted, output.EventTypeDelegationFailed, output.EventTypeDelegationComplete} {
		if got := len(eventsOfType(events, typ)); got != 0 {
			t.Fatalf("unknown status emitted %s (%d events)", typ, got)
		}
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
	assertReplayGolden(t, "replay_blocking_delegates.golden", replayEvents(t, msgs))
}
