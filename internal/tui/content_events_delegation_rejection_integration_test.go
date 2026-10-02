package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

func TestRejectedToolFinishFlushesLiveAnswerAndThinkingBeforeExactEvidence(t *testing.T) {
	const rejection = "api: provider /status:run failed"
	b := &contentBuffer{collapseState: make(map[int]bool)}
	b.appendToolCallStartedEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call", map[string]any{"type": "explore"}))
	b.AppendEvent(output.NewThinkingChunkEventWithSource(1, "buffered thought", output.ChunkSourceAssistant))
	b.AppendEvent(output.NewAssistantChunkEventWithSource(1, "buffered answer", output.ChunkSourceAssistant))
	if !b.streaming || b.streamBuffer != "buffered answer" || len(b.segments) != 2 || b.segments[1].kind != segmentThinkingBlock || b.segments[1].thinkData == nil || !b.segments[1].thinkData.streaming || b.segments[1].thinkData.body != "buffered thought" {
		t.Fatalf("test setup did not leave live answer/thinking stream: streaming=%v buffer=%q segments=%#v", b.streaming, b.streamBuffer, b.segments)
	}

	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errors.New(rejection), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected", PolicyNotice: true}))

	if b.streaming {
		t.Fatal("tool finish left stream open")
	}
	if len(b.segments) < 3 {
		t.Fatalf("finished segments = %#v", b.segments)
	}
	if b.segments[0].kind != segmentThinkingBlock || b.segments[0].thinkData == nil || b.segments[0].thinkData.body != "buffered thought" {
		t.Fatalf("thinking stream = %#v, want buffered thought before rejection", b.segments[0])
	}
	if b.segments[1].kind != segmentAssistantMarkdown || b.segments[1].text != "buffered answer" {
		t.Fatalf("answer segment = %#v, want buffered answer after thinking and before rejection", b.segments[1])
	}
	if b.segments[2].kind != segmentStatus || b.segments[2].text != "Delegation rejected by policy." {
		t.Fatalf("policy notice = %#v", b.segments[2])
	}
	if len(b.segments) != 4 || b.segments[3].kind != segmentTool || b.segments[3].text != rejection {
		t.Fatalf("rejection evidence = %#v, want exact error after stream and notice", b.segments)
	}
}

func TestRejectedErrorPrefixesRemainExactAndPolicyNoticeTyped(t *testing.T) {
	for _, message := range []string{"api: provider failed", "turn /status:run failed", "status: provider failed"} {
		t.Run(message, func(t *testing.T) {
			b := &contentBuffer{segments: []contentSegment{{kind: segmentDelegation, delegData: &delegationDisplayState{parentCallID: "call"}}}}
			b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errors.New(message), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected", PolicyNotice: true}))
			if len(b.segments) != 2 || b.segments[0].kind != segmentStatus || b.segments[0].text != "Delegation rejected by policy." || b.segments[1].kind != segmentTool || b.segments[1].text != message {
				t.Fatalf("notice/error = %#v, want typed notice and exact %q", b.segments, message)
			}
		})
	}
}

func TestRejectedCurrentCardRemovalClearsSelectionAndDrag(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.applyEvent(output.NewToolCallStartedEvent(1, "sub_agent", "rejected-call", map[string]any{"type": "explore"}))
	m.syncViewport()
	idx := len(m.content.segments) - 1
	anchor := m.content.selectionAnchorForSegmentRow(idx, 0)
	if !anchor.ok {
		t.Fatal("test setup: provisional card has no selection anchor")
	}
	line, ok := m.content.contentLineForSegmentRow(idx, 0)
	if !ok {
		t.Fatal("test setup: provisional card has no content line")
	}
	m.activeRegion = regionViewport
	m.selection = selectionState{
		start:       selectionPoint{line: line, col: 0},
		end:         selectionPoint{line: line, col: 1},
		active:      true,
		startAnchor: anchor,
		endAnchor:   anchor,
	}
	m.mousePressX, m.mousePressY = 3, 4
	m.dragScrollDir, m.dragScrollTicking = 1, true
	before := m.content.structureGen

	m.applyEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "rejected-call", "", errors.New("api: rejected"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))
	m.syncViewport()

	if m.content.structureGen == before {
		t.Fatalf("rejection did not advance structure generation: %d", before)
	}
	if m.selection.hasSelection() {
		t.Fatal("selection retained anchor to removed provisional card")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 || m.dragScrollDir != 0 || m.dragScrollTicking {
		t.Fatalf("drag state survived rejection: press=(%d,%d) dir=%d ticking=%v", m.mousePressX, m.mousePressY, m.dragScrollDir, m.dragScrollTicking)
	}
}

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
	b.appendDelegationEvent(output.NewDelegationStartedEvent(output.DelegationOccurrence{CallID: "original", AgentID: "child"}, "original task", "", ""))
	if accepted.parentCallID != "original" || accepted.agentID != "child" {
		t.Fatalf("late original-child event rebound: %#v", accepted)
	}
}
