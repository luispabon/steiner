package tui

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

// A cancellation finalises a delegation card. Events for that same occurrence
// arriving afterwards must not restart it, and its late terminal event must not
// leave the card stuck active.
func TestCancelledDelegationIgnoresLateOccurrenceEvents(t *testing.T) {
	t.Parallel()
	occ := callOcc("call-1", "agent-1")
	m := newModel(Config{}, nil)

	m.applyEvent(output.NewDelegationStartedEvent(occ, "task", "", "explore"))
	m.applyEvent(output.NewStopReasonEvent(1, "cancelled", nil))

	loc, found := m.content.findDelegation("agent-1")
	if !found || loc.dd == nil {
		t.Fatal("agent-1 delegation not found after cancellation")
	}
	if loc.dd.status != "failed" || !loc.dd.finalizedByCancellation {
		t.Fatalf("after cancellation status = %q finalizedByCancellation = %v, want failed and true", loc.dd.status, loc.dd.finalizedByCancellation)
	}
	if m.content.HasActiveDelegations() || m.sessionBusy() {
		t.Fatal("cancelled delegation is still active")
	}

	m.applyEvent(output.NewDelegationStartedEvent(occ, "task", "", "explore"))
	if m.content.HasActiveDelegations() {
		t.Error("late started event re-registered a cancelled delegation as active")
	}
	if loc.dd.status != "failed" {
		t.Errorf("late started event status = %q, want failed", loc.dd.status)
	}
	if m.sessionBusy() {
		t.Error("sessionBusy = true after late started event for a cancelled occurrence")
	}

	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		DelegationOccurrence: occ,
		Status:               "completed",
	}))
	if m.content.HasActiveDelegations() || m.sessionBusy() {
		t.Error("late terminal event left the cancelled delegation active")
	}
	if loc.dd.status != "failed" {
		t.Errorf("status after late terminal event = %q, want failed", loc.dd.status)
	}
}

// A terminal event settles a delegation. A late cache-waiting event for the
// same occurrence must not reactivate it or set cache-waiting state on a
// finished card.
func TestLateCacheWaitingAfterTerminalDoesNotReactivate(t *testing.T) {
	t.Parallel()
	occ := callOcc("call-1", "agent-1")
	tests := []struct {
		name       string
		terminal   output.Event
		wantStatus string
	}{
		{
			name: "complete",
			terminal: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
				DelegationOccurrence: occ,
				Status:               "completed",
			}),
			wantStatus: "complete",
		},
		{
			name: "failed",
			terminal: output.NewDelegationFailedEvent(output.DelegationFailedParams{
				DelegationOccurrence: occ,
				Error:                "child failed",
			}),
			wantStatus: "failed",
		},
		{
			name:       "cancelled",
			terminal:   output.NewStopReasonEvent(1, "cancelled", nil),
			wantStatus: "failed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m.applyEvent(output.NewDelegationStartedEvent(occ, "task", "", "explore"))
			m.applyEvent(tc.terminal)

			loc, found := m.content.findDelegation("agent-1")
			if !found || loc.dd == nil {
				t.Fatal("agent-1 delegation not found after terminal event")
			}
			if loc.dd.status != tc.wantStatus {
				t.Fatalf("status after terminal = %q, want %q", loc.dd.status, tc.wantStatus)
			}
			if m.content.HasActiveDelegations() || m.sessionBusy() {
				t.Fatal("terminal delegation is still active")
			}

			m.applyEvent(output.NewDelegationCacheWaitingEvent(occ, time.Unix(1_800_000_000, 0)))
			if m.content.HasActiveDelegations() {
				t.Error("late cache-waiting event re-registered a finished delegation as active")
			}
			if m.sessionBusy() {
				t.Error("sessionBusy = true after late cache-waiting event for a finished occurrence")
			}
			if loc.dd.cacheWaiting {
				t.Error("late cache-waiting event set cacheWaiting on a finished card")
			}
			if loc.dd.status != tc.wantStatus {
				t.Errorf("status after late cache-waiting event = %q, want %q", loc.dd.status, tc.wantStatus)
			}
		})
	}
}
