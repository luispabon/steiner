package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestDelegationStructureRewriteRemapsLocatorsAndInvalidatesCaches(t *testing.T) {
	first := &delegationDisplayState{agentID: "first", parentCallID: "call-first", collapsed: false}
	second := &delegationDisplayState{agentID: "second", parentCallID: "call-second", collapsed: true}
	removed := &delegationDisplayState{agentID: "removed"}
	regular := &toolCallSegment{callID: "regular", active: true}
	lostTool := &toolCallSegment{callID: "lost"}
	groupedTool := &toolCallSegment{callID: "grouped"}
	advisor := &delegationDisplayState{isAdvisor: true}
	b := &contentBuffer{
		segments: []contentSegment{
			{kind: segmentPlain, text: "prefix", cachedRender: "settled", renderDirty: false},
			{kind: segmentDelegation, delegData: first, renderDirty: true},
			{kind: segmentToolCall, toolData: regular},
			{kind: segmentStatus, text: "unknown"},
			{kind: segmentDelegation, delegData: second, renderDirty: true},
			{kind: segmentToolCallGroup, toolGroupData: &toolCallGroupSegment{entries: []*toolCallSegment{groupedTool}}},
			{kind: segmentDelegation, delegData: advisor},
		},
		collapseState: map[int]bool{0: true, 1: false, 2: true, 3: false, 4: true, 5: false, 6: true},
		activeDelegations: map[string]delegationLocator{
			"first": {seg: 1, dd: first}, "second": {seg: 4, dd: second}, "removed": {seg: 1, dd: removed},
		},
		openDelegations: map[string]delegationLocator{
			"call-first": {seg: 1, dd: first}, "call-second": {seg: 4, dd: second}, "removed": {seg: 1, dd: removed},
		},
		pendingDelegationStarts: []delegationLocator{{seg: 4, dd: second}, {seg: 1, dd: first}, {seg: 1, dd: removed}},
		queuedDelegations: map[string]delegationLocator{
			"call-first": {seg: 1, dd: first}, "call-second": {seg: 4, dd: second}, "removed": {seg: 1, dd: removed},
		},
		activeToolCalls: map[string]toolCallLocator{
			"regular": {seg: 2, td: regular}, "lost": {seg: 2, td: lostTool}, "grouped": {seg: 5, td: groupedTool},
		},
		activeAdvisorSegment: 7,
		stringCacheWidth:     80, stringCacheRendered: "stale string",
		prefixCacheSet: true, prefixCacheRendered: "stale prefix", prefixCacheLen: 5,
		segmentHeights: []int{1, 2, 3, 4, 5, 6}, gen: 7,
	}

	acceptDelegationCard(b, "call-first", "batch", "shared")
	gen, structureGen := b.gen, b.structureGen
	b.stringCacheWidth, b.stringCacheRendered = 80, "stale string"
	b.prefixCacheSet, b.prefixCacheRendered, b.prefixCacheLen = true, "stale prefix", 5
	b.segmentHeights = []int{1, 2, 3, 4, 5, 6, 7}
	acceptDelegationCard(b, "call-second", "batch", "shared")

	if len(b.segments) != 6 || b.segments[0].kind != segmentPlain || b.segments[1].kind != segmentDelegationGroup || b.segments[2].kind != segmentToolCall || b.segments[3].kind != segmentStatus || b.segments[4].kind != segmentToolCallGroup || b.segments[5].delegData != advisor {
		t.Fatalf("rewritten segment order = %#v", b.segments)
	}
	entries := b.segments[1].delegGroupData.entries
	if len(entries) != 2 || entries[0] != first || entries[1] != second {
		t.Fatalf("group entries = %#v, want same state pointers in original order", entries)
	}
	if got := b.activeDelegations["second"]; got.dd != second || got.seg != 1 {
		t.Fatalf("active delegation locator = %#v, want second at 1", got)
	}
	if _, ok := b.activeDelegations["removed"]; ok || len(b.openDelegations) != 0 || len(b.pendingDelegationStarts) != 2 {
		t.Fatalf("stale delegation locator retained: active=%#v open=%#v starts=%#v", b.activeDelegations, b.openDelegations, b.pendingDelegationStarts)
	}
	for callID, want := range map[string]*delegationDisplayState{"call-first": first, "call-second": second} {
		if got := b.delegations[occurrenceKey{BatchID: "batch", CallID: callID}]; got.dd != want || got.seg != 1 {
			t.Fatalf("occurrence locator %s = %#v, want card at 1", callID, got)
		}
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
	if b.activeToolCalls["grouped"].td != groupedTool || b.activeToolCalls["grouped"].seg != 4 {
		t.Fatalf("grouped tool locator = %#v, want original call pointer at 4", b.activeToolCalls["grouped"])
	}
	if _, ok := b.activeToolCalls["lost"]; ok {
		t.Fatal("active tool locator without retained segment was not removed")
	}
	if b.activeAdvisorSegment != 6 {
		t.Fatalf("activeAdvisorSegment = %d, want 6", b.activeAdvisorSegment)
	}
	if first.collapsed || !second.collapsed {
		t.Fatalf("child collapsed state changed: first=%v second=%v", first.collapsed, second.collapsed)
	}
	wantCollapse := map[int]bool{0: true, 1: false, 2: true, 3: false, 4: false, 5: true}
	if len(b.collapseState) != len(wantCollapse) {
		t.Fatalf("collapse state keys = %#v, want exactly %#v", b.collapseState, wantCollapse)
	}
	for index, want := range wantCollapse {
		if got, ok := b.collapseState[index]; !ok || got != want {
			t.Fatalf("collapse state[%d] = %v (present=%v), want %v", index, got, ok, want)
		}
	}
	if b.gen != gen+1 || b.structureGen != structureGen+1 || b.stringCacheWidth != 0 || b.stringCacheRendered != "" || b.prefixCacheSet || b.prefixCacheRendered != "" || b.segmentHeights != nil {
		t.Fatalf("rewrite did not invalidate caches/generation: gen=%d structure=%d string=%d/%q prefix=%v/%q heights=%v", b.gen, b.structureGen, b.stringCacheWidth, b.stringCacheRendered, b.prefixCacheSet, b.prefixCacheRendered, b.segmentHeights)
	}
}

func TestDelegationAcceptanceIsIdempotentAndRejectsIneligibleMembership(t *testing.T) {
	tests := []struct {
		name string
		dd   *delegationDisplayState
	}{
		{"advisor", &delegationDisplayState{parentCallID: "call", isAdvisor: true}},
		{"empty batch", &delegationDisplayState{parentCallID: "call"}},
		{"empty group", &delegationDisplayState{parentCallID: "call"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: tc.dd}}}
			switch tc.name {
			case "empty batch":
				acceptDelegationCard(b, "call", "", "g")
			case "empty group":
				acceptDelegationCard(b, "call", "b", "")
			default:
				acceptDelegationCard(b, "call", "b", "g")
			}
			if b.segments[0].kind != segmentDelegation || b.structureGen != 0 {
				t.Fatalf("ineligible membership regrouped: segment kind=%v structureGen=%d", b.segments[0].kind, b.structureGen)
			}
		})
	}

	first := &delegationDisplayState{parentCallID: "call-first"}
	second := &delegationDisplayState{parentCallID: "call-second"}
	b := &contentBuffer{segments: []contentSegment{
		{kind: segmentDelegation, delegData: first},
		{kind: segmentDelegation, delegData: second},
	}}
	acceptDelegationCard(b, "call-first", "batch", "group")
	b.structureGen = 0
	acceptDelegationCard(b, "call-second", "batch", "group")
	if b.structureGen != 1 || len(b.segments) != 1 || b.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("second acceptance did not join existing group: structure=%d segments=%v", b.structureGen, segmentKinds(b.segments))
	}
	acceptDelegationCard(b, "call-second", "batch", "group")
	if b.structureGen != 1 {
		t.Fatalf("duplicate acceptance changed structure generation: %d", b.structureGen)
	}
	acceptDelegationCard(b, "call-first", "batch", "group")
	if b.structureGen != 1 {
		t.Fatalf("acceptance for already-grouped member changed structure generation: %d", b.structureGen)
	}
	if len(b.segments[0].delegGroupData.entries) != 2 || b.segments[0].delegGroupData.entries[1] != second {
		t.Fatal("duplicate acceptance changed group member pointer/order")
	}
}

