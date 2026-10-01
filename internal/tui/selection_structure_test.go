package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

func TestSelectionStructureRegroupInvalidatesStaleAnchor(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	first := &delegationDisplayState{parentCallID: "first", group: "work", batchID: "batch", groupAccepted: true}
	second := &delegationDisplayState{parentCallID: "second", group: "work", batchID: "batch", groupAccepted: true}
	m.content.segments = []contentSegment{
		{kind: segmentDelegation, delegData: first, cachedRender: "first delegation", renderGen: 7},
		{kind: segmentDelegation, delegData: second, cachedRender: "second delegation", renderGen: 7},
		{kind: segmentPlain, text: "selected row", cachedRender: "selected row", renderGen: 7},
		{kind: segmentPlain, text: "different row", cachedRender: "different row", renderGen: 7},
	}
	m.content.segmentHeights = []int{1, 1, 1, 1}
	m.content.String(m.viewport.Width())
	m.syncViewport()
	selectedSegment := 2
	selectedLine := -1
	for line := range m.viewport.Lines() {
		segIndex, _, ok := m.content.segmentAtContentLine(line)
		if ok && segIndex == selectedSegment {
			selectedLine = line
			break
		}
	}
	if selectedLine < 0 {
		t.Fatal("test setup: selected row not found after initial render")
	}
	line, anchor := m.viewportSelectionEndpoint(selectedLine)
	if !anchor.ok || anchor.segIndex != selectedSegment {
		t.Fatalf("selected row anchor = %#v; want segment %d", anchor, selectedSegment)
	}
	m.activeRegion = regionViewport
	m.selection = selectionState{
		start:       selectionPoint{line: line, col: 0},
		end:         selectionPoint{line: line, col: 6},
		active:      true,
		startAnchor: anchor,
		endAnchor:   anchor,
	}
	m.mousePressX, m.mousePressY = 3, 4
	m.dragScrollDir, m.dragScrollTicking = 1, true

	m.content.regroupAcceptedDelegations()
	if len(m.content.segments) != 3 || m.content.segments[0].kind != segmentDelegationGroup {
		t.Fatalf("regrouped segments = %#v; want delegation group, selected row, and different row", m.content.segments)
	}
	if m.content.segments[1].text != "selected row" || m.content.segments[2].text != "different row" {
		t.Fatalf("segments after regroup = %#v; want selected row at 1 and different row at stale index 2", m.content.segments)
	}
	m.content.String(m.viewport.Width())
	group := &m.content.segments[0]
	group.cachedRender = "regrouped delegation"
	group.cachedRenderWidth = m.viewport.Width()
	group.renderGen = anchor.renderGen
	group.renderDirty = false
	occupant := &m.content.segments[anchor.segIndex]
	occupant.renderGen = anchor.renderGen
	occupant.cachedRenderWidth = m.viewport.Width()
	occupant.renderDirty = false
	if occupant.renderGen != anchor.renderGen {
		t.Fatalf("stale index render generation = %d; want anchor generation %d", occupant.renderGen, anchor.renderGen)
	}
	point := selectionPoint{line: line, col: 1}
	stale := anchor
	if m.remapEndpoint(&point, &stale) {
		t.Fatal("stale anchor remapped to a different segment with an equal render generation")
	}
	valid := m.content.selectionAnchorForSegmentRow(anchor.segIndex, anchor.rowInSeg)
	validPoint := selectionPoint{line: line, col: 1}
	if !m.remapEndpoint(&validPoint, &valid) {
		t.Fatal("control: current anchor at the same segment index and render generation did not remap")
	}
	m.syncViewport()
	if m.content.segments[anchor.segIndex].renderGen != anchor.renderGen {
		t.Fatalf("same-index occupant render generation at model remap = %d; want %d", m.content.segments[anchor.segIndex].renderGen, anchor.renderGen)
	}
	if m.selection.hasSelection() {
		t.Fatal("selection survived regroup with a stale segment index")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 || m.dragScrollDir != 0 || m.dragScrollTicking {
		t.Fatalf("drag state survived regroup: press=(%d,%d) dir=%d ticking=%v", m.mousePressX, m.mousePressY, m.dragScrollDir, m.dragScrollTicking)
	}
}

func TestSelectionStructureNonStructuralRenderRetainsSelection(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	m.syncViewport()
	line := -1
	for i, rendered := range m.viewport.Lines() {
		if rendered != "" {
			if seg, _, ok := m.content.segmentAtContentLine(i); ok && m.content.segments[seg].kind == segmentDelegation {
				line = i
				break
			}
		}
	}
	if line < 0 {
		t.Fatal("test setup: delegation row not found")
	}
	anchor := m.content.selectionAnchorForSegmentRow(0, 0)
	m.activeRegion = regionViewport
	m.selection = selectionState{
		start:       selectionPoint{line: line, col: 0},
		end:         selectionPoint{line: line, col: 1},
		active:      true,
		startAnchor: anchor,
		endAnchor:   anchor,
	}
	beforeGen := m.content.structureGen
	beforeRender := m.content.segments[0].renderGen
	before := m.content.String(m.viewport.Width())
	m.content.AdvanceDelegationSpinners()
	m.syncViewport()
	after := m.content.String(m.viewport.Width())
	if before == after {
		t.Fatal("test setup: spinner tick did not change rendered output")
	}
	if m.content.segments[0].renderGen == beforeRender {
		t.Fatal("test setup: spinner tick did not advance render generation")
	}
	if m.content.structureGen != beforeGen {
		t.Fatalf("spinner tick changed structure generation from %d to %d", beforeGen, m.content.structureGen)
	}
	if !m.selection.hasSelection() {
		t.Fatal("selection cleared by non-structural spinner repaint")
	}
}

func TestSelectionStructureClearInvalidatesAnchor(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("same row")
	m.syncViewport()
	anchor := m.content.selectionAnchorForSegmentRow(0, 0)
	if !anchor.ok {
		t.Fatal("test setup: content row has no segment anchor")
	}
	before := m.content.structureGen
	m.content.Clear()
	if m.content.structureGen == 0 || m.content.structureGen == before {
		t.Fatalf("Clear structure generation = %d; prior %d", m.content.structureGen, before)
	}
	m.content.AppendLine("same row")
	m.syncViewport()
	if m.content.segments[0].renderGen != anchor.renderGen {
		t.Fatalf("test setup: render generation differs, got %d want %d", m.content.segments[0].renderGen, anchor.renderGen)
	}
	line, ok := m.content.contentLineForSegmentRow(0, 0)
	if !ok {
		t.Fatal("test setup: replacement row has no content line")
	}
	point := selectionPoint{line: line, col: 1}
	if m.remapEndpoint(&point, &anchor) {
		t.Fatal("old selection anchor remapped after Clear and identical content replacement")
	}
}
