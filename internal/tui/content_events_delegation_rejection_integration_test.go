package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestRejectedProvisionalBetweenAcceptedGroupSiblingsLeavesOneFrame(t *testing.T) {
	first := &delegationDisplayState{parentCallID: "first", groupAccepted: true, batchID: "batch", group: "g"}
	provisional := &delegationDisplayState{parentCallID: "rejected"}
	last := &delegationDisplayState{parentCallID: "last", groupAccepted: true, batchID: "batch", group: "g"}
	b := &contentBuffer{segments: []contentSegment{
		{kind: segmentDelegation, delegData: first},
		{kind: segmentDelegation, delegData: provisional},
		{kind: segmentDelegation, delegData: last},
	}}
	b.regroupAcceptedDelegations()
	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "rejected", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))
	if len(b.segments) != 1 || b.segments[0].kind != segmentDelegationGroup || len(b.segments[0].delegGroupData.entries) != 2 || b.segments[0].delegGroupData.entries[0] != first || b.segments[0].delegGroupData.entries[1] != last {
		t.Fatalf("accepted frame after rejection = %#v", b.segments)
	}
}

func TestRejectedCurrentCallRewriteRemapsPointersAndKeepsFollowUpChild(t *testing.T) {
	accepted := &delegationDisplayState{agentID: "child", parentCallID: "original", groupAccepted: true, batchID: "batch", group: "shared", status: "active"}
	rejected := &delegationDisplayState{parentCallID: "current", status: "active"}
	other := &delegationDisplayState{parentCallID: "other", groupAccepted: true, batchID: "batch", group: "shared"}
	tool := &toolCallSegment{callID: "tool", active: true}
	advisor := &delegationDisplayState{isAdvisor: true}
	b := &contentBuffer{
		segments: []contentSegment{
			{kind: segmentDelegation, delegData: accepted},
			{kind: segmentToolCall, toolData: tool},
			{kind: segmentDelegation, delegData: rejected},
			{kind: segmentDelegation, delegData: other},
			{kind: segmentDelegation, delegData: advisor},
		},
		collapseState:           map[int]bool{0: false, 1: true, 2: false, 3: true, 4: false},
		activeDelegations:       map[string]delegationLocator{"child": {seg: 0, dd: accepted}, "removed": {seg: 2, dd: rejected}, "other": {seg: 3, dd: other}},
		pendingDelegateParents:  []delegationLocator{{seg: 2, dd: rejected}, {seg: 3, dd: other}},
		pendingDelegationStarts: []delegationLocator{{seg: 2, dd: rejected}, {seg: 3, dd: other}},
		queuedDelegations:       map[string]delegationLocator{"current": {seg: 2, dd: rejected}, "other": {seg: 3, dd: other}},
		activeToolCalls:         map[string]toolCallLocator{"tool": {seg: 1, td: tool}},
		activeAdvisorSegment:    5,
		stringCacheWidth:        80, stringCacheRendered: "stale",
		prefixCacheSet: true, prefixCacheRendered: "stale", segmentHeights: []int{1, 2, 3, 4, 5},
	}

	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "current", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	if _, ok := b.activeDelegations["removed"]; ok || len(b.pendingDelegateParents) != 1 || len(b.pendingDelegationStarts) != 1 {
		t.Fatalf("removed current-call locators remain: active=%#v pending=%#v starts=%#v", b.activeDelegations, b.pendingDelegateParents, b.pendingDelegationStarts)
	}
	if _, ok := b.queuedDelegations["current"]; ok || b.queuedDelegations["other"].dd != other || b.queuedDelegations["other"].seg != 0 {
		t.Fatalf("queued locators = %#v", b.queuedDelegations)
	}
	if b.activeDelegations["child"].dd != accepted || b.activeDelegations["child"].seg != 0 || b.activeDelegations["other"].seg != 0 {
		t.Fatalf("active locators = %#v", b.activeDelegations)
	}
	if b.activeToolCalls["tool"].td != tool || b.activeToolCalls["tool"].seg != 1 || b.activeAdvisorSegment != 3 {
		t.Fatalf("tool/advisor locators = %#v advisor=%d", b.activeToolCalls, b.activeAdvisorSegment)
	}
	if b.structureGen != 1 || b.segmentHeights != nil || b.stringCacheWidth != 0 || b.stringCacheRendered != "" || b.prefixCacheSet || b.prefixCacheRendered != "" {
		t.Fatalf("rewrite caches/generation not invalidated: structure=%d heights=%v string=%d/%q prefix=%v/%q", b.structureGen, b.segmentHeights, b.stringCacheWidth, b.stringCacheRendered, b.prefixCacheSet, b.prefixCacheRendered)
	}
	if len(b.segments) != 3 || b.segments[0].kind != segmentDelegationGroup || b.segments[0].delegGroupData.entries[0] != accepted || b.segments[0].delegGroupData.entries[1] != other || b.segments[1].toolData != tool || b.segments[2].delegData != advisor {
		t.Fatalf("rewritten layout = %#v", b.segments)
	}
	if len(b.collapseState) != 3 || b.collapseState[0] || !b.collapseState[1] || b.collapseState[2] {
		t.Fatalf("collapse state = %#v", b.collapseState)
	}

	b.handleFollowUpToolCallStarted(output.ToolCallStartedEvent{CallID: "follow-up", Arguments: map[string]any{"agent_id": "child", "message": "continue"}})
	var followUp *delegationDisplayState
	b.forEachDelegationReverse(func(loc delegationLocator) bool {
		if loc.dd != accepted {
			followUp = loc.dd
			return true
		}
		return false
	})
	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "follow_up", "follow-up", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))
	if findDelegationSegment(b.segments, accepted) < 0 || followUp == nil || findDelegationSegment(b.segments, followUp) >= 0 {
		t.Fatal("follow-up rejection removed original child or retained rejected pending card")
	}
	if accepted.agentID != "child" || accepted.parentCallID != "original" || accepted.status != "active" {
		t.Fatalf("original running child changed: %#v", accepted)
	}
	b.appendDelegationEvent(output.NewDelegationStartedEvent("child", "original task", "original"))
	if accepted.parentCallID != "original" || accepted.agentID != "child" {
		t.Fatalf("late original-child event rebound: %#v", accepted)
	}
}
