package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestSidebarAcceptedGroupIdentity(t *testing.T) {
	var r subAgentRoster
	// Queue/start events precede tool finished metadata on this lifecycle path.
	for _, call := range []struct{ callID, batchID, agentID string }{
		{"call-a", "batch-a", "same-a"}, {"call-b", "batch-a", "same-b"}, {"call-c", "batch-b", "other"},
	} {
		occ := output.DelegationOccurrence{CallID: call.callID, BatchID: call.batchID, AgentID: call.agentID}
		r.observe(ev(output.DelegationQueuedEvent{DelegationOccurrence: occ, AgentType: "review"}), 1)
		r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: occ, AgentType: "review"}), 2)
	}
	for _, admission := range []output.DelegationAdmission{
		{Status: "accepted", AgentID: "same-a", BatchID: "batch-a", Group: "review"},
		{Status: "accepted", AgentID: "same-b", BatchID: "batch-a", Group: "review"},
		{Status: "accepted", AgentID: "other", BatchID: "batch-b", Group: "review"},
	} {
		callID := map[string]string{"same-a": "call-a", "same-b": "call-b", "other": "call-c"}[admission.AgentID]
		r.observe(ev(output.ToolCallFinishedEvent{CallID: callID, DelegationAdmission: &admission}), 3)
	}
	r.observe(ev(output.ToolCallQueuedEvent{CallID: "unknown", Arguments: map[string]any{"group": "review"}}), 3)
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "unknown", AgentID: "unknown-child"}, AgentType: "review"}), 4)

	entries := r.snapshot()
	if len(entries) != 4 {
		t.Fatalf("roster entries = %d, want 4 including unknown lifecycle", len(entries))
	}
	unknown := r.entries["unknown-child"]
	if unknown == nil || unknown.group != "" {
		t.Fatalf("unknown entry = %+v, want visible ungrouped unknown", unknown)
	}
	if got := r.entries["same-a"].group; got != "review" {
		t.Errorf("accepted group = %q, want review", got)
	}
	state := rosterSidebar(entries, 3)
	out := stripANSI(strings.Join(state.subAgentsSection(60), "\n"))
	if strings.Count(out, "┌ review") != 2 {
		t.Errorf("group headings = %d, want separate heading per batch:\n%s", strings.Count(out, "┌ review"), out)
	}
	if !strings.Contains(out, "unknown...") {
		t.Errorf("unknown lifecycle missing from ungrouped roster:\n%s", out)
	}
}

func TestRosterPreparationFailureUsesAdmissionIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		accept output.Event
		callID string
	}{
		{"accepted event", ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "prep", BatchID: "batch", AgentID: "prepare"}, Group: "prepare-group"}), "prep"},
		{"accepted finish fallback", ev(output.ToolCallFinishedEvent{CallID: "prep", DelegationAdmission: &output.DelegationAdmission{Status: "accepted", AgentID: "prepare", BatchID: "batch", Group: "prepare-group"}}), "prep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r subAgentRoster
			r.observe(tc.accept, 1)
			r.observe(ev(output.DelegationFailedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: tc.callID, BatchID: "batch", AgentID: "prepare"}, AgentType: "code", Error: "preparation failed"}), 2)
			e := r.entries["prepare"]
			if e == nil || e.status != rosterFailed || e.group != "prepare-group" {
				t.Fatalf("preparation failure entry = %+v", e)
			}
			out := stripANSI(strings.Join(rosterSidebar(r.snapshot(), 2).subAgentsSection(60), "\\n"))
			if !strings.Contains(out, "prepare-group") || !strings.Contains(out, "prepare") {
				t.Fatalf("preparation failure missing from grouped sidebar:\\n%s", out)
			}
		})
	}
}

