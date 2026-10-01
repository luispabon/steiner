package tui

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestDelegationRejectionIdentityKeepsAcceptedCardAndError(t *testing.T) {
	accepted := &delegationDisplayState{parentCallID: "accepted-call", groupAccepted: true, batchID: "accepted-batch", group: "accepted-group"}
	b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: accepted}}, activeDelegations: map[string]delegationLocator{"child": {seg: 0, dd: accepted}}}
	const rejection = "provider: current call rejected"
	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "accepted-call", "", errors.New(rejection), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))
	if b.activeDelegations["child"].dd != accepted || findDelegationSegment(b.segments, accepted) < 0 {
		t.Fatal("conflicting rejection removed or replaced accepted card")
	}
	if accepted.group != "accepted-group" || accepted.batchID != "accepted-batch" || !accepted.groupAccepted {
		t.Fatalf("accepted identity changed: %#v", accepted)
	}
	if got := b.segments[len(b.segments)-1]; got.kind != segmentTool || got.text != rejection {
		t.Fatalf("rejection evidence = %#v, want exact error %q", got, rejection)
	}
}

func TestDelegationRejectionIdentityLateRejectedCallDoesNotStealActiveChild(t *testing.T) {
	m := newIdentityTestModel()
	m.applyEvent(output.NewToolCallStartedEvent(1, "sub_agent", "original-call", map[string]any{"type": "explore", "task": "original task"}))
	m.applyEvent(output.NewDelegationAcceptedEvent("original-call", "child", "old-batch", "old-group"))
	m.applyEvent(output.NewDelegationQueuedEvent("child", "original-call", "explore", "original task"))
	m.applyEvent(output.NewDelegationStartedEventWithType("child", "original task", "original-call", "", "explore"))
	original := m.content.activeDelegations["child"]
	originalCard := original.dd
	originalRoster := m.roster.entries["child"]
	if originalCard == nil || originalRoster == nil || originalRoster.status != rosterRunning {
		t.Fatalf("test setup missing active child: locator=%#v roster=%#v", original, originalRoster)
	}
	m.applyEvent(output.NewToolCallStartedEvent(1, "follow_up", "rejected-call", map[string]any{"agent_id": "child", "message": "rejected follow-up"}))
	m.applyEvent(output.NewToolCallFinishedEventWithAdmission(1, "follow_up", "rejected-call", "", errors.New("not admitted"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	m.applyEvent(output.NewDelegationStartedEventWithType("child", "late rejected task", "rejected-call", "", "explore"))
	got := m.content.activeDelegations["child"]
	if got.dd != originalCard || got.seg != original.seg {
		t.Fatalf("late rejected call stole active locator: got=%#v want=%#v", got, original)
	}
	if findDelegationSegment(m.content.segments, originalCard) < 0 || originalCard.parentCallID != "original-call" || originalCard.agentID != "child" || originalCard.group != "old-group" || originalCard.batchID != "old-batch" || originalCard.status != "active" {
		t.Fatalf("late rejected event changed original card identity: %#v", originalCard)
	}
	if countDelegationCards(m.content.segments) != 1 {
		t.Fatalf("late rejected event created another card: %#v", m.content.segments)
	}
	if gotRoster := m.roster.entries["child"]; gotRoster != originalRoster || gotRoster.currentCallID != "original-call" || gotRoster.status != rosterRunning || gotRoster.group != "old-group" || gotRoster.batchID != "old-batch" {
		t.Fatalf("late rejected event changed original roster: %#v", gotRoster)
	}
}

func TestDelegationRejectionIdentityAllowsFreshAcceptedFollowUpAfterCompletion(t *testing.T) {
	m := newIdentityTestModel()
	m.applyEvent(output.NewToolCallStartedEvent(1, "sub_agent", "original-call", map[string]any{"type": "explore", "task": "original task"}))
	m.applyEvent(output.NewDelegationAcceptedEvent("original-call", "child", "old-batch", "old-group"))
	m.applyEvent(output.NewDelegationQueuedEvent("child", "original-call", "explore", "original task"))
	m.applyEvent(output.NewDelegationStartedEventWithType("child", "original task", "original-call", "", "explore"))
	original := m.content.activeDelegations["child"]
	originalCard := original.dd
	m.applyEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "child", Status: "completed"}))
	if originalCard.status != "complete" {
		t.Fatalf("test setup original card status = %q, want complete", originalCard.status)
	}

	m.applyEvent(output.NewToolCallStartedEvent(1, "follow_up", "fresh-call", map[string]any{"agent_id": "child", "message": "fresh work"}))
	fresh := m.content.segments[len(m.content.segments)-1].delegData
	if fresh == nil || fresh == originalCard {
		t.Fatalf("fresh follow-up card = %#v, original=%p", fresh, originalCard)
	}
	m.applyEvent(output.NewToolCallFinishedEventWithAdmission(1, "follow_up", "fresh-call", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "accepted", AgentID: "child", BatchID: "fresh-batch", Group: "fresh-group"}))
	m.applyEvent(output.NewDelegationStartedEventWithType("child", "fresh work", "fresh-call", "", "explore"))

	if fresh.agentID != "child" || fresh.parentCallID != "fresh-call" || fresh.status != "active" || !fresh.groupAccepted || fresh.group != "fresh-group" || fresh.batchID != "fresh-batch" {
		t.Fatalf("fresh follow-up binding = %#v", fresh)
	}
	if findDelegationSegment(m.content.segments, originalCard) < 0 || originalCard.status != "complete" {
		t.Fatalf("completed original card changed: %#v", originalCard)
	}
	if loc := m.content.activeDelegations["child"]; loc.dd != fresh || loc.seg != findDelegationSegment(m.content.segments, fresh) {
		t.Fatalf("active locator = %#v, want fresh card at %d", loc, findDelegationSegment(m.content.segments, fresh))
	}
	if len(m.content.pendingDelegateParents) != 0 {
		t.Fatalf("fresh follow-up remains pending: %#v", m.content.pendingDelegateParents)
	}
	roster := m.roster.entries["child"]
	if roster == nil || roster.currentCallID != "fresh-call" || roster.status != rosterRunning || roster.group != "fresh-group" || roster.batchID != "fresh-batch" {
		t.Fatalf("fresh follow-up roster = %#v", roster)
	}
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
		pendingDelegateParents:  []delegationLocator{{seg: 0, dd: rejected}, {seg: 1, dd: survivor}},
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
	if len(b.pendingDelegateParents) != 1 || b.pendingDelegateParents[0].dd != survivor || b.pendingDelegateParents[0].seg != 0 {
		t.Errorf("pending parent survivors = %#v", b.pendingDelegateParents)
	}
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
