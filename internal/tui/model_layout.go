package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (m *Model) layout() {
	contentWidth := m.width
	if m.sidebar.Visible(m.width) {
		contentWidth = m.width - sidebarWidth - 1 // 1-cell vertical divider
	}
	if contentWidth < 1 {
		contentWidth = 1
	}
	// ContentPane has PaddingTop(1)+PaddingLeft(3)+PaddingRight(3), so inner = contentWidth-6.
	// Total rows: top_pad(1) + viewport + hDivider(1) + approval tray + input + activity + status(1).
	// The composer renders as a padded message-style card, so derive its height.
	m.input.MaxWidth = 0
	m.input.SetWidth(99999)
	inputRows := m.inputChromeHeight(contentWidth)
	approvalRows := m.approvalTrayHeight(contentWidth)
	activityRows := m.activityRowHeight(contentWidth)
	m.viewport.Width = max(1, contentWidth-6)
	m.viewport.Height = max(1, m.height-3-inputRows-approvalRows-activityRows)
	m.syncViewport()
}

func (m *Model) syncViewport() {
	rendered := m.content.String(m.viewport.Width)
	if m.showContextDiagnostics {
		if header := m.renderContextInfoLine(m.viewport.Width); header != "" {
			rendered = header + rendered
		}
	}
	rendered = theme.WithBg(rendered, lipgloss.Color(theme.BgElev))
	rendered = theme.PadLines(rendered, m.viewport.Width, lipgloss.Color(theme.BgElev))

	contentLines := strings.Count(rendered, "\n")
	pad := m.viewport.Height - contentLines
	if pad < 0 {
		pad = 0
	}
	m.contentTopPad = pad
	if pad > 0 {
		padLine := lipgloss.NewStyle().
			Background(lipgloss.Color(theme.BgElev)).
			Render(strings.Repeat(" ", m.viewport.Width))
		rendered = strings.Repeat(padLine+"\n", pad) + rendered
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

func (m *Model) handleMouse(msg tea.MouseMsg) {
	switch msg.Action {
	case tea.MouseActionPress:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollUp(m.viewport.MouseWheelDelta)
		case tea.MouseButtonWheelDown:
			m.scrollDown(m.viewport.MouseWheelDelta)
		case tea.MouseButtonLeft:
			m.mousePressX = msg.X
			m.mousePressY = msg.Y
		}
	case tea.MouseActionRelease:
		if msg.Button == tea.MouseButtonLeft && m.mousePressX == msg.X && m.mousePressY == msg.Y {
			m.handleLeftClick(msg.Y)
		}
		m.mousePressX = -1
		m.mousePressY = -1
	}
}

func (m *Model) handleLeftClick(termY int) {
	// The viewport content area starts below the status bar and input area.
	// We need the content-area row. The viewport itself is positioned at
	// some Y within the terminal — approximate by using termY directly
	// adjusted for scroll offset.
	// content line = termY + m.viewport.YOffset
	// (viewport renders from its YOffset in the scrollable content)
	contentLine := termY + m.viewport.YOffset - m.contentTopPad - m.viewportContentTopOffset()

	if contentLine < 0 || len(m.content.segmentHeights) == 0 {
		return
	}

	// Walk segmentHeights to find which segment index this line falls in
	cumulative := 0
	for i, h := range m.content.segmentHeights {
		if h == 0 {
			continue
		}
		if contentLine < cumulative+h {
			seg := &m.content.segments[i]
			switch seg.kind {
			case segmentToolCall:
				if seg.toolData != nil {
					seg.toolData.collapsed = !seg.toolData.collapsed
					seg.renderDirty = true
					m.syncViewport()
				}
			case segmentToolCallGroup:
				if seg.toolGroupData == nil {
					return
				}
				entryIndex := m.content.toolCallGroupEntryAtRow(seg.toolGroupData, contentLine-cumulative, m.viewport.Width)
				if entryIndex < 0 || entryIndex >= len(seg.toolGroupData.entries) {
					return
				}
				entry := seg.toolGroupData.entries[entryIndex]
				if entry == nil {
					return
				}
				entry.collapsed = !entry.collapsed
				seg.renderDirty = true
				m.syncViewport()
			case segmentDelegation:
				if m.handleDelegationClick(seg, contentLine-cumulative) {
					seg.renderDirty = true
					m.syncViewport()
				}
			case segmentThinkingBlock:
				if seg.thinkData != nil {
					seg.thinkData.collapsed = !seg.thinkData.collapsed
					seg.renderDirty = true
					m.syncViewport()
				}
			}
			return
		}
		cumulative += h
	}
}

func (m Model) viewportContentTopOffset() int {
	// ContentPane normally pads the viewport down by one row, and the scrollbar
	// layout replaces that with a leading blank row, so content starts one row
	// below the pane top in both cases.
	return 1
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
	rows := m.content.delegationRows(dd, m.viewport.Width)
	if rowInSegment >= len(rows) {
		return -1
	}
	row := rows[rowInSegment]
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

func (m *Model) renderScrollbar() string {
	totalContent := m.viewport.TotalLineCount()
	if totalContent <= m.viewport.Height {
		return ""
	}

	vh := m.viewport.Height
	if vh <= 0 {
		return ""
	}

	thumbH := max(1, vh*vh/totalContent)
	trackH := vh - thumbH

	scrollRange := totalContent - vh
	var thumbPos int
	if scrollRange > 0 && trackH > 0 {
		thumbPos = int(float64(m.viewport.YOffset) / float64(scrollRange) * float64(trackH))
	}
	if thumbPos > trackH {
		thumbPos = trackH
	}

	style := m.styles.Scrollbar
	trackStyle := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev))
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
	return sb.String()
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
