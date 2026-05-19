package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// View renders the full TUI frame for the current model state.
func (m Model) View() string {
	contentWidth := max(1, m.width)
	sidebarVisible := m.sidebar.Visible(m.width)
	if sidebarVisible {
		contentWidth = max(1, m.width-sidebarWidth-1)
	}

	base := m.renderBaseView(contentWidth, sidebarVisible)
	return m.renderOverlayView(base, contentWidth)
}

func (m Model) renderBaseView(contentWidth int, sidebarVisible bool) string {
	mainColumn := m.renderMainColumn(contentWidth)
	if !sidebarVisible {
		return mainColumn
	}

	vDivider := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BorderSoft)).
		Width(1).
		Height(m.height).
		Render("")
	if m.sidebarPosition == "right" {
		return lipgloss.JoinHorizontal(lipgloss.Top, mainColumn, vDivider, m.sidebar.View(m.width, m.height))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, m.sidebar.View(m.width, m.height), vDivider, mainColumn)
}

func (m Model) renderMainColumn(contentWidth int) string {
	viewportView := m.renderViewportView(contentWidth)
	hDivider := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", contentWidth))

	mainComponents := []string{viewportView, hDivider}
	if tray := m.renderApprovalTray(contentWidth); tray != "" {
		mainComponents = append(mainComponents, tray)
	}
	mainComponents = append(mainComponents,
		m.renderActivityRow(contentWidth),
		m.renderInputView(contentWidth),
		m.status.view(contentWidth),
	)

	mainColumn := lipgloss.JoinVertical(lipgloss.Left, mainComponents...)
	return lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Width(contentWidth).
		Height(m.height).
		Render(mainColumn)
}

func (m Model) renderViewportView(contentWidth int) string {
	viewportInner := m.viewport.View()
	scrollbar := m.renderScrollbar()
	viewportContent := viewportInner
	paneStyle := m.styles.ContentPane

	if scrollbar != "" {
		viewportContent = m.renderViewportWithScrollbar(viewportInner, scrollbar)
		paneStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(theme.BgElev)).
			PaddingLeft(3).
			PaddingRight(2)
	}

	viewportView := paneStyle.Width(contentWidth).Render(viewportContent)
	if m.helpVisible {
		help := renderHelp(m.styles, max(20, contentWidth-4))
		return composeCenteredOverlay(viewportView, help, contentWidth, lipgloss.Height(viewportView))
	}
	return viewportView
}

func (m Model) renderViewportWithScrollbar(viewportInner, scrollbar string) string {
	vpLines := strings.Split(viewportInner, "\n")
	scLines := strings.Split(scrollbar, "\n")
	merged := make([]string, 0, len(vpLines)+1)
	merged = append(merged, "")
	for i := 0; i < len(vpLines) && i < len(scLines); i++ {
		merged = append(merged, vpLines[i]+scLines[i])
	}
	return strings.Join(merged, "\n")
}

func (m Model) renderOverlayView(base string, contentWidth int) string {
	switch {
	case m.palette.open:
		return composeCenteredOverlay(base, m.palette.View(), m.width, m.height)
	case m.fileList.open:
		return composeCenteredOverlay(base, m.fileList.View(), m.width, m.height)
	}

	base = m.renderBottomAnchoredOverlays(base, contentWidth)
	switch {
	case m.contextOverlay.open:
		return composeCenteredOverlay(base, m.renderContextOverlay(), m.width, m.height)
	case m.scratchpadOverlay.IsOpen():
		return composeCenteredOverlay(base, m.scratchpadOverlay.renderScratchpadOverlay(), m.width, m.height)
	case m.exitModal.open:
		return composeCenteredOverlay(base, m.renderExitModal(), m.width, m.height)
	default:
		return base
	}
}

func (m Model) renderBottomAnchoredOverlays(base string, contentWidth int) string {
	offset := m.inputChromeHeight(contentWidth) + m.activityRowHeight(contentWidth)
	if m.slashOverlay.open {
		base = m.slashOverlay.PlaceBottomAnchored(base, m.slashOverlay.View(), offset)
	}
	if m.filePicker.open {
		base = m.filePicker.PlaceBottomAnchored(base, m.filePicker.View(), offset)
	}
	if m.sessionPicker.open {
		base = m.sessionPicker.PlaceBottomAnchored(base, m.sessionPicker.View(), offset)
	}
	return base
}

func (m Model) renderActivityRow(contentWidth int) string {
	return m.activity.view(contentWidth, m.styles)
}

