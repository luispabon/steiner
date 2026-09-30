package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/luispabon/steiner/internal/output"
)

func TestDelegationIntegrationEventsJoinAcrossResponses(t *testing.T) {
	useTrueColor(t)
	b := newGroupTestBuffer()
	b.styles = testStyles("#5599ff")
	appendChild := func(id, callID, task string) {
		args := map[string]any{"type": "explore", "task": task, "group": "shared"}
		b.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", callID, args))
		b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", callID, args))
		b.AppendEvent(output.NewDelegationStartedEvent(id, task, callID))
	}
	appendChild("child-1", "call-1", "first source task")
	b.AppendEvent(output.NewThinkingChunkEventWithSource(1, "hidden reasoning source", output.ChunkSourceAssistant))
	b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "tool_calls", nil))
	b.AppendEvent(output.NewAssistantMessageEvent(2, "assistant", ""))
	appendChild("child-2", "call-2", "second source task")

	if len(b.segments) != 3 || b.segments[0].kind != segmentDelegation || b.segments[1].kind != segmentThinkingBlock || b.segments[2].kind != segmentDelegation {
		t.Fatalf("event segments = %v, want delegation, thinking, delegation", segmentKinds(b.segments))
	}
	joined := b.String(76)
	if b.segments[0].delegationJoinedAbove || !b.segments[2].delegationJoinedAbove {
		t.Fatalf("hidden-thinking join flags = (%t, %t), want (false, true)", b.segments[0].delegationJoinedAbove, b.segments[2].delegationJoinedAbove)
	}
	if b.segmentHeights[0] == 0 || b.segmentHeights[1] != 0 || b.segmentHeights[2] == 0 || !strings.Contains(joined, "2 agents") {
		t.Fatalf("hidden response content did not render as joined source cards, heights=%v output=%q", b.segmentHeights, joined)
	}

	b.showThinking = true
	split := b.String(76)
	if b.segments[2].delegationJoinedAbove || b.segmentHeights[1] == 0 || strings.Index(split, "hidden reasoning source") > strings.Index(split, "second source task") {
		t.Fatalf("revealed thinking did not split cards in event order, heights=%v output=%q", b.segmentHeights, split)
	}
	b.showThinking = false
	b.String(76)
	if !b.segments[2].delegationJoinedAbove || b.segmentHeights[1] != 0 {
		t.Fatalf("hidden thinking did not restore join, flags/heights=(%t,%v)", b.segments[2].delegationJoinedAbove, b.segmentHeights)
	}
}

func TestDelegationIntegrationRenderedRowsMapAndClicks(t *testing.T) {
	useTrueColor(t)
	b := newGroupTestBuffer()
	b.styles = testStyles("#5599ff")
	args := map[string]any{"type": "explore", "task": "first prompt", "group": "shared"}
	b.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", "call-1", args))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-1", args))
	b.AppendEvent(output.NewDelegationStartedEvent("child-1", "first prompt", "call-1"))
	b.AppendEvent(output.NewToolCallQueuedEvent(1, "sub_agent", "call-2", map[string]any{"type": "explore", "task": "second prompt", "group": "shared"}))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", "call-2", map[string]any{"type": "explore", "task": "second prompt", "group": "shared"}))
	b.AppendEvent(output.NewDelegationStartedEvent("child-2", "second prompt", "call-2"))
	b.String(76)

	for segIndex := range b.segments {
		for row := 0; row < b.segmentHeight(segIndex); row++ {
			line, ok := b.contentLineForSegmentRow(segIndex, row)
			if !ok {
				t.Fatalf("segment %d row %d has no rendered line", segIndex, row)
			}
			gotSeg, gotRow, ok := b.segmentAtContentLine(line)
			if !ok || gotSeg != segIndex || gotRow != row {
				t.Fatalf("segment %d row %d round-trips to (%d,%d,%t)", segIndex, row, gotSeg, gotRow, ok)
			}
		}
	}

	groupIndex := -1
	for i := range b.segments {
		if b.segments[i].kind == segmentDelegationGroup {
			groupIndex = i
			break
		}
	}
	if groupIndex < 0 {
		t.Fatalf("events produced no grouped delegation card: %v", segmentKinds(b.segments))
	}
	seg := &b.segments[groupIndex]
	if len(seg.delegGroupData.entries) != 2 {
		t.Fatalf("group entries = %d, want 2", len(seg.delegGroupData.entries))
	}
	m := newModel(Config{}, nil)
	m.content = *b
	m.viewport.SetWidth(76)
	m.viewport.SetHeight(30)
	first, second := seg.delegGroupData.entries[0], seg.delegGroupData.entries[1]
	if !first.collapsed || !second.collapsed {
		t.Fatal("event-created cards should start collapsed")
	}
	m.handleSegmentClick(seg, m.content.segmentHeight(groupIndex)-1)
	if !first.collapsed || !second.collapsed {
		t.Fatal("group footer click changed card state")
	}
	entry, _ := m.content.delegationGroupEntryAtRow(seg.delegGroupData, 1, 76)
	if entry != 0 {
		t.Fatalf("first source header maps to entry %d, want 0", entry)
	}
	firstRows := len(m.content.delegationContentRows(first, 76))
	dividerRow := 1 + firstRows
	if entry, row := m.content.delegationGroupEntryAtRow(seg.delegGroupData, dividerRow, 76); entry != -1 || row != -1 {
		t.Fatalf("divider maps to entry/row (%d,%d), want (-1,-1)", entry, row)
	}
	m.handleSegmentClick(seg, dividerRow)
	if !first.collapsed || !second.collapsed {
		t.Fatal("group divider click changed card state")
	}
	m.handleSegmentClick(seg, 1)
	if first.collapsed || !second.collapsed {
		t.Fatalf("first header click states = (%t,%t), want (false,true)", first.collapsed, second.collapsed)
	}
	second.collapsed = false
	second.promptCollapsed = true
	promptHeaderRow := 1 + firstRows + 2 // top border, first card, divider, second header
	m.handleSegmentClick(seg, promptHeaderRow)
	if !second.promptCollapsed || second.collapsed {
		t.Fatal("joined prompt-header click changed a source other than its prompt")
	}
}

