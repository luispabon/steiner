package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestDelegationRejectionIdentityKeepsAcceptedCardAndError(t *testing.T) {
	b := newGroupTestBuffer()
	occ := output.DelegationOccurrence{CallID: "accepted-call", BatchID: "accepted-batch", AgentID: "child"}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "accepted-call", subAgentArgs("accepted-group")))
	accepted := lastDelegationCard(b)
	b.AppendEvent(output.NewDelegationAcceptedEvent(occ, "accepted-group"))
	b.AppendEvent(output.NewDelegationStartedEvent(occ, "find files", "", "explore"))
	const rejection = "provider: current call rejected"
	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "accepted-call", "", errors.New(rejection), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))
	if b.activeDelegations["child"].dd != accepted || findDelegationSegment(b.segments, accepted) < 0 {
		t.Fatal("conflicting rejection removed or replaced accepted card")
	}
	if len(b.segments) != 2 || b.segments[0].kind != segmentDelegationGroup || accepted.acceptedGroup() != "accepted-group" || accepted.status != "active" {
		t.Fatalf("accepted card changed: segments=%v card=%#v", segmentKinds(b.segments), accepted)
	}
	if got := b.segments[len(b.segments)-1]; got.kind != segmentNotice || got.text != "sub_agent rejected: "+rejection {
		t.Fatalf("rejection evidence = %#v, want exact error %q", got, rejection)
	}
}

func TestDelegationRejectionIdentityLateRejectedCallDoesNotStealActiveChild(t *testing.T) {
	m := newIdentityTestModel()
	m.applyEvent(output.NewToolCallStartedEvent(1, "sub_agent", "original-call", map[string]any{"type": "explore", "task": "original task"}))
	m.applyEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: "original-call", BatchID: "old-batch", AgentID: "child"}, "old-group"))
	m.applyEvent(output.NewDelegationQueuedEvent(output.DelegationOccurrence{CallID: "original-call", BatchID: "old-batch", AgentID: "child"}, "explore", "original task"))
	m.applyEvent(output.NewDelegationStartedEvent(output.DelegationOccurrence{CallID: "original-call", BatchID: "old-batch", AgentID: "child"}, "original task", "", "explore"))
	original := m.content.activeDelegations["child"]
	originalCard := original.dd
	originalRoster := m.roster.entries["child"]
	if originalCard == nil || originalRoster == nil || originalRoster.status != rosterRunning {
		t.Fatalf("test setup missing active child: locator=%#v roster=%#v", original, originalRoster)
	}
	m.applyEvent(output.NewToolCallStartedEvent(1, "follow_up", "rejected-call", map[string]any{"agent_id": "child", "message": "rejected follow-up"}))
	m.applyEvent(output.NewToolCallFinishedEventWithAdmission(1, "follow_up", "rejected-call", "", errors.New("not admitted"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	m.applyEvent(output.NewDelegationStartedEvent(callOcc("rejected-call", "child"), "late rejected task", "", "explore"))
	got := m.content.activeDelegations["child"]
	if got.dd != originalCard || got.seg != original.seg {
		t.Fatalf("late rejected call stole active locator: got=%#v want=%#v", got, original)
	}
	if findDelegationSegment(m.content.segments, originalCard) < 0 || originalCard.parentCallID != "original-call" || originalCard.agentID != "child" || originalCard.acceptedGroup() != "old-group" || originalCard.status != "active" {
		t.Fatalf("late rejected event changed original card identity: %#v", originalCard)
	}
	if countDelegationCards(m.content.segments) != 1 {
		t.Fatalf("late rejected event created another card: %#v", m.content.segments)
	}
	if gotRoster := m.roster.entries["child"]; gotRoster != originalRoster || gotRoster.status != rosterRunning || gotRoster.group != "old-group" {
		t.Fatalf("late rejected event changed original roster: %#v", gotRoster)
	}
	if out := sidebarText(&m.roster, 1); !strings.Contains(out, "old-group") || strings.Count(out, "child") != 1 {
		t.Fatalf("sidebar should show the original run once:\n%s", out)
	}
}

func TestDelegationRejectionIdentityAllowsFreshAcceptedFollowUpAfterCompletion(t *testing.T) {
	m := newIdentityTestModel()
	m.applyEvent(output.NewToolCallStartedEvent(1, "sub_agent", "original-call", map[string]any{"type": "explore", "task": "original task"}))
	m.applyEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: "original-call", BatchID: "old-batch", AgentID: "child"}, "old-group"))
	m.applyEvent(output.NewDelegationQueuedEvent(output.DelegationOccurrence{CallID: "original-call", BatchID: "old-batch", AgentID: "child"}, "explore", "original task"))
	m.applyEvent(output.NewDelegationStartedEvent(output.DelegationOccurrence{CallID: "original-call", BatchID: "old-batch", AgentID: "child"}, "original task", "", "explore"))
	original := m.content.activeDelegations["child"]
	originalCard := original.dd
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: output.DelegationOccurrence{CallID: "original-call", BatchID: "old-batch", AgentID: "child"}, Status: "completed"}))
	if originalCard.status != "complete" {
		t.Fatalf("test setup original card status = %q, want complete", originalCard.status)
	}

	m.applyEvent(output.NewToolCallStartedEvent(1, "follow_up", "fresh-call", map[string]any{"agent_id": "child", "message": "fresh work"}))
	fresh := m.content.segments[len(m.content.segments)-1].delegData
	if fresh == nil || fresh == originalCard {
		t.Fatalf("fresh follow-up card = %#v, original=%p", fresh, originalCard)
	}
	m.applyEvent(output.NewToolCallFinishedEventWithAdmission(1, "follow_up", "fresh-call", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "accepted", AgentID: "child", BatchID: "fresh-batch", Group: "fresh-group"}))
	m.applyEvent(output.NewDelegationStartedEvent(output.DelegationOccurrence{CallID: "fresh-call", BatchID: "fresh-batch", AgentID: "child"}, "fresh work", "", "explore"))

	if fresh.agentID != "child" || fresh.parentCallID != "fresh-call" || fresh.status != "active" || fresh.acceptedGroup() != "fresh-group" {
		t.Fatalf("fresh follow-up binding = %#v", fresh)
	}
	if findDelegationSegment(m.content.segments, originalCard) < 0 || originalCard.status != "complete" {
		t.Fatalf("completed original card changed: %#v", originalCard)
	}
	if loc := m.content.activeDelegations["child"]; loc.dd != fresh || loc.seg != findDelegationSegment(m.content.segments, fresh) {
		t.Fatalf("active locator = %#v, want fresh card at %d", loc, findDelegationSegment(m.content.segments, fresh))
	}
	if len(m.content.openDelegations) != 0 {
		t.Fatalf("fresh follow-up remains open: %#v", m.content.openDelegations)
	}
	roster := m.roster.entries["child"]
	if roster == nil || roster.status != rosterRunning || roster.group != "fresh-group" {
		t.Fatalf("fresh follow-up roster = %#v", roster)
	}
	if out := sidebarText(&m.roster, 1); !strings.Contains(out, "fresh-group") || strings.Contains(out, "old-group") || strings.Count(out, "child") != 1 {
		t.Fatalf("sidebar should show only the fresh run:\n%s", out)
	}
}