func (m *Model) applyInputStyles() {
	base := m.styles.UserBg
	cursorLine := m.styles.UserBg
	placeholder := m.styles.UserBg.Foreground(lipgloss.Color(theme.FgDim))
	text := m.styles.UserBg.Foreground(lipgloss.Color(theme.Fg))
	endOfBuffer := m.styles.UserBg.Foreground(lipgloss.Color(theme.UserSoft))

	m.input.FocusedStyle.Base = base
	m.input.FocusedStyle.CursorLine = cursorLine
	m.input.FocusedStyle.Placeholder = placeholder
	m.input.FocusedStyle.Prompt = base
	m.input.FocusedStyle.Text = text
	m.input.FocusedStyle.EndOfBuffer = endOfBuffer

	m.input.BlurredStyle.Base = base
	m.input.BlurredStyle.CursorLine = cursorLine
	m.input.BlurredStyle.Placeholder = placeholder
	m.input.BlurredStyle.Prompt = base
	m.input.BlurredStyle.Text = text
	m.input.BlurredStyle.EndOfBuffer = endOfBuffer

	m.input.Cursor.TextStyle = text
	m.input.Cursor.Style = text
	_ = m.input.Cursor.SetMode(cursor.CursorHide)
	if m.input.Focused() {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
}

func (m Model) renderInputView(contentWidth int) string {
	bar := m.styles.UserBar.Render("┃")
	bodyWidth := max(1, contentWidth-inputRailWidth)
	innerWidth := m.inputInnerWidth(contentWidth)
	lines, isPlaceholder := m.renderInputLines(innerWidth)

	var sb strings.Builder
	paddingLine := bar + m.styles.UserBg.Width(bodyWidth).Render("")
	for range inputPadY {
		sb.WriteString(paddingLine + "\n")
	}
	for _, line := range lines {
		if isPlaceholder {
			content := m.styles.UserBg.
				Foreground(lipgloss.Color(theme.FgDim)).
				Width(bodyWidth).
				Render(strings.Repeat(" ", inputPadX) + line + strings.Repeat(" ", inputPadX))
			sb.WriteString(bar + content + "\n")
			continue
		}
		lineStyle := lipgloss.NewStyle().Width(innerWidth)
		content := m.styles.UserBg.Width(bodyWidth).Render(strings.Repeat(" ", inputPadX) + lineStyle.Render(line) + strings.Repeat(" ", inputPadX))
		sb.WriteString(bar + content + "\n")
	}
	for range inputPadY {
		sb.WriteString(paddingLine + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m Model) inputChromeHeight(contentWidth int) int {
	return lipgloss.Height(m.renderInputView(contentWidth))
}

func (m Model) activityRowHeight(_ int) int {
	return 1
}

func (m Model) inputInnerWidth(contentWidth int) int {
	return max(1, contentWidth-inputRailWidth-(inputPadX*2)-inputTailFill)
}

func (m Model) renderInputLines(innerWidth int) ([]string, bool) {
	if m.input.Value() != "" {
		return m.renderTypedInputLines(innerWidth), false
	}
	return renderPlaceholderLines(m.input.Placeholder, innerWidth), true
}

func (m Model) renderTypedInputLines(width int) []string {
	if width < 1 {
		width = 1
	}

	valueLines := strings.Split(m.input.Value(), "\n")
	if len(valueLines) == 0 {
		valueLines = []string{""}
	}

	cursorLine := m.input.Line()
	if cursorLine < 0 {
		cursorLine = 0
	}
	if cursorLine >= len(valueLines) {
		cursorLine = len(valueLines) - 1
	}
	lineInfo := m.input.LineInfo()

	lines := make([]string, 0, len(valueLines))
	for i, valueLine := range valueLines {
		wrapped := wrapComposerLine(valueLine, width)
		if i == cursorLine {
			absPos := max(0, lineInfo.ColumnOffset)
			row := 0
			col := absPos
			for r, seg := range wrapped {
				segLen := len([]rune(seg))
				if col < segLen || (col == segLen && r == len(wrapped)-1) {
					row = r
					break
				}
				col -= segLen
			}
			wrapped[row] = insertComposerCursor(wrapped[row], col)
		}
		lines = append(lines, wrapped...)
	}
	return lines
}

func wrapComposerLine(line string, width int) []string {
	if width < 1 {
		width = 1
	}
	wrapped := ansi.Hardwrap(line, width, true)
	wrapped = strings.TrimRight(wrapped, "\n")
	if wrapped == "" {
		return []string{""}
	}
	return strings.Split(wrapped, "\n")
}

func insertComposerCursor(line string, col int) string {
	runes := []rune(line)
	col = max(0, min(col, len(runes)))
	if col == len(runes) {
		return string(append(runes, '█'))
	}
	out := make([]rune, 0, len(runes)+1)
	out = append(out, runes[:col]...)
	out = append(out, '█')
	out = append(out, runes[col:]...)
	return string(out)
}

func renderPlaceholderLines(placeholder string, width int) []string {
	if width < 1 {
		width = 1
	}
	wrapped := ansi.Hardwrap(ansi.Wordwrap(placeholder, width, ""), width, true)
	wrapped = strings.TrimRight(wrapped, "\n")
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	cursorWidth := max(0, width-1)
	firstLine := lines[0]
	if cursorWidth == 0 {
		firstLine = ""
	}
	lines[0] = "█" + lipgloss.NewStyle().Width(cursorWidth).Render(firstLine)
	return lines
}
