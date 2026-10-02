package interactive

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

var updateReplayGolden = flag.Bool("update-replay-golden", false, "rewrite replay golden files")

func replayGoldenLines(events []output.Event, t *testing.T) []string {
	t.Helper()
	var lines []string
	for _, e := range events {
		lines = append(lines, e.Type+" "+mustJSON(t, e.Payload))
	}
	return lines
}

func assertReplayGolden(t *testing.T, name string, events []output.Event) {
	t.Helper()
	got := strings.Join(replayGoldenLines(events, t), "\n") + "\n"
	path := filepath.Join("testdata", name)
	if *updateReplayGolden {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("replayed events differ from %s:\n%s", name, got)
	}
}

// assertFullOccurrences fails when a replayed delegation event lacks any part
// of its occurrence identity.
func assertFullOccurrences(t *testing.T, events []output.Event) {
	t.Helper()
	check := func(typ string, occ output.DelegationOccurrence) {
		if occ.CallID == "" || occ.BatchID == "" || occ.AgentID == "" {
			t.Errorf("%s occurrence incomplete: %+v", typ, occ)
		}
	}
	// Every occurrence's first lifecycle event must be Accepted, and it must
	// be accepted only once.
	seen := make(map[output.DelegationOccurrence]bool)
	lifecycle := func(typ string, occ output.DelegationOccurrence) {
		check(typ, occ)
		accepted := typ == output.EventTypeDelegationAccepted
		switch {
		case !seen[occ] && !accepted:
			t.Errorf("%s is the first lifecycle event of %+v, want accepted", typ, occ)
		case seen[occ] && accepted:
			t.Errorf("duplicate accepted for %+v", occ)
		}
		seen[occ] = true
	}
	for _, e := range events {
		switch p := e.Payload.(type) {
		case output.DelegationAcceptedEvent:
			lifecycle(e.Type, p.DelegationOccurrence)
		case output.DelegationQueuedEvent:
			lifecycle(e.Type, p.DelegationOccurrence)
		case output.DelegationStartedEvent:
			lifecycle(e.Type, p.DelegationOccurrence)
		case output.DelegationFailedEvent:
			lifecycle(e.Type, p.DelegationOccurrence)
		case output.DelegationCompleteEvent:
			lifecycle(e.Type, p.DelegationOccurrence)
		case output.SubAgentsDeliveredEvent:
			for _, item := range p.Items {
				if item.ParentCallID == "" || item.BatchID == "" || item.AgentID == "" {
					t.Errorf("delivered item incomplete: %+v", item)
				}
			}
		}
	}
}

func admittedAck(t *testing.T, callID, agentID, batch, status string) agent.Message {
	t.Helper()
	msg := ackResult(t, callID, "sub_agent", agentID, status)
	msg.DelegationAdmission = &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted, BatchID: batch, Group: "g", AgentID: agentID}
	return msg
}

// TestReplayAdmissionSessionMatchesGolden replays a session written with typed
// admissions: one running and one queued sub-agent in one batch, the first
// delivered later, the second still outstanding in the ledger. Live emits
// tool start, accepted, queued/started, parent finish, then the result
// delivery; the golden pins that order and the batch/agent identity.
func TestReplayAdmissionSessionMatchesGolden(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "go"},
		{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{
			{ID: "call-1", Name: "sub_agent", Arguments: map[string]any{"task": "first"}},
			{ID: "call-2", Name: "sub_agent", Arguments: map[string]any{"task": "second"}},
		}},
		admittedAck(t, "call-1", "agent-a", "batch-1", "running"),
		admittedAck(t, "call-2", "agent-b", "batch-1", "queued"),
		{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: resultEnvelope(t, "call-1", "completed", "found it", 1)},
	}
	ledger := []agent.SubAgentLedgerEntry{{AgentID: "agent-b", AgentType: "explore", ParentCallID: "call-2", BatchID: "batch-1", Group: "g"}}
	events := replayEventsWithLedger(t, msgs, ledger)
	assertFullOccurrences(t, events)
	assertReplayGolden(t, "replay_admission_session.golden", events)
}