// TestRejectedFrameMemberRerendersCachedFrame checks that rejecting the
// provisional card of a frame whose settled card is segment-cached re-renders
// the kept card alone. The outer string cache is not reset before the warm render.
func TestRejectedFrameMemberRerendersCachedFrame(t *testing.T) {
	useTrueColor(t)
	freezeDelegationClock(t)
	build := func(b *contentBuffer) *delegationDisplayState {
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "settled-call", subAgentArgs("")))
		settled := lastDelegationCard(b)
		occ := callOcc("settled-call", "child-1")
		b.AppendEvent(output.NewDelegationStartedEvent(occ, "settled work", "", "explore"))
		b.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{DelegationOccurrence: occ, Status: "completed"}))
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "provisional-call", subAgentArgs("")))
		return settled
	}
	warm := groupTestBuffer()
	settled := build(warm)
	quietFrame(warm)
	if len(warm.segments) != 1 || warm.segments[0].kind != segmentDelegationGroup || warm.segments[0].cachedRender == "" {
		t.Fatalf("setup: frame not cached: segments=%v", segmentKinds(warm.segments))
	}
	rejectRegroupCard(warm, "provisional-call")
	got := warm.String(prefixGenWidth)
	if len(warm.segments) != 1 || warm.segments[0].kind != segmentDelegation || warm.segments[0].delegData != settled {
		t.Fatalf("segments after rejection = %v, want only the settled card", segmentKinds(warm.segments))
	}

	cold := groupTestBuffer()
	build(cold)
	rejectRegroupCard(cold, "provisional-call")
	if want := cold.String(prefixGenWidth); got != want {
		t.Fatalf("rejected render differs from cold render:\nwarm:\n%s\ncold:\n%s", stripANSI(got), stripANSI(want))
	}
}

