package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (m *Model) applyInputStyles() {
	base := m.styles.UserBg
	placeholder := m.styles.UserBg.Foreground(lipgloss.Color(theme.FgDim))
	text := m.styles.UserBg.Foreground(lipgloss.Color(theme.Fg))
	endOfBuffer := m.styles.UserBg.Foreground(lipgloss.Color(theme.UserSoft))

	style := textarea.StyleState{
		Base:        base,
		CursorLine:  base,
		Placeholder: placeholder,
		Prompt:      base,
		Text:        text,
		EndOfBuffer: endOfBuffer,
	}
	m.input.SetStyles(textarea.Styles{
		Focused: style,
		Blurred: style,
		Cursor:  textarea.CursorStyle{Blink: false},
	})
	if m.input.Focused() {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
}

// renderInputView memoizes the composer render, keyed on every model field
// the input render path reads, plus the render dimensions and blink state.
// The blink state changes on every 500ms tick, invalidating the cache.
// Any write to any of those inputs, from anywhere, changes the key and forces
// a fresh render: the composer is re-rendered on every frame, and a missed
// input would serve a stale composer indefinitely.
func (m *Model) renderInputView(contentWidth int) string {
	cursorLine := m.input.Line()
	cursorCol := m.input.Column()

	// Reset blink phase to visible when the cursor moves
	if cursorLine != m.lastCursorLine || cursorCol != m.lastCursorCol {
		m.composerBlinkOn = true
		m.lastCursorLine = cursorLine
		m.lastCursorCol = cursorCol
	}

	key := inputViewCacheKey{
		contentWidth:   contentWidth,
		height:         m.height,
		value:          m.input.Value(),
		cursorLine:     cursorLine,
		cursorColumn:   cursorCol,
		placeholder:    m.input.Placeholder,
		oneshotRunning: m.oneshotRunning,
		styles:         m.styles,
		cursorBlinkOn:  m.composerBlinkOn,
	}
	if m.inputViewCacheSet && m.inputViewCacheKey == key && slices.Equal(m.inputViewCacheSkills, m.skillNames) {
		return m.inputViewCacheRendered
	}
	rendered := m.renderInputViewUncached(contentWidth)
	m.inputViewCacheSet = true
	m.inputViewCacheKey = key
	m.inputViewCacheSkills = append([]string(nil), m.skillNames...)
	m.inputViewCacheRendered = rendered
	return rendered
}

func (m *Model) renderInputViewUncached(contentWidth int) string {
	userBg := theme.ColorHex(m.styles.UserBg.GetBackground())
	bar := m.styles.UserBar.Background(m.styles.UserBg.GetBackground()).Render("┃")
	bodyWidth := max(1, contentWidth-inputRailWidth)
	innerWidth := m.inputInnerWidth(contentWidth)
	lines, isPlaceholder, cursorRow, cursorCol := m.renderInputLines(innerWidth)
	var rendered string
	if isPlaceholder {
		rendered = m.renderPlaceholderInputView(bar, bodyWidth, innerWidth, lines)
	} else {
		rendered = m.renderNormalInputView(contentWidth, bar, bodyWidth, innerWidth, lines, cursorRow, cursorCol)
	}
	return theme.WithBg(rendered, userBg)
}

func (m *Model) renderPlaceholderInputView(bar string, bodyWidth int, innerWidth int, lines []string) string {
	var sb strings.Builder
	paddingLine := bar + m.styles.UserBg.Width(bodyWidth).Render("")
	for range inputPadY {
		sb.WriteString(paddingLine + "\n")
	}
	for i, line := range lines {
		renderedLine := m.styles.UserBg.
			Foreground(lipgloss.Color(theme.FgDim)).
			Width(innerWidth).
			Render(line)
		if i == 0 {
			renderedLine = applyComposerCursorAnsi(renderedLine, 0, m.composerBlinkOn)
		}
		content := m.styles.UserBg.Width(bodyWidth).Render(strings.Repeat(" ", inputPadX) + renderedLine + strings.Repeat(" ", inputPadX))
		sb.WriteString(bar + content + "\n")
	}
	for range inputPadY {
		sb.WriteString(paddingLine + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m *Model) renderNormalInputView(contentWidth int, bar string, bodyWidth, innerWidth int, lines []string, cursorRow, cursorCol int) string {
	maxVisible := m.maxVisibleInputLines(contentWidth)
	start := 0
	if len(lines) > maxVisible {
		start = max(0, cursorRow-maxVisible/2)
		if start+maxVisible > len(lines) {
			start = len(lines) - maxVisible
		}
		lines = lines[start : start+maxVisible]
	}

	var sb strings.Builder
	paddingLine := bar + m.styles.UserBg.Width(bodyWidth).Render("")
	for range inputPadY {
		sb.WriteString(paddingLine + "\n")
	}
	for i, line := range lines {
		renderedLine := line
		if i+start == cursorRow {
			renderedLine = highlightCommandPrefixLine(line, cursorCol, m.input.Value(), m.skillNames, m.oneshotRunning, innerWidth, m.styles.CommandPrefixStyle, m.styles.UserBg)
		}

		if renderedLine == line {
			renderedLine = renderInputLine(line, innerWidth, m.styles.ImageMarkerStyle, m.pendingMarkerLabels())
		}
		renderedLine = m.styles.UserBg.Width(innerWidth).Render(renderedLine)
		if i+start == cursorRow {
			renderedLine = applyComposerCursorAnsi(renderedLine, cursorCol, m.composerBlinkOn)
		}
		content := m.styles.UserBg.Width(bodyWidth).Render(strings.Repeat(" ", inputPadX) + renderedLine + strings.Repeat(" ", inputPadX))
		sb.WriteString(bar + content + "\n")
	}
	for range inputPadY {
		sb.WriteString(paddingLine + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// highlightCommandPrefixLine renders the cursor line with its matched
// command prefix (e.g. a slash command or skill invocation) styled
// separately from the rest of the line. It returns line unchanged if no
// command prefix matches or the cursor sits within the prefix itself.
func highlightCommandPrefixLine(line string, cursorCol int, value string, skillNames []string, oneshotRunning bool, innerWidth int, prefixStyle, restStyle lipgloss.Style) string {
	cmdPrefix, ok := matchCommandPrefix(value, skillNames, oneshotRunning)
	prefixWidth := ansi.StringWidth(cmdPrefix)
	if !ok || !strings.HasPrefix(line, cmdPrefix) || cursorCol < prefixWidth {
		return line
	}

	prefix := prefixStyle.Render(cmdPrefix)

	restText := line[len(cmdPrefix):]
	restVisibleWidth := ansi.StringWidth(restText)
	expectedRestWidth := innerWidth - prefixWidth
	if restVisibleWidth < expectedRestWidth {
		restText += strings.Repeat(" ", expectedRestWidth-restVisibleWidth)
	}
	rest := restStyle.Render(restText)

	return prefix + rest
}

func (m *Model) inputChromeHeight(contentWidth int) int {
	innerWidth := m.inputInnerWidth(contentWidth)
	lines, isPlaceholder, _, _ := m.renderInputLines(innerWidth)
	visibleLines := len(lines)
	if !isPlaceholder {
		visibleLines = min(visibleLines, m.maxVisibleInputLines(contentWidth))
	}
	return visibleLines + (2 * inputPadY)
}

// overlayAnchorOffset is the row count a bottom-anchored overlay (slash,
// file, model, session, etc. pickers) sits above: just the input box and
// status bar. The hDivider and activity row above them are not reserved for
// the overlay — it's fine for the overlay to sit over them, since they carry
// no content worth preserving while a picker is open.
func (m *Model) overlayAnchorOffset(contentWidth int) int {
	return m.inputChromeHeight(contentWidth) + 1 + m.queuedSteerHeight(contentWidth) // status bar + queued box
}

func (m *Model) activityRowHeight(_ int) int {
	return 1
}

func (m *Model) maxVisibleInputLines(contentWidth int) int {
	return max(1, m.height-4-m.activityRowHeight(contentWidth)-2*inputPadY-m.queuedSteerHeight(contentWidth))
}

func (m *Model) inputInnerWidth(contentWidth int) int {
	return max(1, contentWidth-inputRailWidth-(inputPadX*2)-inputTailFill)
}

func (m *Model) renderInputLines(innerWidth int) ([]string, bool, int, int) {
	if m.input.Value() != "" {
		lines, cursorRow, cursorCol := m.renderTypedInputLines(innerWidth)
		return lines, false, cursorRow, cursorCol
	}
	return renderPlaceholderLines(m.input.Placeholder, innerWidth), true, 0, 0
}

func (m *Model) renderTypedInputLines(width int) ([]string, int, int) {
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

	cursorDisplayRow := 0
	cursorDisplayCol := 0
	lines := make([]string, 0, len(valueLines))
	for i, valueLine := range valueLines {
		rows := wrapComposerLine(valueLine, width)
		if i == cursorLine {
			// m.input.Column() is the rune offset of the cursor within the
			// logical line, independent of the textarea's internal wrap
			// width. Mapping it through the rows wrapComposerLine draws keeps
			// the drawn caret on the row the textarea navigates.
			runes := []rune(valueLine)
			absPos := min(max(0, m.input.Column()), len(runes))
			row := 0
			for r, seg := range rows {
				if composerGridStart(seg.runes, len(runes)) > absPos {
					break
				}
				row = r
			}
			cursorDisplayRow = len(lines) + row
			cursorDisplayCol = composerGridCol(rows[row].runes, absPos)
		}
		for _, seg := range rows {
			lines = append(lines, seg.text)
		}
	}
	return lines, cursorDisplayRow, cursorDisplayCol
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
	return lines
}
