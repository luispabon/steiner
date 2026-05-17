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
	if msg.Action != tea.MouseActionPress {
		return
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollUp(m.viewport.MouseWheelDelta)
	case tea.MouseButtonWheelDown:
		m.scrollDown(m.viewport.MouseWheelDelta)
	case tea.MouseButtonLeft:
		m.handleLeftClick(msg.Y)
	}
}

func (m *Model) handleLeftClick(termY int) {
	// The viewport content area starts below the status bar and input area.
	// We need the content-area row. The viewport itself is positioned at
	// some Y within the terminal — approximate by using termY directly
	// adjusted for scroll offset.
	// content line = termY + m.viewport.YOffset
	// (viewport renders from its YOffset in the scrollable content)
	contentLine := termY + m.viewport.YOffset - m.contentTopPad

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
			// Click landed in segment i — toggle if collapsible
			seg := &m.content.segments[i]
			switch seg.kind {
			case segmentToolCall:
				if seg.toolData != nil {
					seg.toolData.collapsed = !seg.toolData.collapsed
					seg.renderDirty = true
					m.syncViewport()
				}
			case segmentDelegation:
				if seg.delegData != nil {
					seg.delegData.collapsed = !seg.delegData.collapsed
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
	hasSessionHealth := m.sessionHealthState != "" || m.sessionHealthCompactionCount > 0 || m.sessionHealthGuidance != "" || len(m.sessionHealthNotes) > 0
	hasContextInfo := m.ctxInfoPromptTokens > 0 || m.ctxInfoContextWindow > 0 || m.ctxInfoContextUsagePercent > 0 || m.ctxInfoCompactionThreshold > 0 || m.ctxInfoEstimatorPadTokens > 0 || strings.TrimSpace(m.ctxInfoStatus) != ""
	if !hasSessionHealth && !hasContextInfo {
		return ""
	}
	line1Parts := []string{"context info:"}
	if hasSessionHealth {
		line1Parts = append(line1Parts, fmt.Sprintf("session health #%d turn %d", m.sessionHealthCompactionCount, m.sessionHealthTurn))
		if m.sessionHealthState != "" {
			line1Parts = append(line1Parts, "state "+m.sessionHealthState)
		}
		if m.sessionHealthGuidance != "" {
			entry := m.sessionHealthGuidance
			if m.sessionHealthCompactionCount > 0 {
				suffix := "compaction"
				if m.sessionHealthCompactionCount != 1 {
					suffix = "compactions"
				}
				entry += fmt.Sprintf(" after %d %s", m.sessionHealthCompactionCount, suffix)
			}
			line1Parts = append(line1Parts, entry)
		}
		if len(m.sessionHealthNotes) > 0 {
			line1Parts = append(line1Parts, "notes "+strings.Join(m.sessionHealthNotes, ", "))
		}
	}
	line1 := strings.Join(line1Parts, "; ")
	contextWindow := m.ctxInfoContextWindow
	if contextWindow <= 0 {
		contextWindow = m.sidebar.contextBudget
	}
	status := strings.TrimSpace(m.ctxInfoStatus)
	if status == "" {
		status = "unknown_context"
	}
	line2 := fmt.Sprintf(
		"view full, prompt_tokens=%d context_window=%d context_usage_percent=%s compaction_threshold=%s estimator_pad_tokens=%d status=%s",
		m.ctxInfoPromptTokens,
		contextWindow,
		fmt.Sprintf("%.0f%%", m.ctxInfoContextUsagePercent),
		fmt.Sprintf("%.0f%%", m.ctxInfoCompactionThreshold),
		m.ctxInfoEstimatorPadTokens,
		status,
	)
	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgFaint)).
		Italic(true).
		Width(width)
	return style.Render(line1) + "\n" + style.Render(line2) + "\n"
}