func TestDelegationIntegrationSelectionTracksSourceAcrossVisibilityAndAppend(t *testing.T) {
	useTrueColor(t)
	m := newModel(Config{}, nil)
	m.content = *newGroupTestBuffer()
	m.content.styles = testStyles("#5599ff")
	appendChild := func(id, callID, task string) {
		args := map[string]any{"type": "explore", "task": task, "group": "shared"}
		m.content.AppendEvent(output.NewToolCallStartedEvent(1, "sub_agent", callID, args))
		m.content.AppendEvent(output.NewDelegationStartedEvent(id, task, callID))
	}
	appendChild("child-1", "call-1", "first source")
	m.content.AppendEvent(output.NewThinkingChunkEventWithSource(1, "hidden reasoning", output.ChunkSourceAssistant))
	m.content.AppendEvent(output.NewAPIResponseEvent(nil, nil, "tool_calls", nil))
	m.content.AppendEvent(output.NewAssistantMessageEvent(2, "assistant", ""))
	appendChild("child-2", "call-2", "second source")
	const secondSeg = 2
	second := m.content.segments[secondSeg].delegData
	second.collapsed = false
	second.promptCollapsed = false
	m.content.segments[secondSeg].renderDirty = true
	m.content.gen++
	m.viewport.SetWidth(76)
	m.viewport.SetHeight(30)
	m.syncViewport()

	contentRows := m.content.delegationContentRows(second, 76)
	rowInSegment := -1
	for row, contentRow := range contentRows {
		if contentRow.kind == delegationRowPromptBody && strings.Contains(ansi.Strip(contentRow.text), "second source") {
			rowInSegment = row + 1 // add the rendered card's top border row
			break
		}
	}
	if rowInSegment < 1 || !strings.Contains(ansi.Strip(m.content.segmentRenderedLines(secondSeg)[rowInSegment]), "second source") {
		t.Fatal("stable second-source prompt body has no matching rendered row")
	}
	line, ok := m.content.contentLineForSegmentRow(secondSeg, rowInSegment)
	if !ok {
		t.Fatalf("second source prompt row %d has no rendered line", rowInSegment)
	}
	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{line: line, col: 2}, end: selectionPoint{line: line, col: 5}, active: true}
	m.selection.startAnchor = m.content.selectionAnchorForSegmentRow(secondSeg, rowInSegment)
	m.selection.endAnchor = m.content.selectionAnchorForSegmentRow(secondSeg, rowInSegment)
	assertSelectionOnSecondSource := func(stage string) {
		t.Helper()
		if !m.selection.hasSelection() {
			t.Fatalf("selection cleared after %s", stage)
		}
		for name, point := range map[string]selectionPoint{"start": m.selection.start, "end": m.selection.end} {
			got, _, ok := m.content.segmentAtContentLine(point.line)
			if !ok || got != secondSeg {
				t.Fatalf("%s endpoint after %s maps to segment %d (ok=%t), want source segment %d", name, stage, got, ok, secondSeg)
			}
		}
		if m.selection.startAnchor.rowText != m.selection.endAnchor.rowText || !strings.Contains(m.selection.startAnchor.rowText, "second source") {
			t.Fatalf("selection after %s lost stable prompt-body anchor: %q / %q", stage, m.selection.startAnchor.rowText, m.selection.endAnchor.rowText)
		}
	}

	m.content.showThinking = true
	m.content.segments[1].renderDirty = true
	m.content.gen++
	m.syncViewport()
	assertSelectionOnSecondSource("revealing thinking")

	appendChild("child-3", "call-3", "third source")
	m.syncViewport()
	assertSelectionOnSecondSource("appending a source")
	if len(m.content.segments[secondSeg].delegGroupData.entries) == 0 || m.content.segments[secondSeg].delegGroupData.entries[0] != second {
		t.Fatal("joined group no longer starts with the original second-source entry")
	}

	m.content.showThinking = false
	m.content.segments[1].renderDirty = true
	m.content.gen++
	m.syncViewport()
	assertSelectionOnSecondSource("hiding thinking")
	if !m.content.segments[secondSeg].delegationJoinedAbove {
		t.Fatal("second-source segment did not rejoin after hiding thinking")
	}
}

func segmentKinds(segments []contentSegment) []contentSegmentKind {
	kinds := make([]contentSegmentKind, len(segments))
	for i := range segments {
		kinds[i] = segments[i].kind
	}
	return kinds
}
