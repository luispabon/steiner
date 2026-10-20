package tui

import "strings"

func (m *Model) handleLeftClick(termY int) {
	// The viewport content area starts below the status bar and input area.
	// We need the content-area row. The viewport itself is positioned at
	// some Y within the terminal — approximate by using termY directly
	// adjusted for scroll offset.
	// content line = termY + m.viewport.YOffset()
	// (viewport renders from its YOffset in the scrollable content)
	contentLine := termY + m.viewport.YOffset() - m.contentTopPad - m.viewportContentTopOffset()

	if contentLine < 0 || len(m.content.segmentHeights) == 0 {
		return
	}

	segIndex, rowInSegment, ok := m.content.segmentAtContentLine(contentLine)
	if !ok {
		return
	}
	m.handleSegmentClick(&m.content.segments[segIndex], rowInSegment)
}

func (m *Model) viewportContentTopOffset() int {
	// ContentPane normally pads the viewport down by one row, and the scrollbar
	// layout replaces that with a leading blank row, so content starts one row
	// below the pane top in both cases.
	return 1
}

// contentLineAtScreenY converts a viewport frame row to the unpadded content
// line it currently displays, accounting for scroll offset, the content pane's
// leading blank row, and the prepended pad rows.
func (m *Model) contentLineAtScreenY(y int) int {
	return y + m.viewport.YOffset() - m.viewportContentTopOffset() - m.contentTopPad
}

// screenYAtContentLine converts an unpadded content line back to the viewport
// frame row it projects to at the current scroll offset.
func (m *Model) screenYAtContentLine(line int) int {
	return line - m.viewport.YOffset() + m.viewportContentTopOffset() + m.contentTopPad
}

// viewportContentTopRow returns the first frame row that displays viewport
// content (the content pane's leading blank row is chrome, not content).
func (m *Model) viewportContentTopRow() int {
	return m.viewportContentTopOffset()
}

// viewportContentBottomRow returns the last frame row that displays viewport
// content at the current viewport height.
func (m *Model) viewportContentBottomRow() int {
	return m.viewportContentTopRow() + m.viewport.Height() - 1
}

// segmentHeight returns the rendered height of segment i, treating a missing
// trailing height as 0 so the mapping walks tolerate segmentHeights being
// shorter than segments (content appended but debounced sync not yet run).
func (b *contentBuffer) segmentHeight(i int) int {
	if i >= 0 && i < len(b.segmentHeights) {
		return b.segmentHeights[i]
	}
	return 0
}

func (b *contentBuffer) segmentAtContentLine(contentLine int) (segIndex int, rowInSegment int, ok bool) {
	if contentLine < 0 {
		return 0, 0, false
	}

	cumulative := 0
	lastKind := contentSegmentKind(-1)
	firstVisible := true
	for i := range b.segments {
		if b.skipHiddenSegment(i) {
			continue
		}

		seg := &b.segments[i]
		if !firstVisible && joinSeparator(lastKind, seg.kind) == "\n\n" {
			if contentLine == cumulative {
				return 0, 0, false
			}
			cumulative++
		}

		h := b.segmentHeight(i)
		if h > 0 && contentLine < cumulative+h {
			return i, contentLine - cumulative, true
		}
		cumulative += h
		lastKind = seg.kind
		firstVisible = false
	}

	return 0, 0, false
}

// contentLineForSegmentRow is the inverse of segmentAtContentLine: it maps a
// rendered row of a segment to its absolute unpadded content line. The walk
// mirrors segmentAtContentLine (hidden segments skipped, user-margin blank
// lines counted) but uses the pure isSegmentHidden so it never mutates render
// state.
func (b *contentBuffer) contentLineForSegmentRow(segIndex, rowInSeg int) (int, bool) {
	if segIndex < 0 || segIndex >= len(b.segments) {
		return 0, false
	}
	cumulative := 0
	lastKind := contentSegmentKind(-1)
	firstVisible := true
	for i := 0; i <= segIndex; i++ {
		if b.isSegmentHidden(i) {
			continue
		}
		seg := &b.segments[i]
		if !firstVisible && joinSeparator(lastKind, seg.kind) == "\n\n" {
			cumulative++
		}
		if i == segIndex {
			h := b.segmentHeight(i)
			if h <= 0 || rowInSeg < 0 || rowInSeg >= h {
				return 0, false
			}
			return cumulative + rowInSeg, true
		}
		cumulative += b.segmentHeight(i)
		lastKind = seg.kind
		firstVisible = false
	}
	return 0, false
}

func (b *contentBuffer) toolCallGroupEntryAtRow(group *toolCallGroupSegment, rowInSegment, width int) int {
	if group == nil || rowInSegment <= 0 {
		return -1
	}
	row := rowInSegment - 1
	for i, tc := range group.entries {
		frameRows := b.toolCallFrameRowCount(tc, width)
		if row < frameRows {
			return i
		}
		row -= frameRows
		if i < len(group.entries)-1 {
			if row == 0 {
				return -1
			}
			row--
		}
	}
	return -1
}

// delegationGroupEntryAtRow maps a rendered row of a delegation group segment to
// the entry it belongs to and the row within that entry's content rows.
// Returns (-1, -1) for the box borders and inter-entry dividers.
func (b *contentBuffer) delegationGroupEntryAtRow(group *delegationGroupSegment, rowInSegment, width int) (entry, rowInEntry int) {
	if group == nil || rowInSegment <= 0 {
		return -1, -1
	}
	row := rowInSegment - 1
	for i, dd := range group.entries {
		contentRowCount := len(b.delegationContentRows(dd, width))
		if row < contentRowCount {
			return i, row
		}
		row -= contentRowCount
		if i < len(group.entries)-1 {
			if row == 0 {
				return -1, -1
			}
			row--
		}
	}
	return -1, -1
}

