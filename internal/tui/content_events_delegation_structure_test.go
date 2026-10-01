package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestDelegationStructureRewriteRemapsLocatorsAndInvalidatesCaches(t *testing.T) {
	first := &delegationDisplayState{agentID: "first", parentCallID: "call-first", groupAccepted: true, batchID: "batch", group: "shared"}
	second := &delegationDisplayState{agentID: "second", parentCallID: "call-second", groupAccepted: true, batchID: "batch", group: "shared"}
	removed := &delegationDisplayState{agentID: "removed"}
	regular := &toolCallSegment{callID: "regular", active: true}
	lostTool := &toolCallSegment{callID: "lost"}
	advisor := &delegationDisplayState{isAdvisor: true}
	b := &contentBuffer{
		segments: []contentSegment{
			{kind: segmentPlain, text: "prefix", cachedRender: "settled", renderDirty: false},
			{kind: segmentDelegation, delegData: first, renderDirty: true},
			{kind: segmentToolCall, toolData: regular},
			{kind: segmentStatus, text: "unknown"},
			{kind: segmentDelegation, delegData: second, renderDirty: true},
			{kind: segmentDelegation, delegData: advisor},
		},
		collapseState: map[int]bool{0: true, 1: false, 2: true, 3: false, 4: true, 5: false},
		activeDelegations: map[string]delegationLocator{
			"first": {seg: 1, dd: first}, "second": {seg: 4, dd: second}, "removed": {seg: 1, dd: removed},
		},
		pendingDelegateParents:  []delegationLocator{{seg: 1, dd: first}, {seg: 4, dd: second}, {seg: 1, dd: removed}},
		pendingDelegationStarts: []delegationLocator{{seg: 4, dd: second}, {seg: 1, dd: first}, {seg: 1, dd: removed}},
		queuedDelegations: map[string]delegationLocator{
			"call-first": {seg: 1, dd: first}, "call-second": {seg: 4, dd: second}, "removed": {seg: 1, dd: removed},
		},
		activeToolCalls: map[string]toolCallLocator{
			"regular": {seg: 2, td: regular}, "lost": {seg: 2, td: lostTool},
		},
		activeAdvisorSegment: 6,
		stringCacheWidth:     80, stringCacheRendered: "stale string",
		prefixCacheSet: true, prefixCacheRendered: "stale prefix", prefixCacheLen: 5,
		segmentHeights: []int{1, 2, 3, 4, 5, 6}, gen: 7,
	}

	b.regroupAcceptedDelegations()

	if len(b.segments) != 5 || b.segments[0].kind != segmentPlain || b.segments[1].kind != segmentDelegationGroup || b.segments[2].kind != segmentToolCall || b.segments[3].kind != segmentStatus || b.segments[4].delegData != advisor {
		t.Fatalf("rewritten segment order = %#v", b.segments)
	}
	entries := b.segments[1].delegGroupData.entries
	if len(entries) != 2 || entries[0] != first || entries[1] != second {
		t.Fatalf("group entries = %#v, want same state pointers in original order", entries)
	}
	if got := b.activeDelegations["second"]; got.dd != second || got.seg != 1 {
		t.Fatalf("active delegation locator = %#v, want second at 1", got)
	}
	if _, ok := b.activeDelegations["removed"]; ok || len(b.pendingDelegateParents) != 2 || len(b.pendingDelegationStarts) != 2 {
		t.Fatalf("removed delegation locator retained: active=%#v pending=%#v starts=%#v", b.activeDelegations, b.pendingDelegateParents, b.pendingDelegationStarts)
	}
	if b.pendingDelegateParents[0].dd != first || b.pendingDelegateParents[0].seg != 1 || b.pendingDelegateParents[1].dd != second || b.pendingDelegateParents[1].seg != 1 {
		t.Fatalf("pending parent locators = %#v", b.pendingDelegateParents)
	}
	if b.pendingDelegationStarts[0].dd != second || b.pendingDelegationStarts[1].dd != first || b.pendingDelegationStarts[0].seg != 1 || b.pendingDelegationStarts[1].seg != 1 {
		t.Fatalf("pending start locators = %#v", b.pendingDelegationStarts)
	}
	if b.queuedDelegations["call-first"].dd != first || b.queuedDelegations["call-first"].seg != 1 || b.queuedDelegations["call-second"].dd != second || b.queuedDelegations["call-second"].seg != 1 {
		t.Fatalf("queued delegation locators = %#v", b.queuedDelegations)
	}
	if _, ok := b.queuedDelegations["removed"]; ok {
		t.Fatal("removed queued delegation locator retained")
	}
	if b.activeToolCalls["regular"].td != regular || b.activeToolCalls["regular"].seg != 2 {
		t.Fatalf("active tool locator = %#v, want same call pointer at 2", b.activeToolCalls["regular"])
	}
	if _, ok := b.activeToolCalls["lost"]; ok {
		t.Fatal("active tool locator without retained segment was not removed")
	}
	if b.activeAdvisorSegment != 5 {
		t.Fatalf("activeAdvisorSegment = %d, want 5", b.activeAdvisorSegment)
	}
	if b.collapseState[0] != true || b.collapseState[1] != false || b.collapseState[2] != true || b.collapseState[3] != false || b.collapseState[4] != false {
		t.Fatalf("collapse state remap = %#v", b.collapseState)
	}
	if b.gen != 8 || b.structureGen != 1 || b.stringCacheWidth != 0 || b.stringCacheRendered != "" || b.prefixCacheSet || b.prefixCacheRendered != "" || b.segmentHeights != nil {
		t.Fatalf("rewrite did not invalidate caches/generation: gen=%d structure=%d string=%d/%q prefix=%v/%q heights=%v", b.gen, b.structureGen, b.stringCacheWidth, b.stringCacheRendered, b.prefixCacheSet, b.prefixCacheRendered, b.segmentHeights)
	}
}

