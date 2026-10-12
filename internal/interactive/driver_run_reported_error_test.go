package interactive

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

// TestDriverRunReportsStopReasonOnce proves the ReportedError marker suppresses
// only driverRun's duplicate stop-reason event. Unanswered-result handling still
// runs for both errors, and an unmarked error keeps its stop-reason message.
func TestDriverRunReportsStopReasonOnce(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	tests := []struct {
		name      string
		err       error
		wantStops int
	}{
		{name: "unmarked error", err: boom, wantStops: 1},
		{name: "runner-reported error", err: &agent.ReportedError{Err: boom}, wantStops: 0},
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

			stops := eventsOfType(events, output.EventTypeStopReason)
			if len(stops) != tc.wantStops {
				t.Fatalf("stop-reason events = %d, want %d", len(stops), tc.wantStops)
			}
			if tc.wantStops == 1 {
				p := stops[0].Payload.(output.StopReasonEvent)
				if p.Error != boom.Error() {
					t.Errorf("stop-reason error = %q, want %q", p.Error, boom.Error())
				}
			}

			if got := eventsOfType(events, output.EventTypeSubAgentResultsUnanswered); len(got) != 1 {
				t.Fatalf("unanswered-results events = %d, want 1", len(got))
			}
		})
	}
}
