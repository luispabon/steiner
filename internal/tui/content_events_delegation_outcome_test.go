package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func fullOccurrence(callID, batchID, agentID string) output.DelegationOccurrence {
	return output.DelegationOccurrence{CallID: callID, BatchID: batchID, AgentID: agentID}
}

// replayAccepted feeds one accepted delegation as the replay path emits it:
// the tool call card, then Accepted, Queued and Started, each with full identity.
func replayAccepted(m *Model, tool, callID, batchID, agentID, group, task string) *delegationDisplayState {
	args := map[string]any{"type": "explore", "task": task}
	if group != "" {
		args["group"] = group
	}
	if tool == "follow_up" {
		args = map[string]any{"agent_id": agentID, "message": task}
	}
	m.applyEvent(output.NewToolCallStartedEvent(1, tool, callID, args))
	card := lastDelegationCard(&m.content)
	occ := fullOccurrence(callID, batchID, agentID)
	m.applyEvent(output.NewDelegationAcceptedEvent(occ, group))
	m.applyEvent(output.NewDelegationQueuedEvent(occ, "explore", task))
	m.applyEvent(output.NewDelegationStartedEvent(occ, task, "", "explore"))
	return card
}

func rejectCall(m *Model, tool, callID, message string) {
	m.applyEvent(output.NewToolCallFinishedEventWithAdmission(1, tool, callID, "", errors.New(message), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))
}

func delivered(items ...output.DeliveredSubAgent) output.Event {
	return output.Event{Type: output.EventTypeSubAgentsDelivered, Payload: output.SubAgentsDeliveredEvent{Items: items}}
}

func TestReplayRejectedFollowUpKeepsPriorRosterEntry(t *testing.T) {
	m := newIdentityTestModel()
	card := replayAccepted(m, "sub_agent", "original", "b1", "child", "prior", "original task")
	m.applyEvent(output.NewToolCallStartedEvent(1, "follow_up", "follow-up", map[string]any{"agent_id": "child", "message": "continue"}))
	rejectCall(m, "follow_up", "follow-up", "not admitted")
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: fullOccurrence("original", "b1", "child"), Status: "completed"}))

	if got := countDelegationCards(m.content.segments); got != 1 || card.status != "complete" || findDelegationSegment(m.content.segments, card) < 0 {
		t.Fatalf("cards = %d, original = %#v", got, card)
	}
	if got := countToolText(m.content.segments, "not admitted"); got != 1 {
		t.Fatalf("rejection text count = %d, want 1", got)
	}
	if e := m.roster.entries["child"]; len(m.roster.entries) != 1 || e == nil || e.status != rosterDone || e.group != "prior" {
		t.Fatalf("roster = %#v", m.roster.entries)
	}
	if out := sidebarText(&m.roster, 1); !strings.Contains(out, "prior") || strings.Count(out, "child") != 1 {
		t.Fatalf("sidebar:\n%s", out)
	}
}

func TestReplayReusedGroupNameRejectedLeavesAcceptedGroup(t *testing.T) {
	m := newIdentityTestModel()
	for _, id := range []string{"a", "b"} {
		replayAccepted(m, "sub_agent", "call-"+id, "b1", "agent-"+id, "review", "task "+id)
		m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: fullOccurrence("call-"+id, "b1", "agent-"+id), Status: "completed"}))
	}
	m.applyEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-c", subAgentArgs("review")))
	rejected := lastDelegationCard(&m.content)
	rejectCall(m, "sub_agent", "call-c", "group name already used")

	if findDelegationSegment(m.content.segments, rejected) >= 0 {
		t.Fatal("rejected card remains")
	}
	if len(m.content.segments) != 2 || m.content.segments[0].kind != segmentDelegationGroup || len(m.content.segments[0].delegGroupData.entries) != 2 {
		t.Fatalf("segments = %v", segmentKinds(m.content.segments))
	}
	if got := countToolText(m.content.segments, "group name already used"); got != 1 {
		t.Fatalf("rejection text count = %d, want 1", got)
	}
	if out := sidebarText(&m.roster, 1); strings.Count(out, "┌ review") != 1 || len(m.roster.entries) != 2 {
		t.Fatalf("sidebar:\n%s", out)
	}
}

func TestReplayLostDeliverySettlesCardAndRosterRow(t *testing.T) {
	m := newIdentityTestModel()
	card := replayAccepted(m, "sub_agent", "call", "b1", "child", "g", "task")
	m.applyEvent(delivered(output.DeliveredSubAgent{AgentID: "child", AgentType: "explore", Status: "lost", ParentCallID: "call", BatchID: "b1"}))

	if card.status != "failed" || card.resultStatus != "lost" || findDelegationSegment(m.content.segments, card) < 0 {
		t.Fatalf("lost card = %#v", card)
	}
	if e := m.roster.entries["child"]; e == nil || e.status != rosterLost {
		t.Fatalf("roster = %#v", m.roster.entries)
	}
	if out := sidebarText(&m.roster, 1); !strings.Contains(out, "child") {
		t.Fatalf("sidebar:\n%s", out)
	}
}