func TestRosterUnknownLegacyLifecycleAndDelivery(t *testing.T) {
	var r subAgentRoster
	r.observe(ev(output.DelegationQueuedEvent{DelegationOccurrence: output.DelegationOccurrence{AgentID: "queued"}, AgentType: "review"}), 1)
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{AgentID: "running"}, AgentType: "code"}), 2)
	r.observe(ev(output.SubAgentsDeliveredEvent{Items: []output.DeliveredSubAgent{{AgentID: "lost", AgentType: "explore", Status: "lost"}}}), 4)
	for id, status := range map[string]string{"queued": rosterQueued, "running": rosterRunning, "lost": rosterLost} {
		e := r.entries[id]
		if e == nil || e.status != status || e.group != "" {
			t.Errorf("legacy %s entry = %+v, want visible ungrouped status %s", id, e, status)
		}
	}
	out := stripANSI(strings.Join(rosterSidebar(r.snapshot(), 4).subAgentsSection(60), "\\n"))
	for _, id := range []string{"queued", "running", "lost"} {
		if !strings.Contains(out, id) {
			t.Errorf("sidebar missing unknown %s:\\n%s", id, out)
		}
	}
}

func TestRosterAcceptedRunResetAndRejectedFollowup(t *testing.T) {
	var r subAgentRoster
	r.observe(ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "original", BatchID: "old", AgentID: "child"}, Group: "old-group"}), 1)
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "original", BatchID: "old", AgentID: "child"}, AgentType: "code"}), 2)
	r.observe(ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "fresh", BatchID: "fresh-batch", AgentID: "child"}, Group: "fresh-group"}), 3)
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "fresh", BatchID: "fresh-batch", AgentID: "child"}, AgentType: "code"}), 4)
	entry := r.entries["child"]
	if entry.group != "fresh-group" {
		t.Fatalf("fresh accepted group = %q, want fresh-group", entry.group)
	}

	// Rejected lifecycle never settles the accepted current run, including failed events without an occurrence ID.
	for _, failure := range []output.DelegationFailedEvent{
		{DelegationOccurrence: output.DelegationOccurrence{CallID: "rejected", AgentID: "child"}, AgentType: "code", Error: "busy"},
		{DelegationOccurrence: output.DelegationOccurrence{AgentID: "child"}, AgentType: "code", Error: "legacy busy"},
	} {
		r.observe(ev(output.DelegationQueuedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: failure.CallID, AgentID: "child"}, AgentType: "code"}), 5)
		r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: failure.CallID, AgentID: "child"}, AgentType: "code"}), 6)
		r.observe(ev(failure), 7)
		if entry.status != rosterRunning || entry.group != "fresh-group" {
			t.Fatalf("rejected follow-up changed accepted running entry: %+v", entry)
		}
	}

	// Accepted lifecycle may arrive before its acceptance event.
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "late", BatchID: "late-batch", AgentID: "late-child"}, AgentType: "review"}), 8)
	r.observe(ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "late", BatchID: "late-batch", AgentID: "late-child"}, Group: "late-group"}), 9)
	if e := r.entries["late-child"]; e == nil || e.group != "late-group" {
		t.Fatalf("late accepted identity = %+v", e)
	}

	// Accepted follow-up without a group resets prior identity in the live row.
	r.observe(ev(output.DelegationAcceptedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "ungrouped", BatchID: "new-batch", AgentID: "child"}}), 10)
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: output.DelegationOccurrence{CallID: "ungrouped", BatchID: "new-batch", AgentID: "child"}, AgentType: "code"}), 11)
	if entry.group != "" || entry.status != rosterRunning {
		t.Fatalf("ungrouped current run identity = %+v", entry)
	}
	// Delayed delivery from old call must not replace current run identity or settle it.
	r.observe(ev(output.SubAgentsDeliveredEvent{Items: []output.DeliveredSubAgent{{AgentID: "child", AgentType: "code", Status: "complete", ParentCallID: "fresh", BatchID: "fresh-batch"}}}), 12)
	if entry.status != rosterRunning || entry.delivered || entry.group != "" {
		t.Fatalf("stale delivery changed current run: %+v", entry)
	}
	state := rosterSidebar(r.snapshot(), 11)
	out := stripANSI(strings.Join(state.subAgentsSection(60), "\\n"))
	if strings.Contains(out, "old-group") || strings.Contains(out, "fresh-group") || !strings.Contains(out, "late-group") {
		t.Fatalf("rendered current-run groups incorrect:\\n%s", out)
	}
}
