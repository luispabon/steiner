package tui

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestDelegationRejectionIdentityKeepsAcceptedCardAndError(t *testing.T) {
	accepted := &delegationDisplayState{parentCallID: "accepted-call", groupAccepted: true, batchID: "accepted-batch", group: "accepted-group"}
	b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: accepted}}, activeDelegations: map[string]delegationLocator{"child": {seg: 0, dd: accepted}}}
	if accepted == nil {
		t.Fatal("accepted card missing")
	}
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
	m := &Model{content: contentBuffer{}, roster: subAgentRoster{entries: map[string]*rosterEntry{}}, styles: testStyles("#5599ff")}
	m.content.segments = []contentSegment{{kind: segmentDelegation, delegData: &delegationDisplayState{agentID: "child", parentCallID: "original-call", groupAccepted: true, batchID: "batch", group: "group", status: "active"}}}
	m.content.activeDelegations = map[string]delegationLocator{"child": {seg: 0, dd: m.content.segments[0].delegData}}
	original := m.content.activeDelegations["child"]
	originalCard := original.dd
	m.applyEvent(output.NewToolCallStartedEvent(1, "follow_up", "rejected-call", map[string]any{"agent_id": "child", "message": "rejected follow-up"}))
	m.applyEvent(output.NewToolCallFinishedEventWithAdmission(1, "follow_up", "rejected-call", "", errors.New("not admitted"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	m.applyEvent(output.NewDelegationStartedEventWithType("child", "late rejected task", "rejected-call", "", "explore"))
	got := m.content.activeDelegations["child"]
	if got.dd != originalCard || got.seg != original.seg {
		t.Fatalf("late rejected call stole active locator: got=%#v want=%#v", got, original)
	}
	if findDelegationSegment(m.content.segments, originalCard) < 0 || originalCard.parentCallID != "original-call" {
		t.Fatalf("late rejected event rebound original card: %#v", originalCard)
	}
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