// TestReplayLegacySessionSynthesisesOccurrences replays a pre-admission
// session: acks without admission or ledger, one resolved by an envelope and
// one never resolved. Batches are synthesised from the assistant message index.
func TestReplayLegacySessionSynthesisesOccurrences(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{
		delegateCall("call-1", "sub_agent", "first"),
		ackResult(t, "call-1", "sub_agent", "agent-a", "running"),
		delegateCall("call-2", "sub_agent", "second"),
		ackResult(t, "call-2", "sub_agent", "agent-b", "queued"),
		{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: resultEnvelope(t, "call-1", "completed", "found it", 1)},
	}
	events := replayEvents(t, msgs)
	assertFullOccurrences(t, events)
	assertReplayGolden(t, "replay_legacy_async_session.golden", events)
}

func TestReplayNonDelegationStatusFailedIsSuccess(t *testing.T) {
	t.Parallel()
	events := replayEvents(t, []agent.Message{
		{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c1", Name: "bash", Arguments: map[string]any{"command": "x"}}}},
		{Role: agent.MessageRoleTool, ToolCallID: "c1", Name: "bash", Content: `{"status":"failed","reason":"x"}`},
	})
	finished := eventsOfType(events, output.EventTypeToolCallFinished)
	if len(finished) != 1 {
		t.Fatalf("finished events = %d, want 1", len(finished))
	}
	if p := finished[0].Payload.(output.ToolCallFinishedEvent); p.Error != "" {
		t.Errorf("non-delegation result replayed as error %q, want success", p.Error)
	}
}

func TestReplayDelegationProjectionFailureIsError(t *testing.T) {
	t.Parallel()
	msg := admittedAck(t, "c1", "agent-a", "b1", "failed")
	msg.Content = agent.FailureBody("failed", "setup failed")
	msg.Retention = nil
	events := replayEvents(t, []agent.Message{delegateCall("c1", "sub_agent", "t"), msg})
	finished := eventsOfType(events, output.EventTypeToolCallFinished)
	if len(finished) != 1 {
		t.Fatalf("finished events = %d, want 1", len(finished))
	}
	if p := finished[0].Payload.(output.ToolCallFinishedEvent); p.Error != "setup failed" {
		t.Errorf("finish error = %q, want %q", p.Error, "setup failed")
	}
}

func TestReplayLegacyAcceptedOnlyForAdmittedChildren(t *testing.T) {
	t.Parallel()
	setupFailure := agent.Message{
		Role: agent.MessageRoleTool, ToolCallID: "c1", Name: "sub_agent",
		Content: mustJSON(t, tool.JSONEnvelope{OK: false, Error: &tool.JSONEnvelopeError{Kind: "setup", Message: "no worktree"}}),
	}
	tests := []struct {
		name         string
		msgs         []agent.Message
		wantAccepted int
	}{
		{"running ack with agent id", []agent.Message{delegateCall("c1", "sub_agent", "t"), ackResult(t, "c1", "sub_agent", "agent-a", "running")}, 1},
		{"blocking result with agent id", []agent.Message{delegateCall("c1", "sub_agent", "t"), {
			Role: agent.MessageRoleTool, ToolCallID: "c1", Name: "sub_agent",
			Content: mustJSON(t, agent.DelegationResultEnvelope{Output: "done", Status: "complete", Continuation: &agent.DelegationContinuation{AgentID: "agent-a"}}),
		}}, 1},
		{"setup failure envelope", []agent.Message{delegateCall("c1", "sub_agent", "t"), setupFailure}, 0},
		{"failed result without agent id", []agent.Message{delegateCall("c1", "sub_agent", "t"), {
			Role: agent.MessageRoleTool, ToolCallID: "c1", Name: "sub_agent",
			Content: mustJSON(t, agent.DelegationResultEnvelope{Output: "no worktree", Status: "failed"}),
		}}, 0},
		{"ack resolved by envelope", []agent.Message{
			delegateCall("c1", "sub_agent", "t"),
			ackResult(t, "c1", "sub_agent", "agent-a", "running"),
			{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: resultEnvelope(t, "c1", "completed", "ok", 1)},
		}, 1},
		{"envelope without ack", []agent.Message{
			{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: resultEnvelope(t, "c1", "completed", "ok", 1)},
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			events := replayEvents(t, tt.msgs)
			assertFullOccurrences(t, events)
			if got := len(eventsOfType(events, output.EventTypeDelegationAccepted)); got != tt.wantAccepted {
				t.Errorf("accepted events = %d, want %d", got, tt.wantAccepted)
			}
		})
	}
}