func TestDelegationRegroupSeparatesAcceptedAndUnknownSameLabelCards(t *testing.T) {
	accepted := &delegationDisplayState{parentCallID: "accepted"}
	unknown := &delegationDisplayState{parentCallID: "unknown"}
	b := &contentBuffer{segments: []contentSegment{
		{kind: segmentDelegation, delegData: accepted},
		{kind: segmentDelegation, delegData: unknown},
	}}
	acceptDelegationCard(b, "accepted", "batch", "same")
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
				acceptDelegationCard(b, id, batchID, "g")
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
	first := &delegationDisplayState{parentCallID: "first"}
	second := &delegationDisplayState{parentCallID: "second"}
	tool := &toolCallSegment{callID: "tool"}
	unknown := contentSegment{kind: segmentStatus, text: "unknown"}
	thinking := contentSegment{kind: segmentThinkingBlock, thinkData: &thinkingBlockData{body: "thinking text", source: output.ChunkSourceAssistant}}
	b := &contentBuffer{
		segments: []contentSegment{
			{kind: segmentDelegation, delegData: first},
			{kind: segmentToolCall, toolData: tool},
			unknown,
			thinking,
			{kind: segmentDelegation, delegData: second},
		},
		streamBuffer: "live answer", streaming: true, streamingPhase: "answer", streamingSource: output.ChunkSourceAssistant,
	}
	acceptDelegationCard(b, "first", "batch", "g")
	acceptDelegationCard(b, "second", "batch", "g")
	if len(b.segments) != 4 || b.segments[0].kind != segmentDelegationGroup || len(b.segments[0].delegGroupData.entries) != 2 || b.segments[0].delegGroupData.entries[0] != first || b.segments[0].delegGroupData.entries[1] != second || b.segments[1].toolData != tool || b.segments[2].text != "unknown" || b.segments[3].thinkData != thinking.thinkData || b.segments[3].thinkData.body != "thinking text" || b.segments[3].thinkData.source != output.ChunkSourceAssistant {
		t.Fatalf("intervening segment order changed: %#v", b.segments)
	}
	if !b.streaming || b.streamingPhase != "answer" || b.streamingSource != output.ChunkSourceAssistant || b.streamBuffer != "live answer" {
		t.Fatalf("live stream changed during structure rewrite: streaming=%v phase=%q source=%q buffer=%q", b.streaming, b.streamingPhase, b.streamingSource, b.streamBuffer)
	}
}

func acceptDelegationCard(b *contentBuffer, callID, batchID, group string) {
	openParentCard(b, callID)
	b.appendDelegationAcceptedEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: callID, BatchID: batchID}, group))
}

// openParentCard indexes the unadmitted card for callID as an open parent, as
// its ToolCallStarted would have.
func openParentCard(b *contentBuffer, callID string) {
	for _, tok := range flattenDelegationSegments(b.segments) {
		if tok.dd != nil && tok.dd.parentCallID == callID && tok.dd.admission == nil {
			b.openDelegation(delegationLocator{seg: tok.old, dd: tok.dd})
		}
	}
}
