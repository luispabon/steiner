package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestSidebarAcceptedGroupIdentity(t *testing.T) {
	var r subAgentRoster
	// Queue/start events precede tool finished metadata on this lifecycle path.
	for _, call := range []struct{ callID, agentID string }{
		{"call-a", "same-a"}, {"call-b", "same-b"}, {"call-c", "other"},
	} {
		r.observe(ev(output.DelegationQueuedEvent{CallID: call.callID, AgentID: call.agentID, AgentType: "review"}), 1)
		r.observe(ev(output.DelegationStartedEvent{CallID: call.callID, AgentID: call.agentID, AgentType: "review"}), 2)
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
	r.observe(ev(output.DelegationStartedEvent{CallID: "unknown", AgentID: "unknown-child", AgentType: "review"}), 4)

	entries := r.snapshot()
	if len(entries) != 4 {
		t.Fatalf("roster entries = %d, want 4 including unknown lifecycle", len(entries))
	}
	unknown := r.entries["unknown-child"]
	if unknown == nil || unknown.accepted || unknown.group != "" || unknown.batchID != "" {
		t.Fatalf("unknown entry = %+v, want visible ungrouped unknown", unknown)
	}
	if got := r.entries["same-a"].group + "/" + r.entries["same-a"].batchID; got != "review/batch-a" {
		t.Errorf("accepted identity = %q, want review/batch-a", got)
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

func TestRosterUnknownLegacyLifecycleAndDelivery(t *testing.T) {
	var r subAgentRoster
	r.observe(ev(output.DelegationQueuedEvent{AgentID: "queued", AgentType: "review"}), 1)
	r.observe(ev(output.DelegationStartedEvent{AgentID: "running", AgentType: "code"}), 2)
	r.observe(ev(output.DelegationFailedEvent{AgentID: "failed", AgentType: "code", Error: "legacy"}), 3)
	r.observe(ev(output.SubAgentsDeliveredEvent{Items: []output.DeliveredSubAgent{{AgentID: "lost", AgentType: "explore", Status: "lost"}}}), 4)
	for id, status := range map[string]string{"queued": rosterQueued, "running": rosterRunning, "failed": rosterFailed, "lost": rosterLost} {
		e := r.entries[id]
		if e == nil || e.status != status || e.accepted || e.group != "" || e.batchID != "" {
			t.Errorf("legacy %s entry = %+v, want visible ungrouped status %s", id, e, status)
		}
	}
	out := stripANSI(strings.Join(rosterSidebar(r.snapshot(), 4).subAgentsSection(60), "\\n"))
	for _, id := range []string{"queued", "running", "failed", "lost"} {
		if !strings.Contains(out, id) {
			t.Errorf("sidebar missing unknown %s:\\n%s", id, out)
		}
	}
}

func TestRosterAcceptedRunResetAndRejectedFollowup(t *testing.T) {
	var r subAgentRoster
	r.observe(ev(output.DelegationAcceptedEvent{CallID: "original", AgentID: "child", BatchID: "old", Group: "old-group"}), 1)
	r.observe(ev(output.DelegationStartedEvent{CallID: "original", AgentID: "child", AgentType: "code"}), 2)
	r.observe(ev(output.DelegationAcceptedEvent{CallID: "fresh", AgentID: "child", BatchID: "fresh-batch", Group: "fresh-group"}), 3)
	r.observe(ev(output.DelegationStartedEvent{CallID: "fresh", AgentID: "child", AgentType: "code"}), 4)
	entry := r.entries["child"]
	if entry.group != "fresh-group" || entry.batchID != "fresh-batch" {
		t.Fatalf("fresh accepted identity = %q/%q, want fresh-group/fresh-batch", entry.group, entry.batchID)
	}

	// Rejected lifecycle never settles the accepted current run, including failed events without an occurrence ID.
	for _, failure := range []output.DelegationFailedEvent{
		{CallID: "rejected", AgentID: "child", AgentType: "code", Error: "busy"},
		{AgentID: "child", AgentType: "code", Error: "legacy busy"},
	} {
		r.observe(ev(output.DelegationQueuedEvent{CallID: failure.CallID, AgentID: "child", AgentType: "code"}), 5)
		r.observe(ev(output.DelegationStartedEvent{CallID: failure.CallID, AgentID: "child", AgentType: "code"}), 6)
		r.observe(ev(failure), 7)
		if entry.status != rosterRunning || entry.currentCallID != "fresh" || entry.group != "fresh-group" || entry.batchID != "fresh-batch" {
			t.Fatalf("rejected follow-up changed accepted running entry: %+v", entry)
		}
	}

	// Accepted lifecycle may arrive before its acceptance event.
	r.observe(ev(output.DelegationStartedEvent{CallID: "late", AgentID: "late-child", AgentType: "review"}), 8)
	r.observe(ev(output.DelegationAcceptedEvent{CallID: "late", AgentID: "late-child", BatchID: "late-batch", Group: "late-group"}), 9)
	if e := r.entries["late-child"]; e == nil || e.group != "late-group" || e.batchID != "late-batch" || !e.accepted {
		t.Fatalf("late accepted identity = %+v", e)
	}

	// Accepted follow-up without a group resets prior identity in the live row.
	r.observe(ev(output.DelegationAcceptedEvent{CallID: "ungrouped", AgentID: "child", BatchID: "new-batch"}), 10)
	r.observe(ev(output.DelegationStartedEvent{CallID: "ungrouped", AgentID: "child", AgentType: "code"}), 11)
	if entry.group != "" || entry.batchID != "new-batch" || !entry.accepted {
		t.Fatalf("ungrouped current run identity = %+v", entry)
	}
	state := rosterSidebar(r.snapshot(), 11)
	out := stripANSI(strings.Join(state.subAgentsSection(60), "\\n"))
	if strings.Contains(out, "old-group") || strings.Contains(out, "fresh-group") || !strings.Contains(out, "late-group") {
		t.Fatalf("rendered current-run groups incorrect:\\n%s", out)
	}
}