func TestDelegationAcceptanceIsIdempotentAndRejectsIneligibleMembership(t *testing.T) {
	tests := []struct {
		name string
		dd   *delegationDisplayState
	}{
		{"advisor", &delegationDisplayState{parentCallID: "call", isAdvisor: true}},
		{"unaccepted", &delegationDisplayState{parentCallID: "call", group: "g", batchID: "b"}},
		{"empty batch", &delegationDisplayState{parentCallID: "call", group: "g", groupAccepted: true}},
		{"empty group", &delegationDisplayState{parentCallID: "call", batchID: "b", groupAccepted: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: tc.dd}}}
			b.regroupAcceptedDelegations()
			if b.segments[0].kind != segmentDelegation || b.structureGen != 0 {
				t.Fatalf("ineligible membership regrouped: segment kind=%v structureGen=%d", b.segments[0].kind, b.structureGen)
			}
		})
	}

	first := &delegationDisplayState{parentCallID: "call-first", group: "group", groupAccepted: true, batchID: "batch"}
	second := &delegationDisplayState{parentCallID: "call-second"}
	b := &contentBuffer{segments: []contentSegment{
		{kind: segmentDelegation, delegData: first},
		{kind: segmentDelegation, delegData: second},
	}}
	b.appendDelegationAcceptedEvent(output.NewDelegationAcceptedEvent("call-second", "", "batch", "group"))
	if b.structureGen != 1 || len(b.segments) != 1 || b.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("second acceptance did not join existing group: structure=%d segments=%v", b.structureGen, segmentKinds(b.segments))
	}
	b.appendDelegationAcceptedEvent(output.NewDelegationAcceptedEvent("missing-call", "", "batch", "group"))
	if b.structureGen != 1 {
		t.Fatalf("acceptance with unknown call changed structure generation: %d", b.structureGen)
	}
	if len(b.segments[0].delegGroupData.entries) != 2 || b.segments[0].delegGroupData.entries[1] != second {
		t.Fatal("duplicate acceptance changed group member pointer/order")
	}
}

