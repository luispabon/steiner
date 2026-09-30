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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, e := range unansweredResultEnvelopes(tc.conv) {
				got = append(got, e.AgentID)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ids = %v, want %v", got, tc.want)
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
			if _, err := s.driverRun(context.Background(), in); err != nil {
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
