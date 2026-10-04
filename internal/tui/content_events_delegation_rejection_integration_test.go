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
			openParentCard(b, "call")
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
	b := newGroupTestBuffer()
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "first", subAgentArgs("g")))
	first := lastDelegationCard(b)
	b.AppendEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: "first", BatchID: "batch"}, "g"))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "rejected", subAgentArgs("g")))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "last", subAgentArgs("g")))
	last := lastDelegationCard(b)
	b.AppendEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: "last", BatchID: "batch"}, "g"))
	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "rejected", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))
	if len(b.segments) != 1 || b.segments[0].kind != segmentDelegationGroup || len(b.segments[0].delegGroupData.entries) != 2 || b.segments[0].delegGroupData.entries[0] != first || b.segments[0].delegGroupData.entries[1] != last {
		t.Fatalf("accepted frame after rejection = %#v", b.segments)
	}
}

func TestRejectedCurrentCallRewriteRemapsPointersAndKeepsFollowUpChild(t *testing.T) {
	b := newGroupTestBuffer()
	occ := output.DelegationOccurrence{CallID: "original", BatchID: "batch", AgentID: "child"}
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "original", subAgentArgs("shared")))
	accepted := lastDelegationCard(b)
	b.AppendEvent(output.NewDelegationAcceptedEvent(occ, "shared"))
	b.AppendEvent(output.NewDelegationStartedEvent(occ, "original task", "", "explore"))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "bash", "tool", map[string]any{"command": "true"}))
	tool := b.activeToolCalls["tool"].td
	b.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", "current", subAgentArgs("shared")))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "current", subAgentArgs("shared")))
	rejected := lastDelegationCard(b)
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "other", subAgentArgs("shared")))
	other := lastDelegationCard(b)
	b.AppendEvent(output.NewDelegationAcceptedEvent(output.DelegationOccurrence{CallID: "other", BatchID: "batch"}, "shared"))
	b.stringCacheWidth, b.stringCacheBlocks = 80, []string{"stale"}
	b.prefixCacheSet, b.prefixCacheRendered, b.segmentHeights = true, "stale", []int{1, 2, 3}
	structureGen := b.structureGen

	b.appendToolCallFinishedEvent(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "current", "", nil, output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected"}))

	if findDelegationSegment(b.segments, rejected) >= 0 {
		t.Fatal("rejected card remains")
	}
	for id, loc := range b.activeDelegations {
		if loc.dd == rejected {
			t.Fatalf("active locator %q still points at rejected card", id)
		}
	}
	for _, loc := range b.pendingDelegationStarts {
		if loc.dd == rejected {
			t.Fatal("pending locator still points at rejected card")
		}
	}
	for _, loc := range b.openDelegations {
		if loc.dd == rejected {
			t.Fatal("open locator still points at rejected card")
		}
	}
	if _, ok := b.queuedDelegations["current"]; ok {
		t.Fatalf("queued locator remains: %#v", b.queuedDelegations)
	}
	if b.activeDelegations["child"].dd != accepted || b.activeDelegations["child"].seg != 0 {
		t.Fatalf("active locators = %#v", b.activeDelegations)
	}
	if b.activeToolCalls["tool"].td != tool || b.activeToolCalls["tool"].seg != 1 {
		t.Fatalf("tool locator = %#v", b.activeToolCalls)
	}
	if b.structureGen != structureGen+1 || b.segmentHeights != nil || b.stringCacheWidth != 0 || b.stringCacheBlocks != nil || b.prefixCacheSet || b.prefixCacheRendered != "" {
		t.Fatalf("rewrite caches/generation not invalidated: structure=%d heights=%v string=%d/%q prefix=%v/%q", b.structureGen, b.segmentHeights, b.stringCacheWidth, b.stringCacheBlocks, b.prefixCacheSet, b.prefixCacheRendered)
	}
	if len(b.segments) != 2 || b.segments[0].kind != segmentDelegationGroup || b.segments[0].delegGroupData.entries[0] != accepted || b.segments[0].delegGroupData.entries[1] != other || b.segments[1].toolData != tool {
		t.Fatalf("rewritten layout = %#v", b.segments)
	}

	b.handleFollowUpToolCallStarted(output.ToolCallStartedEvent{CallID: "follow-up", Arguments: map[string]any{"agent_id": "child", "message": "continue"}})
	var followUp *delegationDisplayState
	b.forEachDelegationReverse(func(loc delegationLocator) bool {
		if loc.dd != accepted && loc.dd != other {
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
	b.appendDelegationEvent(output.NewDelegationStartedEvent(occ, "original task", "", ""))
	if accepted.parentCallID != "original" || accepted.agentID != "child" || countDelegationCards(b.segments) != 2 {
		t.Fatalf("late original-child event rebound or duplicated: %#v", accepted)
	}
}
