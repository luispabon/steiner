package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestSidebarAcceptedGroupIdentity(t *testing.T) {
	var r subAgentRoster
	for _, admission := range []output.DelegationAdmission{
		{Status: "accepted", AgentID: "same-a", BatchID: "batch-a", Group: "review"},
		{Status: "accepted", AgentID: "same-b", BatchID: "batch-a", Group: "review"},
		{Status: "accepted", AgentID: "other", BatchID: "batch-b", Group: "review"},
	} {
		callID := admission.AgentID
		r.observe(ev(output.ToolCallFinishedEvent{CallID: callID, DelegationAdmission: &admission}), 1)
		r.observe(ev(output.DelegationStartedEvent{CallID: callID, AgentID: admission.AgentID, AgentType: "review"}), 2)
	}
	r.observe(ev(output.ToolCallQueuedEvent{CallID: "unknown", Arguments: map[string]any{"group": "review"}}), 3)
	r.observe(ev(output.DelegationStartedEvent{CallID: "unknown", AgentID: "unknown-child", AgentType: "review"}), 4)

	entries := r.snapshot()
	if len(entries) != 3 {
		t.Fatalf("accepted roster entries = %d, want 3", len(entries))
	}
	if got := entries[0].group + "/" + entries[0].batchID; got != "review/batch-a" {
		t.Errorf("first identity = %q, want review/batch-a", got)
	}
	if entries[0].accepted != true || entries[1].accepted != true || entries[2].accepted != true {
		t.Fatal("accepted entries lost their admission identity")
	}
	state := rosterSidebar(entries, 3)
	out := stripANSI(strings.Join(state.subAgentsSection(60), "\n"))
	if strings.Count(out, "┌ review") != 2 {
		t.Errorf("group headings = %d, want separate heading per batch:\n%s", strings.Count(out, "┌ review"), out)
	}
	if strings.Contains(out, "unknown-child") {
		t.Errorf("unknown lifecycle became roster membership:\n%s", out)
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

	// Rejected follow-up has no accepted event or accepted finish metadata.
	r.observe(ev(output.DelegationQueuedEvent{CallID: "rejected", AgentID: "child", AgentType: "code"}), 5)
	r.observe(ev(output.DelegationStartedEvent{CallID: "rejected", AgentID: "child", AgentType: "code"}), 6)
	r.observe(ev(output.DelegationFailedEvent{CallID: "rejected", AgentID: "child", AgentType: "code", Error: "busy"}), 7)
	if entry.status != rosterRunning || entry.group != "fresh-group" || entry.batchID != "fresh-batch" {
		t.Fatalf("rejected follow-up changed accepted running entry: %+v", entry)
	}

	// An accepted follow-up without a group explicitly clears prior membership.
	r.observe(ev(output.DelegationAcceptedEvent{CallID: "ungrouped", AgentID: "child", BatchID: "new-batch"}), 8)
	r.observe(ev(output.DelegationStartedEvent{CallID: "ungrouped", AgentID: "child", AgentType: "code"}), 9)
	if entry.group != "" || entry.batchID != "new-batch" || !entry.accepted {
		t.Fatalf("ungrouped current run identity = %+v", entry)
	}
}
