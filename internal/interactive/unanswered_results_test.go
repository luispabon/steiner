package interactive

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

func envelopeFor(id string) string {
	return agent.RenderSubAgentResultEnvelope(agent.SubAgentCompletion{
		ParentCallID: "call-" + id, AgentID: id, AgentType: "explore", Status: "complete",
		ObjectivePreview: "obj", Body: `{"output":"x"}`,
	})
}

func TestUnansweredResultEnvelopes(t *testing.T) {
	t.Parallel()
	user := func(c string) agent.Message { return agent.Message{Role: agent.MessageRoleUser, Content: c} }
	asst := func(c string) agent.Message { return agent.Message{Role: agent.MessageRoleAssistant, Content: c} }
	tests := []struct {
		name string
		conv []agent.Message
		want []string
	}{
		{name: "empty"},
		{name: "no envelopes", conv: []agent.Message{user("hi"), asst("hello"), user("more")}},
		{name: "answered", conv: []agent.Message{user("hi"), asst("ok"), user(envelopeFor("a")), asst("thanks")}},
		{name: "unanswered", conv: []agent.Message{user("hi"), asst("ok"), user(envelopeFor("a") + "\n\n" + envelopeFor("b"))}, want: []string{"a", "b"}},
		{name: "unanswered without prior assistant", conv: []agent.Message{user(envelopeFor("a"))}, want: []string{"a"}},
		{name: "envelope then user text only", conv: []agent.Message{asst("ok"), user(envelopeFor("a")), user("just text")}, want: []string{"a"}},
		{name: "text after answered results", conv: []agent.Message{asst("ok"), user(envelopeFor("a")), asst("done"), user("text")}},
		{
			name: "interrupted partial does not answer results",
			conv: []agent.Message{user("hi"), asst("ok"), user(envelopeFor("a")), {Role: agent.MessageRoleAssistant, Content: "I looked at", Incomplete: true}},
			want: []string{"a"},
		},
		{
			name: "complete answer after an earlier partial answers results",
			conv: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "cut off", Incomplete: true}, user(envelopeFor("a")), asst("done")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, e := range unansweredResultEnvelopes(tc.conv) {
				got = append(got, e.result.AgentID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUnansweredBoundaryTreatsRestoredPartialAsInterrupted proves the shape a
// provider round trip restores (raw text, no interruption note, nonzero turn,
// Incomplete set) is not mistaken for an answer, exactly like a partial the run
// committed itself.
func TestUnansweredBoundaryTreatsRestoredPartialAsInterrupted(t *testing.T) {
	t.Parallel()
	committed := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "hi"},
		{Role: agent.MessageRoleAssistant, Content: "ok"},
		{Role: agent.MessageRoleUser, Content: envelopeFor("a")},
		{Role: agent.MessageRoleAssistant, Content: "I looked at", Incomplete: true},
	}
	restored := append([]agent.Message(nil), committed...)
	restored[3].Turn = 2

	want := unansweredBoundary(committed)
	if got := unansweredBoundary(restored); got != want {
		t.Fatalf("boundary of the restored partial = %d, want %d", got, want)
	}
	var ids []string
	for _, e := range unansweredResultEnvelopes(restored) {
		ids = append(ids, e.result.AgentID)
	}
	if !reflect.DeepEqual(ids, []string{"a"}) {
		t.Errorf("unanswered ids = %v, want the envelope the partial did not answer", ids)
	}
	if got := unansweredResultsReason(restored, "run failed"); got != interruptedUnansweredReason {
		t.Errorf("reason = %q, want %q", got, interruptedUnansweredReason)
	}
}

func TestUnansweredResultsReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		conv []agent.Message
		want string
	}{
		{
			name: "interrupted partial tail",
			conv: []agent.Message{
				{Role: agent.MessageRoleAssistant, Content: "ok"},
				{Role: agent.MessageRoleUser, Content: envelopeFor("a")},
				{Role: agent.MessageRoleAssistant, Content: "I looked at", Incomplete: true},
			},
			want: interruptedUnansweredReason,
		},
		{
			name: "failed before any response",
			conv: []agent.Message{
				{Role: agent.MessageRoleAssistant, Content: "ok"},
				{Role: agent.MessageRoleUser, Content: envelopeFor("a")},
			},
			want: "provider error",
		},
		{
			name: "earlier partial is answered by a later complete response",
			conv: []agent.Message{
				{Role: agent.MessageRoleAssistant, Content: "cut off", Incomplete: true},
				{Role: agent.MessageRoleUser, Content: envelopeFor("a")},
				{Role: agent.MessageRoleAssistant, Content: "done"},
			},
			want: "provider error",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := unansweredResultsReason(tc.conv, "provider error"); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDriverRunEmitsUnansweredResultsOnError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		err       error
		wantEvent bool
	}{
		{name: "error", err: errors.New("boom"), wantEvent: true},
		{name: "cancel", err: context.Canceled, wantEvent: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var events []output.Event
			s := testNewSession(t, Dependencies{
				BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
			})
			s.SetRunner(&inputRunner{run: func(context.Context, RunInput) (RunResult, error) {
				return RunResult{}, tc.err
			}})
			in := agent.DriverRunInput{Conversation: []agent.Message{
				{Role: agent.MessageRoleAssistant, Content: "ok"},
				{Role: agent.MessageRoleUser, Content: envelopeFor("a")},
			}}
			if _, err := s.driverRun(context.Background(), in, ""); err != nil {
				t.Fatalf("driverRun: %v", err)
			}
			got := eventsOfType(events, output.EventTypeSubAgentResultsUnanswered)
			if !tc.wantEvent {
				if len(got) != 0 {
					t.Fatalf("events = %d, want none", len(got))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("events = %d, want 1", len(got))
			}
			p := got[0].Payload.(output.SubAgentResultsUnansweredEvent)
			if !reflect.DeepEqual(p.AgentIDs, []string{"a"}) || p.Reason == "" {
				t.Errorf("payload = %+v", p)
			}
		})
	}
}

// TestDriverRunNamesInterruptedResponseInUnansweredResults proves the driver
// passes the committed partial conversation through to the warning, so its
// reason says the response was interrupted instead of quoting the stream error.
func TestDriverRunNamesInterruptedResponseInUnansweredResults(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})
	s.SetRunner(&inputRunner{run: func(context.Context, RunInput) (RunResult, error) {
		return RunResult{Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: "ok"},
			{Role: agent.MessageRoleUser, Content: envelopeFor("a")},
			{Role: agent.MessageRoleAssistant, Content: "I looked at", Incomplete: true},
		}}, errors.New("stream interrupted")
	}})
	in := agent.DriverRunInput{Conversation: []agent.Message{
		{Role: agent.MessageRoleAssistant, Content: "ok"},
	}}
	if _, err := s.driverRun(context.Background(), in, ""); err != nil {
		t.Fatalf("driverRun: %v", err)
	}
	got := eventsOfType(events, output.EventTypeSubAgentResultsUnanswered)
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	p := got[0].Payload.(output.SubAgentResultsUnansweredEvent)
	if p.Reason != interruptedUnansweredReason {
		t.Fatalf("reason = %q, want %q", p.Reason, interruptedUnansweredReason)
	}
}

// TestUnansweredElidedResultEnvelopeKeepsIdentity proves an unanswered result
// whose body elision replaced with a recall marker is still recognised, with
// its agent identity and status read from the envelope attributes instead of a
// body that no longer decodes.
func TestUnansweredElidedResultEnvelopeKeepsIdentity(t *testing.T) {
	t.Parallel()
	env := elidedResultEnvelope(t, "call-a", "a", "explore", "complete")
	conv := []agent.Message{
		{Role: agent.MessageRoleAssistant, Content: "ok"},
		{Role: agent.MessageRoleUser, Content: env},
	}
	got := unansweredResultEnvelopes(conv)
	if len(got) != 1 {
		t.Fatalf("envelopes = %d, want 1", len(got))
	}
	want := agent.ParsedSubAgentResult{
		AgentID:   "a",
		AgentType: "explore",
		Status:    "complete",
		CallID:    "call-a",
		Inner:     elidedResultInner("a"),
	}
	if got[0].result != want {
		t.Fatalf("result = %+v, want %+v", got[0].result, want)
	}
}
