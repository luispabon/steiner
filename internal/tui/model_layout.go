package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const delegationBodyOverhead = 9

func (m Model) contentWidth() int {
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
	m.input.MaxWidth = 0
	m.input.SetWidth(99999)
	inputRows = m.inputChromeHeight(contentWidth)
	activityRows = m.activityRowHeight(contentWidth)
	return inputRows, activityRows
}

func (m *Model) syncViewport() {
	m.vpViewCache = ""
	rendered := m.content.String(m.viewport.Width())
	if m.showContextDiagnostics {
		if header := m.renderContextInfoLine(m.viewport.Width()); header != "" {
			rendered = header + rendered
		}
	}

	// WithBg re-inserts the background escape after every ANSI reset in rendered
	// content. This is necessary because terminals with transparency enabled
	// composite ANSI resets (\x1b[0m) with their transparency setting, producing
	// transparent gaps whenever nested lipgloss/glamour renders emit a reset.
	// lipgloss Background() on a container does NOT fix this — it only fills
	// padding/border cells. WithBg + PadLines ensures every cell in the viewport
	// has an explicit SGR 48 background, making content fully opaque.
	// The cache avoids re-running the O(n) byte scan on scroll-only updates where
	// content hasn't changed (m.content.String returns the same string value).
	if rendered != m.fmtBgCacheInput || m.viewport.Width() != m.fmtBgCacheWidth {
		m.fmtBgCacheInput = rendered
		m.fmtBgCacheWidth = m.viewport.Width()
		formatted := theme.WithBg(rendered, theme.BgElev)
		m.fmtBgCacheOutput = theme.PadLines(formatted, m.viewport.Width(), theme.BgElev)
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
	m.viewport.SetContent(rendered)
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

func (m Model) viewportContentTopOffset() int {
	// ContentPane normally pads the viewport down by one row, and the scrollbar
	// layout replaces that with a leading blank row, so content starts one row
	// below the pane top in both cases.
	return 1
}

func (b *contentBuffer) segmentAtContentLine(contentLine int) (segIndex int, rowInSegment int, ok bool) {
	if contentLine < 0 || len(b.segmentHeights) != len(b.segments) {
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

		h := b.segmentHeights[i]
		if h > 0 && contentLine < cumulative+h {
			return i, contentLine - cumulative, true
		}
		cumulative += h
		lastKind = seg.kind
		firstVisible = false
	}

	return 0, 0, false
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
	switch {
	case row.kind == delegationRowBorderTop, row.kind == delegationRowHeader:
		return 0
	case row.kind == delegationRowPromptHeader && !dd.collapsed && strings.TrimSpace(dd.promptText) != "":
		return 1
	case delegationRowIsInteractive(row.kind):
		return -1
	default:
		return -1
	}
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
	var sb strings.Builder
	sb.Grow(vh * 2)
	for i := 0; i < vh; i++ {
		if i >= thumbPos && i < thumbPos+thumbH {
			sb.WriteString(style.Render("▕"))
		} else {
			sb.WriteString(trackStyle.Render(" "))
		}
		if i < vh-1 {
			sb.WriteString("\n")
		}
	}
	result := sb.String()
	m.scrollbarCacheKey = cacheKey
	m.scrollbarCacheRendered = result
	return result
}

func (m *Model) renderContextInfoLine(width int) string {
	hasSessionHealth := m.hasSessionHealthInfo()
	hasContextInfo := m.hasContextUsageInfo()
	if !hasSessionHealth && !hasContextInfo {
		return ""
	}
	line1 := strings.Join(m.contextSessionHealthParts(hasSessionHealth), "; ")
	line2 := m.contextUsageLine()
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgFaint)).
		Italic(true).
		Width(width)
	return style.Render(line1) + "\n" + style.Render(line2) + "\n"
}

func (m *Model) hasSessionHealthInfo() bool {
	return m.sessionHealthState != "" || m.sessionHealthCompactionCount > 0 || m.sessionHealthGuidance != "" || len(m.sessionHealthNotes) > 0
}

func (m *Model) hasContextUsageInfo() bool {
	return m.ctxInfoPromptTokens > 0 || m.ctxInfoContextWindow > 0 || m.ctxInfoContextUsagePercent > 0 || m.ctxInfoCompactionThreshold > 0 || m.ctxInfoEstimatorPadTokens > 0 || strings.TrimSpace(m.ctxInfoStatus) != ""
}

func (m *Model) contextSessionHealthParts(include bool) []string {
	parts := []string{"context info:"}
	if !include {
		return parts
	}

	parts = append(parts, fmt.Sprintf("session health #%d turn %d", m.sessionHealthCompactionCount, m.sessionHealthTurn))
	if m.sessionHealthState != "" {
		parts = append(parts, "state "+m.sessionHealthState)
	}
	if guidance := m.sessionHealthGuidanceEntry(); guidance != "" {
		parts = append(parts, guidance)
	}
	if len(m.sessionHealthNotes) > 0 {
		parts = append(parts, "notes "+strings.Join(m.sessionHealthNotes, ", "))
	}
	return parts
}

func (m *Model) sessionHealthGuidanceEntry() string {
	entry := m.sessionHealthGuidance
	if entry == "" {
		return ""
	}
	if m.sessionHealthCompactionCount <= 0 {
		return entry
	}

	suffix := "compaction"
	if m.sessionHealthCompactionCount != 1 {
		suffix = "compactions"
	}
	return fmt.Sprintf("%s after %d %s", entry, m.sessionHealthCompactionCount, suffix)
}

func (m *Model) contextUsageLine() string {
	return fmt.Sprintf(
		"view full, prompt_tokens=%d context_window=%d context_usage_percent=%s compaction_threshold=%s estimator_pad_tokens=%d status=%s",
		m.ctxInfoPromptTokens,
		m.contextInfoWindow(),
		fmt.Sprintf("%.0f%%", m.ctxInfoContextUsagePercent),
		fmt.Sprintf("%.0f%%", m.ctxInfoCompactionThreshold),
		m.ctxInfoEstimatorPadTokens,
		m.contextInfoStatus(),
	)
}

func (m *Model) contextInfoWindow() int {
	if m.ctxInfoContextWindow > 0 {
		return m.ctxInfoContextWindow
	}
	return m.sidebar.contextBudget
}

func (m *Model) contextInfoStatus() string {
	status := strings.TrimSpace(m.ctxInfoStatus)
	if status == "" {
		return "unknown_context"
	}
	return status
}

func (m *Model) handleSegmentClick(seg *contentSegment, rowInSegment int) {
	switch seg.kind {
	case segmentToolCall:
		m.handleToolCallClick(seg)
	case segmentToolCallGroup:
		m.handleToolCallGroupClick(seg, rowInSegment)
	case segmentDelegation:
		m.handleDelegationSegmentClick(seg, rowInSegment)
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
	m.syncViewport()
}

func (m *Model) handleDelegationSegmentClick(seg *contentSegment, rowInSegment int) {
	if m.handleDelegationClick(seg, rowInSegment) {
		seg.renderDirty = true
		m.syncViewport()
	}
}

func (m *Model) handleThinkingBlockClick(seg *contentSegment) {
	if seg.thinkData != nil {
		seg.thinkData.collapsed = !seg.thinkData.collapsed
		seg.renderDirty = true
		m.syncViewport()
	}
}
