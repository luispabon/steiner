package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestRosterAcceptedSameCallIDFailureReplacesFinishedEntry(t *testing.T) {
	t.Parallel()

	var r subAgentRoster
	for i, event := range []output.Event{
		ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "same-call", BatchID: "old-batch", AgentID: "child"}, Group: "old-group"}),
		ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "same-call", AgentID: "child"}, AgentType: "review"}),
		ev(output.DelegationCompleteEvent{DelegationOccurrence: output.DelegationOccurrence{AgentID: "child"}, AgentType: "review", Status: "completed", DurationMs: 100}),
		ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "same-call", BatchID: "new-batch", AgentID: "child"}, Group: "new-group"}),
		ev(output.DelegationFailedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "same-call", AgentID: "child"}, AgentType: "review", DurationMs: 50, Error: "replay failure"}),
	} {
		r.observe(event, int64(i+1)*1_000_000_000)
	}

	entry := r.entries["child"]
	if entry == nil {
		t.Fatal("missing child entry")
	}
	if entry.status != rosterFailed || entry.group != "new-group" {
		t.Fatalf("entry = %+v, want failed fresh same-call occurrence", entry)
	}
	if entry.startTime != 4_950_000_000 || entry.finishTime != 5_000_000_000 || entry.delivered {
		t.Errorf("timing/delivery = %d, %d, %t, want 4950000000, 5000000000, false", entry.startTime, entry.finishTime, entry.delivered)
	}
	if out := sidebarText(&r, 5); !strings.Contains(out, "new-group") || strings.Contains(out, "old-group") {
		t.Errorf("sidebar should show only the fresh occurrence group:\n%s", out)
	}
}

func TestRosterRepeatedAdmissionIsIdempotent(t *testing.T) {
	t.Parallel()

	var r subAgentRoster
	accepted := ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "call", BatchID: "batch", AgentID: "child"}, Group: "group"})
	r.observe(accepted, 1)
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "call", AgentID: "child"}, AgentType: "review"}), 2)
	before := *r.entries["child"]
	r.observe(accepted, 3)
	after := *r.entries["child"]

	if after != before {
		t.Fatalf("repeated admission changed entry: before %+v, after %+v", before, after)
	}
}

func TestRosterActiveSameCallIDDifferentBatchIsProtected(t *testing.T) {
	t.Parallel()

	var r subAgentRoster
	r.observe(ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "same-call", BatchID: "old-batch", AgentID: "child"}, Group: "old-group"}), 1)
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "same-call", AgentID: "child"}, AgentType: "review"}), 2)
	r.observe(ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "same-call", BatchID: "new-batch", AgentID: "child"}, Group: "new-group"}), 3)
	r.observe(ev(output.DelegationFailedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "same-call", AgentID: "child"}, AgentType: "review", DurationMs: 10, Error: "active overwrite"}), 4)

	entry := r.entries["child"]
	if entry.status != rosterRunning || entry.group != "old-group" {
		t.Fatalf("entry after active same-call replacement = %+v, want old running identity", entry)
	}

	// The original delivery still settles the active run, but must not borrow the refused batch identity.
	r.observe(ev(output.SubAgentsDeliveredEvent{Items: []output.DeliveredSubAgent{{
		AgentID: "child", AgentType: "review", Status: "complete", ParentCallID: "same-call", DurationMs: 25,
	}}}), 5)
	if entry.status != rosterDone || entry.group != "old-group" || !entry.delivered {
		t.Fatalf("entry after original delivery = %+v, want done with old identity", entry)
	}
}

func sidebarText(r *subAgentRoster, now int64) string {
	return stripANSI(strings.Join(rosterSidebar(r.snapshot(), now).subAgentsSection(60), "\n"))
}