// TestRejectedMiddleFrameMemberKeepsNeighbourOrder checks that rejecting the
// middle card of an unnamed frame leaves the other two as one frame in order.
func TestRejectedMiddleFrameMemberKeepsNeighbourOrder(t *testing.T) {
	b := groupTestBuffer()
	var cards []*delegationDisplayState
	for _, id := range []string{"a", "b", "c"} {
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", id, subAgentArgs("")))
		cards = append(cards, lastDelegationCard(b))
	}
	rejectRegroupCard(b, "b")
	if len(b.segments) != 1 || b.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("segments = %v, want one unnamed frame", segmentKinds(b.segments))
	}
	entries := b.segments[0].delegGroupData.entries
	if len(entries) != 2 || entries[0] != cards[0] || entries[1] != cards[2] {
		t.Fatalf("entries after rejecting middle = %v, want a then c", entries)
	}
	if findDelegationSegment(b.segments, cards[1]) >= 0 {
		t.Fatal("rejected middle card is still rendered")
	}
	assertLocatorsResolve(t, b)
}

func countDelegationCards(segments []contentSegment) int {
	count := 0
	for _, segment := range segments {
		switch segment.kind {
		case segmentDelegation:
			count++
		case segmentDelegationGroup:
			if segment.delegGroupData != nil {
				count += len(segment.delegGroupData.entries)
			}
		}
	}
	return count
}

func newIdentityTestModel() *Model {
	return &Model{content: contentBuffer{}, roster: subAgentRoster{entries: map[string]*rosterEntry{}}, styles: testStyles("#5599ff")}
}

func TestDelegationRejectionIdentityRemapsPendingSurvivors(t *testing.T) {
	rejected := &delegationDisplayState{parentCallID: "rejected", status: "active"}
	survivor := &delegationDisplayState{parentCallID: "survivor", status: "active"}
	tool := &toolCallSegment{callID: "tool", active: true}
	advisor := &delegationDisplayState{isAdvisor: true}
	b := &contentBuffer{
		segments: []contentSegment{
			{kind: segmentDelegation, delegData: rejected},
			{kind: segmentDelegation, delegData: survivor},
			{kind: segmentToolCall, toolData: tool},
			{kind: segmentDelegation, delegData: advisor},
		},
		collapseState:           map[int]bool{0: true, 1: false, 2: true, 3: false},
		activeDelegations:       map[string]delegationLocator{"rejected": {seg: 0, dd: rejected}, "survivor": {seg: 1, dd: survivor}},
		openDelegations:         map[string]delegationLocator{"rejected": {seg: 0, dd: rejected}, "survivor": {seg: 1, dd: survivor}},
		delegations:             map[occurrenceKey]delegationLocator{{BatchID: "b", CallID: "survivor"}: {seg: 1, dd: survivor}},
		pendingDelegationStarts: []delegationLocator{{seg: 0, dd: rejected}, {seg: 1, dd: survivor}},
		queuedDelegations:       map[string]delegationLocator{"rejected": {seg: 0, dd: rejected}, "survivor": {seg: 1, dd: survivor}},
		activeToolCalls:         map[string]toolCallLocator{"tool": {seg: 2, td: tool}},
		activeAdvisorSegment:    4,
	}

	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "rejected", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	assertSurvivorLocator := func(name string, loc delegationLocator) {
		t.Helper()
		if loc.dd != survivor || loc.seg != 0 {
			t.Errorf("%s survivor locator = %#v, want pointer %p at shifted index 0", name, loc, survivor)
		}
	}
	if loc, ok := b.activeDelegations["rejected"]; ok {
		t.Errorf("rejected active locator remains: %#v", loc)
	}
	assertSurvivorLocator("active", b.activeDelegations["survivor"])
	if _, ok := b.openDelegations["rejected"]; ok || len(b.openDelegations) != 1 {
		t.Errorf("open parents = %#v, want only survivor", b.openDelegations)
	}
	assertSurvivorLocator("open", b.openDelegations["survivor"])
	assertSurvivorLocator("occurrence", b.delegations[occurrenceKey{BatchID: "b", CallID: "survivor"}])
	if len(b.pendingDelegationStarts) != 1 || b.pendingDelegationStarts[0].dd != survivor || b.pendingDelegationStarts[0].seg != 0 {
		t.Errorf("pending start survivors = %#v", b.pendingDelegationStarts)
	}
	if loc, ok := b.queuedDelegations["rejected"]; ok {
		t.Errorf("rejected queued locator remains: %#v", loc)
	}
	if loc, ok := b.queuedDelegations["survivor"]; !ok {
		t.Error("survivor queued locator missing")
	} else {
		assertSurvivorLocator("queued", loc)
	}
	if loc, ok := b.activeToolCalls["tool"]; !ok || loc.td != tool || loc.seg != 1 {
		t.Errorf("tool locator = %#v, want pointer %p at shifted index 1", loc, tool)
	}
	if b.activeAdvisorSegment != 3 {
		t.Errorf("advisor index = %d, want shifted one-based index 3", b.activeAdvisorSegment)
	}
	if len(b.collapseState) != 3 || b.collapseState[0] || !b.collapseState[1] || b.collapseState[2] {
		t.Errorf("collapse state = %#v, want survivor/tool/advisor entries", b.collapseState)
	}
}