func TestDelegationRegroupSeparatesAcceptedAndUnknownSameLabelCards(t *testing.T) {
	accepted := &delegationDisplayState{parentCallID: "accepted", group: "same", groupAccepted: true, batchID: "batch"}
	unknown := &delegationDisplayState{parentCallID: "unknown", group: "same", groupAccepted: true}
	b := &contentBuffer{segments: []contentSegment{
		{kind: segmentDelegation, delegData: accepted},
		{kind: segmentDelegation, delegData: unknown},
	}}
	b.regroupAcceptedDelegations()
	if len(b.segments) != 2 || b.segments[0].kind != segmentDelegationGroup || b.segments[1].kind != segmentDelegation {
		t.Fatalf("accepted and unknown cards grouped together: kinds=%v", segmentKinds(b.segments))
	}
	if b.segments[0].delegGroupData.entries[0] != accepted || b.segments[1].delegData != unknown {
		t.Fatal("regroup replaced state pointers or moved unknown card")
	}
}

func TestDelegationAcceptanceOrderKeepsFirstMemberPosition(t *testing.T) {
	for _, order := range [][]string{{"first", "second"}, {"second", "first"}} {
		t.Run(order[0]+" then "+order[1], func(t *testing.T) {
			first := &delegationDisplayState{parentCallID: "first"}
			second := &delegationDisplayState{parentCallID: "second"}
			b := &contentBuffer{segments: []contentSegment{
				{kind: segmentDelegation, delegData: first},
				{kind: segmentPlain, text: "middle"},
				{kind: segmentDelegation, delegData: second},
				{kind: segmentPlain, text: "tail"},
			}}
			for _, id := range order {
				batchID := "batch"
				b.appendDelegationAcceptedEvent(output.NewDelegationAcceptedEvent(id, "", batchID, "g"))
			}
			if len(b.segments) != 3 || b.segments[0].kind != segmentDelegationGroup || b.segments[1].text != "middle" || b.segments[2].text != "tail" {
				t.Fatalf("acceptance order changed first-member position/order: %#v", b.segments)
			}
			if b.segments[0].delegGroupData.entries[0] != first || b.segments[0].delegGroupData.entries[1] != second {
				t.Fatalf("group member order = %#v", b.segments[0].delegGroupData.entries)
			}
		})
	}
}

func TestDelegationRegroupPreservesInterleavedStreamAndSegmentOrder(t *testing.T) {
	first := &delegationDisplayState{parentCallID: "first", group: "g", groupAccepted: true, batchID: "batch"}
	second := &delegationDisplayState{parentCallID: "second", group: "g", groupAccepted: true, batchID: "batch"}
	tool := &toolCallSegment{callID: "tool"}
	unknown := contentSegment{kind: segmentStatus, text: "unknown"}
	b := &contentBuffer{
		segments: []contentSegment{
			{kind: segmentDelegation, delegData: first},
			{kind: segmentToolCall, toolData: tool},
			unknown,
			{kind: segmentDelegation, delegData: second},
		},
		streamBuffer: "live answer", streaming: true, streamingPhase: "answer", streamingSource: output.ChunkSourceAssistant,
	}
	b.regroupAcceptedDelegations()
	if len(b.segments) != 3 || b.segments[0].kind != segmentDelegationGroup || len(b.segments[0].delegGroupData.entries) != 2 || b.segments[0].delegGroupData.entries[0] != first || b.segments[0].delegGroupData.entries[1] != second || b.segments[1].toolData != tool || b.segments[2].text != "unknown" {
		t.Fatalf("intervening segment order changed: %#v", b.segments)
	}
	if !b.streaming || b.streamingPhase != "answer" || b.streamingSource != output.ChunkSourceAssistant || b.streamBuffer != "live answer" {
		t.Fatalf("live stream changed during structure rewrite: streaming=%v phase=%q source=%q buffer=%q", b.streaming, b.streamingPhase, b.streamingSource, b.streamBuffer)
	}
}