func (b *contentBuffer) toolCallFrameRowCount(tc *toolCallSegment, width int) int {
	if tc == nil {
		return 0
	}
	frame := b.renderToolCallFrame(tc, width)
	if frame == "" {
		return 0
	}
	return strings.Count(frame, "\n") + 1
}

// delegationRowAction maps a row of one delegation's content rows to a click
// action: 0 = toggle the box, 1 = toggle the prompt, -1 = no action.
func (m *Model) delegationRowAction(dd *delegationDisplayState, contentRow int) int {
	if dd == nil || contentRow < 0 {
		return -1
	}
	contentRows := m.content.delegationContentRows(dd, m.contentRenderWidth())
	if contentRow >= len(contentRows) {
		return -1
	}
	row := contentRows[contentRow]
	switch {
	case row.kind == delegationRowHeader:
		return 0
	case row.kind == delegationRowPromptHeader && !dd.collapsed && strings.TrimSpace(dd.promptText) != "":
		return 1
	case delegationRowIsInteractive(row.kind):
		return -1
	default:
		return -1
	}
}

func (m *Model) handleDelegationClick(seg *contentSegment, rowInSegment int) bool {
	if seg == nil || seg.kind != segmentDelegation || seg.delegData == nil {
		return false
	}
	row := m.delegationRowInSegment(seg.delegData, rowInSegment)
	switch row {
	case 0:
		seg.delegData.collapsed = !seg.delegData.collapsed
		return true
	case 1:
		if seg.delegData.collapsed || strings.TrimSpace(seg.delegData.promptText) == "" {
			return false
		}
		seg.delegData.promptCollapsed = !seg.delegData.promptCollapsed
		return true
	default:
		return false
	}
}

func (m *Model) delegationRowInSegment(dd *delegationDisplayState, rowInSegment int) int {
	if dd == nil || rowInSegment < 0 {
		return -1
	}
	rows := m.content.delegationRows(dd, m.contentRenderWidth())
	if rowInSegment >= len(rows) {
		return -1
	}
	row := rows[rowInSegment]
	if row.kind == delegationRowBorderTop || row.kind == delegationRowHeader {
		return 0
	}
	return m.delegationRowAction(dd, rowInSegment-1)
}

func (m *Model) handleSegmentClick(seg *contentSegment, rowInSegment int) {
	switch seg.kind {
	case segmentToolCall:
		m.handleToolCallClick(seg)
	case segmentToolCallGroup:
		m.handleToolCallGroupClick(seg, rowInSegment)
	case segmentDelegation:
		m.handleDelegationSegmentClick(seg, rowInSegment)
	case segmentDelegationGroup:
		m.handleDelegationGroupClick(seg, rowInSegment)
	case segmentThinkingBlock:
		m.handleThinkingBlockClick(seg)
	}
}

func (m *Model) handleToolCallClick(seg *contentSegment) {
	if seg.toolData != nil {
		if seg.toolData.approvalPending && !seg.toolData.approvalResolved {
			return
		}
		seg.toolData.collapsed = !seg.toolData.collapsed
		seg.renderDirty = true
		m.content.gen++
		m.syncViewport()
	}
}

func (m *Model) handleToolCallGroupClick(seg *contentSegment, rowInSegment int) {
	if seg.toolGroupData == nil {
		return
	}
	entryIndex := m.content.toolCallGroupEntryAtRow(seg.toolGroupData, rowInSegment, m.contentRenderWidth())
	if entryIndex < 0 || entryIndex >= len(seg.toolGroupData.entries) {
		return
	}
	entry := seg.toolGroupData.entries[entryIndex]
	if entry == nil {
		return
	}
	if entry.approvalPending && !entry.approvalResolved {
		return
	}
	entry.collapsed = !entry.collapsed
	seg.renderDirty = true
	m.content.gen++
	m.syncViewport()
}

func (m *Model) handleDelegationSegmentClick(seg *contentSegment, rowInSegment int) {
	if m.handleDelegationClick(seg, rowInSegment) {
		seg.renderDirty = true
		m.content.gen++
		m.syncViewport()
	}
}

func (m *Model) handleDelegationGroupClick(seg *contentSegment, rowInSegment int) {
	if seg.delegGroupData == nil {
		return
	}
	entry, rowInEntry := m.content.delegationGroupEntryAtRow(seg.delegGroupData, rowInSegment, m.contentRenderWidth())
	if entry < 0 {
		return
	}
	action := m.delegationRowAction(seg.delegGroupData.entries[entry], rowInEntry)
	switch action {
	case 0:
		seg.delegGroupData.entries[entry].collapsed = !seg.delegGroupData.entries[entry].collapsed
		seg.renderDirty = true
		m.content.gen++
		m.syncViewport()
	case 1:
		seg.delegGroupData.entries[entry].promptCollapsed = !seg.delegGroupData.entries[entry].promptCollapsed
		seg.renderDirty = true
		m.content.gen++
		m.syncViewport()
	}
}

func (m *Model) handleThinkingBlockClick(seg *contentSegment) {
	if seg.thinkData != nil {
		seg.thinkData.collapsed = !seg.thinkData.collapsed
		seg.renderDirty = true
		m.content.gen++
		m.syncViewport()
	}
}
