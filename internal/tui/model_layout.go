package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const delegationBodyOverhead = 9

func (m *Model) contentWidth() int {
	contentWidth := m.width
	if m.sidebar.Visible(m.width) {
		contentWidth = m.width - sidebarWidth - 1
	}
	return max(1, contentWidth)
}

func (m *Model) layout() {
	contentWidth := m.contentWidth()
	// ContentPane has PaddingTop(1)+PaddingLeft(3)+PaddingRight(3), so inner = contentWidth-6.
	// Total rows: top_pad(1) + viewport + hDivider(1) + input + activity + status(1).
	// The composer renders as a padded message-style card, so derive its height.
	inputRows, activityRows := m.computeInputRows(contentWidth)
	maxInputRows := max(1, m.height-4-activityRows)
	inputRows = min(inputRows, maxInputRows)
	m.viewport.SetWidth(max(1, contentWidth-6))
	m.viewport.SetHeight(max(1, m.height-3-inputRows-activityRows))
	// Set max delegation body lines: viewport height minus overhead for border/header/stats/hint.
	// Overhead: lipgloss border (2) + blank after box (1) + hint+newline (2) + header (1) + separator (1) + stats (1) = 8.
	// Using delegationBodyOverhead leaves one spare row so the box never grazes the viewport edge.
	m.content.maxDelegationBodyLines = max(0, m.viewport.Height()-delegationBodyOverhead)
	m.syncViewport()
}

// relayoutInput recalculates viewport height after the input content changes
// (e.g. on each keystroke). Cheaper than layout(): skips syncViewport when
// height is unchanged, avoiding a full content re-render on every key press.
func (m *Model) relayoutInput() {
	contentWidth := m.contentWidth()
	inputRows, activityRows := m.computeInputRows(contentWidth)
	maxInputRows := max(1, m.height-4-activityRows)
	inputRows = min(inputRows, maxInputRows)
	newHeight := max(1, m.height-3-inputRows-activityRows)
	if newHeight == m.viewport.Height() {
		return
	}
	m.viewport.SetHeight(newHeight)
	m.content.maxDelegationBodyLines = max(0, m.viewport.Height()-delegationBodyOverhead)
	m.syncViewport()
}

func (m *Model) computeInputRows(contentWidth int) (inputRows, activityRows int) {
	// Match the textarea's internal wrap width to the composer's render width.
	// The old SetWidth(99999) disabled the textarea's internal soft wrap so that
	// LineInfo().ColumnOffset reported an absolute cursor offset; cursor
	// placement now uses m.input.Column(), which is width-independent, so the
	// textarea can run at its natural width. Bubbles' word-wrap is quadratic in
	// row width, so the old width made every keystroke re-wrap the entire cursor
	// line with full grapheme segmentation instead of rows capped at the visible
	// width.
	m.input.SetWidth(m.inputInnerWidth(contentWidth))
	inputRows = m.inputChromeHeight(contentWidth)
	activityRows = m.activityRowHeight(contentWidth)
	return inputRows, activityRows
}

// setViewportContent is the single choke point for viewport content updates.
// The scroll model owns the one line slice visibleViewportContent slices, and
// vpViewCache invalidation lives here; calling m.viewport.SetContent or
// m.viewport.SetLines directly would leave vpViewCache stale and serve frames
// that disagree with the new content. Do not bypass this choke point.
func (m *Model) setViewportContent(rendered string) {
	m.viewport.SetContent(rendered)
	// vpViewCache is keyed on scroll position, width and scrollbar presence,
	// none of which change when only the content does. Invalidating here
	// rather than in syncViewport keeps the cache tied to the choke point
	// this comment documents.
	m.vpViewCache = ""
}

