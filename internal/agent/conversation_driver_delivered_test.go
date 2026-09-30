package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// drainDeliveredEvents returns the delivered events buffered on the harness.
func drainDeliveredEvents(h *driverHarness) []output.SubAgentsDeliveredEvent {
	var got []output.SubAgentsDeliveredEvent
	for {
		select {
		case e := <-h.events:
			if e.Type == output.EventTypeSubAgentsDelivered {
				got = append(got, e.Payload.(output.SubAgentsDeliveredEvent))
			}
		default:
			return got
		}
	}
}

func TestConversationDriverDeliveryEmitsSubAgentsDelivered(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		completions []SubAgentCompletion
		submit      string
		wantIDs     []string
	}{
		{name: "completions in order", completions: []SubAgentCompletion{completionFor(1, "a"), completionFor(2, "b")}, wantIDs: []string{"a", "b"}},
		{name: "completions with user text", completions: []SubAgentCompletion{completionFor(1, "a")}, submit: "hi", wantIDs: []string{"a"}},
		{name: "user text only", submit: "hi"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newDriverHarness(t, nil, nil)
			if tc.submit != "" {
				h.d.Submit(tc.submit, nil, SubmitMeta{})
			}
			if len(tc.completions) > 0 {
				h.d.DeliverCompletions(tc.completions)
			}
			h.start()
			if tc.submit == "" {
				h.clock.fire()
			}
			call := h.nextRun()
			call.finish()
			h.waitQuiescent()

			got := drainDeliveredEvents(h)
			if len(tc.wantIDs) == 0 {
				if len(got) != 0 {
					t.Fatalf("delivered events = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("delivered events = %d, want 1", len(got))
			}
			if len(got[0].Items) != len(tc.wantIDs) {
				t.Fatalf("items = %+v, want ids %v", got[0].Items, tc.wantIDs)
			}
			for i, id := range tc.wantIDs {
				it := got[0].Items[i]
				if it.AgentID != id || it.ParentCallID != "call-"+id || it.AgentType != "code" || it.Status != "complete" {
					t.Errorf("item %d = %+v, want agent %q", i, it, id)
				}
			}
		})
	}
}