func TestReplayFollowUpReusingAgentIDBindsFreshCard(t *testing.T) {
	m := newIdentityTestModel()
	original := replayAccepted(m, "sub_agent", "original", "b1", "child", "old-group", "original task")
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: fullOccurrence("original", "b1", "child"), Status: "completed"}))
	fresh := replayAccepted(m, "follow_up", "fresh", "b2", "child", "fresh-group", "fresh work")
	m.applyEvent(delivered(output.DeliveredSubAgent{AgentID: "child", AgentType: "explore", Status: "complete", ParentCallID: "original"}))

	if fresh == original || fresh.status != "active" || original.status != "complete" {
		t.Fatalf("original/fresh = %#v / %#v", original, fresh)
	}
	if loc := m.content.activeDelegations["child"]; loc.dd != fresh {
		t.Fatalf("active card = %p, want fresh %p", loc.dd, fresh)
	}
	if e := m.roster.entries["child"]; e == nil || e.status != rosterRunning || e.group != "fresh-group" {
		t.Fatalf("stale delivery settled fresh run: %#v", e)
	}
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: fullOccurrence("fresh", "b2", "child"), Status: "completed"}))
	if fresh.status != "complete" || countDelegationCards(m.content.segments) != 2 {
		t.Fatalf("fresh = %#v cards = %d", fresh, countDelegationCards(m.content.segments))
	}
	if out := sidebarText(&m.roster, 1); !strings.Contains(out, "fresh-group") || strings.Contains(out, "old-group") {
		t.Fatalf("sidebar:\n%s", out)
	}
}

func TestReplayQueuedStartedThenCompleteOrFail(t *testing.T) {
	m := newIdentityTestModel()
	cards := map[string]*delegationDisplayState{}
	for _, id := range []string{"ok", "bad"} {
		callID := "call-" + id
		m.applyEvent(output.NewToolCallStartedEvent(1, "sub_agent", callID, subAgentArgs("g")))
		cards[id] = lastDelegationCard(&m.content)
		occ := fullOccurrence(callID, "b1", id)
		m.applyEvent(output.NewDelegationAcceptedEvent(occ, "g"))
		m.applyEvent(output.NewDelegationQueuedEvent(occ, "explore", "task "+id))
		if e := m.roster.entries[id]; e == nil || e.status != rosterQueued {
			t.Fatalf("%s after queue = %#v", id, e)
		}
		m.applyEvent(output.NewDelegationStartedEvent(occ, "task "+id, "", "explore"))
		if e := m.roster.entries[id]; e == nil || e.status != rosterRunning || cards[id].status != "active" {
			t.Fatalf("%s after start = %#v card=%#v", id, e, cards[id])
		}
	}
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: fullOccurrence("call-ok", "b1", "ok"), Status: "completed"}))
	m.applyEvent(output.NewDelegationFailedEvent(output.DelegationFailedParams{DelegationOccurrence: fullOccurrence("call-bad", "b1", "bad"), Error: "boom"}))

	if cards["ok"].status != "complete" || cards["bad"].status != "failed" {
		t.Fatalf("cards = %q/%q", cards["ok"].status, cards["bad"].status)
	}
	if m.roster.entries["ok"].status != rosterDone || m.roster.entries["bad"].status != rosterFailed {
		t.Fatalf("roster = %#v", m.roster.entries)
	}
	if len(m.content.segments) != 1 || m.content.segments[0].kind != segmentDelegationGroup || len(m.content.segments[0].delegGroupData.entries) != 2 {
		t.Fatalf("segments = %v", segmentKinds(m.content.segments))
	}
}

func TestReplayDuplicateCallIDsAcrossBatchesKeepSeparateRosterRows(t *testing.T) {
	m := newIdentityTestModel()
	first := replayAccepted(m, "sub_agent", "same", "b1", "agent-1", "review", "one")
	second := replayAccepted(m, "sub_agent", "same", "b2", "agent-2", "review", "two")
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: fullOccurrence("same", "b2", "agent-2"), Status: "completed"}))

	if first == second || first.status != "active" || second.status != "complete" {
		t.Fatalf("cards = %#v / %#v", first, second)
	}
	if m.roster.entries["agent-1"].status != rosterRunning || m.roster.entries["agent-2"].status != rosterDone {
		t.Fatalf("roster = %#v", m.roster.entries)
	}
	if out := sidebarText(&m.roster, 1); strings.Count(out, "┌ review") != 2 {
		t.Fatalf("want one sidebar heading per batch:\n%s", out)
	}
}