func (m *Model) syncViewport() {
	rendered := m.content.String(m.viewport.Width())

	// WithBg re-inserts the background escape after every ANSI reset in rendered
	// content. This is necessary because terminals with transparency enabled
	// composite ANSI resets (\x1b[0m) with their transparency setting, producing
	// transparent gaps whenever nested lipgloss/glamour renders emit a reset.
	// lipgloss Background() on a container does NOT fix this — it only fills
	// padding/border cells. WithBg + PadLines ensures every cell in the viewport
	// has an explicit SGR 48 background, making content fully opaque.
	// The cache avoids re-running the O(n) byte scan on scroll-only updates where
	// content hasn't changed (m.content.String returns the same string value).
	widthChanged := m.viewport.Width() != m.fmtBgCacheWidth
	if rendered != m.fmtBgCacheInput || widthChanged {
		m.fmtBgCacheInput = rendered
		m.fmtBgCacheWidth = m.viewport.Width()
		formatted := theme.WithBg(rendered, theme.BgElev)
		m.fmtBgCacheOutput = theme.PadLines(formatted, m.viewport.Width(), theme.BgElev)

		// A width reflow invalidates row/col anchors (wrapping changes the
		// rendered rows), so the selection clears; same-width content changes
		// remap the selection through segment anchors and no longer clear it.
		// Scroll-only and pure viewport-height changes do not enter this block,
		// so their selections survive, and input-region drags are screen-anchored
		// and must survive reflow untouched.
		if m.activeRegion == regionViewport {
			if widthChanged {
				m.clearSelectionAndDrag()
			} else {
				m.remapViewportSelection()
			}
		}
	}
	rendered = m.fmtBgCacheOutput

	contentLines := strings.Count(rendered, "\n") + 1
	pad := m.viewport.Height() - contentLines
	if pad < 0 {
		pad = 0
	}
	m.contentTopPad = pad
	if pad > 0 {
		if m.padLineCacheWidth != m.viewport.Width() || m.padLineCacheRendered == "" {
			m.padLineCacheWidth = m.viewport.Width()
			m.padLineCacheRendered = lipgloss.NewStyle().
				Background(lipgloss.Color(theme.BgElev)).
				Render(strings.Repeat(" ", m.viewport.Width()))
		}
		rendered = strings.Repeat(m.padLineCacheRendered+"\n", pad) + rendered
	}
	m.setViewportContent(rendered)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) scrollUp(lines int) {
	if lines < 1 {
		lines = 1
	}
	m.autoScroll = false
	m.viewport.ScrollUp(lines)
}

func (m *Model) scrollDown(lines int) {
	if lines < 1 {
		lines = 1
	}
	m.viewport.ScrollDown(lines)
	if m.viewport.AtBottom() {
		m.autoScroll = true
	}
}

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
	contentRows := m.content.delegationContentRows(dd, m.viewport.Width())
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
	rows := m.content.delegationRows(dd, m.viewport.Width())
	if rowInSegment >= len(rows) {
		return -1
	}
	row := rows[rowInSegment]
	if row.kind == delegationRowBorderTop || row.kind == delegationRowHeader {
		return 0
	}
	return m.delegationRowAction(dd, rowInSegment-1)
}

func (m *Model) renderScrollbar() string {
	totalContent := m.viewport.TotalLineCount()
	if totalContent <= m.viewport.Height() {
		return ""
	}

	vh := m.viewport.Height()
	if vh <= 0 {
		return ""
	}

	// Check cache before recomputing.
	cacheKey := scrollbarCacheKey{yOffset: m.viewport.YOffset(), height: vh, totalLines: totalContent}
	if m.scrollbarCacheKey == cacheKey && m.scrollbarCacheRendered != "" {
		return m.scrollbarCacheRendered
	}

	thumbH := max(1, vh*vh/totalContent)
	trackH := vh - thumbH

	scrollRange := totalContent - vh
	var thumbPos int
	if scrollRange > 0 && trackH > 0 {
		thumbPos = int(float64(m.viewport.YOffset()) / float64(scrollRange) * float64(trackH))
	}
	if thumbPos > trackH {
		thumbPos = trackH
	}

	style := m.styles.Scrollbar
	trackStyle := m.styles.ScrollbarTrack
	if m.scrollbarCellStyles != m.styles {
		m.scrollbarCellStyles = m.styles
		m.scrollbarThumbCell = style.Render("▕")
		m.scrollbarTrackCell = trackStyle.Render(" ")
	}
	var sb strings.Builder
	sb.Grow(vh * (max(len(m.scrollbarThumbCell), len(m.scrollbarTrackCell)) + 1))
	sb.WriteString(strings.Repeat(m.scrollbarTrackCell+"\n", thumbPos))
	sb.WriteString(strings.Repeat(m.scrollbarThumbCell+"\n", thumbH))
	sb.WriteString(strings.Repeat(m.scrollbarTrackCell+"\n", vh-thumbPos-thumbH))
	result := strings.TrimSuffix(sb.String(), "\n")
	m.scrollbarCacheKey = cacheKey
	m.scrollbarCacheRendered = result
	return result
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
	entryIndex := m.content.toolCallGroupEntryAtRow(seg.toolGroupData, rowInSegment, m.viewport.Width())
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
	entry, rowInEntry := m.content.delegationGroupEntryAtRow(seg.delegGroupData, rowInSegment, m.viewport.Width())
	if entry < 0 {
		return
	}
	if rowInEntry == -1 {
		seg.delegGroupData.entries[entry].collapsed = !seg.delegGroupData.entries[entry].collapsed
		seg.renderDirty = true
		m.content.gen++
		m.syncViewport()
		return
	}
	action := m.delegationRowAction(seg.delegGroupData.entries[entry], rowInEntry)
	if action == 0 {
		seg.delegGroupData.entries[entry].collapsed = !seg.delegGroupData.entries[entry].collapsed
		seg.renderDirty = true
		m.content.gen++
		m.syncViewport()
	} else if action == 1 {
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
