package tui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// View renders the full TUI frame for the current model state.
func (m *Model) View() tea.View {
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)

	base := m.renderBaseView(contentWidth, sidebarVisible)
	result := m.renderOverlayView(base, contentWidth)

	// Only populate screenLines during an active selection drag; extract lazily on release.
	if m.selection.active {
		m.screenLines = strings.Split(ansi.Strip(result), "\n")
	}
	if m.selection.hasSelection() {
		regionLeft, regionRight := m.selectionHighlightBounds()
		result = applyScreenHighlight(result, m.screenSelection(), m.styles.SelectionStyle, regionLeft, regionRight)
	}

	v := tea.View{
		Content:         result,
		AltScreen:       true,
		MouseMode:       tea.MouseModeCellMotion,
		BackgroundColor: lipgloss.Color(theme.BgElev),
	}
	// Attach v2 mouse handler via View.OnMouse callback without capturing the model.
	v.OnMouse = classifyMouse

	return v
}

func (m *Model) renderBaseView(contentWidth int, sidebarVisible bool) string {
	mainColumn := m.renderMainColumn(contentWidth)
	if !sidebarVisible {
		return mainColumn
	}

	var vDivider string
	if m.vDividerCacheStyles == m.styles && m.vDividerCacheHeight == m.height && m.vDividerCacheRendered != "" {
		vDivider = m.vDividerCacheRendered
	} else {
		vDivider = m.styles.VDivider.Height(m.height).Render("")
		m.vDividerCacheStyles = m.styles
		m.vDividerCacheHeight = m.height
		m.vDividerCacheRendered = vDivider
	}
	if m.sidebarPosition == "right" {
		return zipColumns(mainColumn, vDivider, m.renderSidebar(m.width, m.height))
	}
	return zipColumns(m.renderSidebar(m.width, m.height), vDivider, mainColumn)
}

func (m *Model) renderMainColumn(contentWidth int) string {
	viewportView := m.renderViewportView(contentWidth)
	var hDivider string
	if m.hDividerCacheWidth == contentWidth && m.hDividerCacheRendered != "" {
		hDivider = m.hDividerCacheRendered
	} else {
		hDivider = lipgloss.NewStyle().
			Background(lipgloss.Color(theme.BgElev)).
			Foreground(lipgloss.Color(theme.BorderSoft)).
			Render(strings.Repeat("─", contentWidth))
		m.hDividerCacheWidth = contentWidth
		m.hDividerCacheRendered = hDivider
	}

	mainComponents := []string{viewportView, hDivider}
	mainComponents = append(mainComponents,
		m.renderActivityRow(contentWidth),
		m.renderInputView(contentWidth),
		m.renderStatus(contentWidth),
	)

	mainColumn := strings.Join(mainComponents, "\n")
	return theme.TruncateAndPadVertical(mainColumn, contentWidth, m.height, theme.BgElev)
}

func (m *Model) renderViewportView(contentWidth int) string {
	scrollY := m.viewport.YOffset()
	scrollbar := m.renderScrollbar()
	hasScrollbar := scrollbar != ""

	if m.vpViewCache != "" &&
		!m.helpVisible &&
		m.vpViewCacheScrollY == scrollY &&
		m.vpViewCacheWidth == contentWidth &&
		m.vpViewCacheHasScrollbar == hasScrollbar {
		return m.vpViewCache
	}

	viewportInner := m.visibleViewportContent()
	viewportContent := viewportInner
	if hasScrollbar {
		viewportContent = m.renderViewportWithScrollbar(viewportInner, scrollbar)
	}

	viewportView := theme.ApplyPanePadding(viewportContent, contentWidth, hasScrollbar, theme.BgElev)
	if m.helpVisible {
		help := renderHelp(m.styles, max(20, contentWidth-4))
		return composeCenteredOverlay(viewportView, help, contentWidth, lipgloss.Height(viewportView))
	}

	m.vpViewCache = viewportView
	m.vpViewCacheScrollY = scrollY
	m.vpViewCacheWidth = contentWidth
	m.vpViewCacheHasScrollbar = hasScrollbar
	return viewportView
}

// visibleViewportContent slices the visible window out of the scroll model's
// single line slice without re-deriving the full content. setViewportContent
// is the only writer of that slice, and the scrollbar's line count derives
// from it, so the window and the scroll position cannot disagree.
func (m *Model) visibleViewportContent() string {
	start := m.viewport.YOffset()
	height := m.viewport.Height()
	if start < 0 {
		start = 0
	}
	lines := m.viewport.Lines()
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

func (m *Model) renderViewportWithScrollbar(viewportInner, scrollbar string) string {
	var b strings.Builder
	b.Grow(len(viewportInner) + len(scrollbar) + 64)

	// Emit a background-filled leading line so transparent terminals don't show
	// a gap. The viewport content is m.viewport.Width() wide; the scrollbar
	// occupies one additional column.
	leadBg := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).
		Render(strings.Repeat(" ", m.viewport.Width()))
	leadSc := m.styles.ScrollbarTrack.Render(" ")
	b.WriteString(leadBg + leadSc + "\n")

	// Walk both strings line by line simultaneously, without intermediate slice
	// allocations. strings.Split always yields at least one element, so the
	// "done" condition is tracked via a boolean rather than byte-index exhaustion.
	vpIdx, scIdx := 0, 0
	vpLen, scLen := len(viewportInner), len(scrollbar)
	vpDone, scDone := false, false
	first := true
	for !vpDone && !scDone {
		// Consume one viewport line (strings.Split semantics: last segment after
		// final '\n' is an empty string, not absent).
		var vpLine string
		vpLineEnd := strings.IndexByte(viewportInner[vpIdx:], '\n')
		if vpLineEnd < 0 {
			vpLine = viewportInner[vpIdx:]
			vpIdx = vpLen
			vpDone = true
		} else {
			vpLine = viewportInner[vpIdx : vpIdx+vpLineEnd]
			vpIdx += vpLineEnd + 1
		}

		// Consume one scrollbar line.
		var scLine string
		scLineEnd := strings.IndexByte(scrollbar[scIdx:], '\n')
		if scLineEnd < 0 {
			scLine = scrollbar[scIdx:]
			scIdx = scLen
			scDone = true
		} else {
			scLine = scrollbar[scIdx : scIdx+scLineEnd]
			scIdx += scLineEnd + 1
		}

		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(vpLine)
		b.WriteString(scLine)
	}

	return b.String()
}

func (m *Model) renderOverlayView(base string, contentWidth int) string {
	if m.fileList.IsOpen() {
		return composeCenteredOverlay(base, m.fileList.View(), m.width, m.height)
	}
	if m.mcpOverlay.IsOpen() {
		return composeCenteredOverlay(base, m.mcpOverlay.View(), m.width, m.height)
	}

	base = m.renderBottomAnchoredOverlays(base, contentWidth)
	switch {
	case m.workflowHandoff.IsOpen():
		return composeCenteredOverlay(base, m.renderWorkflowHandoffModal(), m.width, m.height)
	case m.contextOverlay.IsOpen():
		return composeCenteredOverlay(base, m.renderContextOverlay(), m.width, m.height)
	case m.worktreeCleanupModal.IsOpen():
		return composeCenteredOverlay(base, m.renderWorktreeCleanupModal(), m.width, m.height)
	case m.exitModal.IsOpen():
		return composeCenteredOverlay(base, m.renderExitModal(), m.width, m.height)
	default:
		return base
	}
}

func (m *Model) hasOpenBottomOverlay() bool {
	return m.slashOverlay.IsOpen() || m.filePicker.IsOpen() ||
		m.sessionPicker.IsOpen() || m.oneshotResumePicker.IsOpen() ||
		(m.modelPicker.IsOpen() && !m.modelPicker.IsWorkflowHandoff()) ||
		m.reasoningPicker.IsOpen() ||
		m.planPicker.IsOpen() || m.accentPicker.IsOpen()
}

func (m *Model) renderBottomAnchoredOverlays(base string, contentWidth int) string {
	if !m.hasOpenBottomOverlay() {
		return base
	}

	offset := m.overlayAnchorOffset(contentWidth)

	// When the sidebar occupies the left side, push the slash overlay right so
	// it appears above the prompt box rather than over the sidebar.
	xOffset := 0
	if m.width-contentWidth > 1 && m.sidebarPosition != "right" {
		xOffset = m.width - contentWidth
	}

	if m.slashOverlay.IsOpen() {
		base = m.slashOverlay.PlaceBottomAnchoredAt(base, m.slashOverlay.View(), offset, xOffset)
	}
	if m.filePicker.IsOpen() {
		base = m.filePicker.PlaceBottomAnchoredAt(base, m.filePicker.View(), offset, xOffset)
	}
	if m.sessionPicker.IsOpen() {
		base = m.sessionPicker.PlaceBottomAnchored(base, m.sessionPicker.View(), offset)
	}
	if m.oneshotResumePicker.IsOpen() {
		base = m.oneshotResumePicker.PlaceBottomAnchored(base, m.oneshotResumePicker.View(), offset)
	}
	if m.modelPicker.IsOpen() && !m.modelPicker.IsWorkflowHandoff() {
		base = m.modelPicker.PlaceBottomAnchoredAt(base, m.modelPicker.View(), offset, xOffset)
	}
	if m.reasoningPicker.IsOpen() {
		base = m.reasoningPicker.PlaceBottomAnchoredAt(base, m.reasoningPicker.View(), offset, xOffset)
	}
	if m.planPicker.IsOpen() {
		base = m.planPicker.PlaceBottomAnchoredAt(base, m.planPicker.View(), offset, xOffset)
	}
	if m.accentPicker.IsOpen() {
		base = m.accentPicker.PlaceBottomAnchoredAt(base, m.accentPicker.View(), offset, xOffset)
	}
	return base
}

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
	bar := m.styles.UserBar.Render("┃")
	bodyWidth := max(1, contentWidth-inputRailWidth)
	innerWidth := m.inputInnerWidth(contentWidth)
	lines, isPlaceholder, cursorRow, cursorCol := m.renderInputLines(innerWidth)
	if isPlaceholder {
		return m.renderPlaceholderInputView(bar, bodyWidth, innerWidth, lines)
	}
	return m.renderNormalInputView(contentWidth, bar, bodyWidth, innerWidth, lines, cursorRow, cursorCol)
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
			if cmdPrefix, ok := matchCommandPrefix(m.input.Value(), m.skillNames, m.oneshotRunning); ok {
				if strings.HasPrefix(line, cmdPrefix) {
					if cursorCol >= len(cmdPrefix) {
						prefix := m.styles.CommandPrefixStyle.Render(cmdPrefix)

						restText := line[len(cmdPrefix):]
						restVisibleWidth := ansi.StringWidth(restText)
						expectedRestWidth := innerWidth - len([]rune(cmdPrefix))
						if restVisibleWidth < expectedRestWidth {
							restText += strings.Repeat(" ", expectedRestWidth-restVisibleWidth)
						}
						rest := m.styles.UserBg.Render(restText)

						renderedLine = prefix + rest
					}
				}
			}
		}

		if renderedLine == line {
			renderedLine = renderInputLine(line, innerWidth, m.styles.ImageMarkerStyle)
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
	return m.inputChromeHeight(contentWidth) + 1 // status bar
}

func (m *Model) activityRowHeight(_ int) int {
	return 1
}

func (m *Model) maxVisibleInputLines(contentWidth int) int {
	return max(1, m.height-4-m.activityRowHeight(contentWidth)-2*inputPadY)
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
		wrapped := wrapComposerLine(valueLine, width)
		if i == cursorLine {
			// m.input.Column() is the rune offset of the cursor within the
			// logical line, independent of the textarea's internal wrap
			// width. The old code used LineInfo().ColumnOffset, which only
			// equals this offset when the textarea width is large enough to
			// disable soft wrapping.
			absPos := max(0, m.input.Column())
			row := 0
			col := absPos
			for r, seg := range wrapped {
				visibleLen := ansi.StringWidth(seg)
				if col < visibleLen || (col == visibleLen && r == len(wrapped)-1) {
					row = r
					break
				}
				col -= visibleLen
			}
			cursorDisplayRow = len(lines) + row
			cursorDisplayCol = col
		}
		lines = append(lines, wrapped...)
	}
	return lines, cursorDisplayRow, cursorDisplayCol
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

// stripTrailingReset removes the trailing ANSI reset sequence added by lipgloss Style.Render.
func stripTrailingReset(s string) string {
	s = strings.TrimSuffix(s, "\x1b[0m")
	return strings.TrimSuffix(s, "\x1b[m")
}

// applyComposerCursorAnsi highlights the character at the given visible
// column position in an ANSI-styled string using reverse video (SGR 7/27),
// so the cursor overlays the existing glyph instead of displacing it. SGR
// 7/27 compose with whatever styling is already active and scope off
// cleanly without emitting a reset, unlike a lipgloss-rendered span (see
// stripTrailingReset for why that distinction matters in this file).
// When on is false the string is returned with no highlight applied at all
// (the character renders in its normal, already-applied style).
func applyComposerCursorAnsi(s string, pos int, on bool) string {
	if !on {
		return s
	}
	if pos < 0 {
		pos = 0
	}
	var result strings.Builder
	currentCol := 0
	inEsc := false
	var escSeq strings.Builder
	placed := false
	for _, r := range s {
		if inEsc {
			escSeq.WriteRune(r)
			if (r >= '@' && r <= '~') || r == '\\' {
				inEsc = false
				result.WriteString(escSeq.String())
				escSeq.Reset()
			}
			continue
		}
		if r == '' {
			inEsc = true
			escSeq.WriteRune(r)
			continue
		}
		if currentCol == pos {
			placed = true
			result.WriteString("[7m")
			result.WriteRune(r)
			result.WriteString("[27m")
		} else {
			result.WriteRune(r)
		}
		currentCol++
	}
	if !placed {
		result.WriteString("[7m [27m")
	}
	return result.String()
}

func styleImageMarkers(line string, markerStyle lipgloss.Style) string {
	return imageMarkerPattern.ReplaceAllStringFunc(line, func(match string) string {
		return markerStyle.Render(match)
	})
}

func renderInputLine(line string, width int, markerStyle lipgloss.Style) string {
	if styled := styleImageMarkers(line, markerStyle); styled != line {
		return theme.WithBg(lipgloss.NewStyle().Width(width).Render(styled), theme.UserSoft)
	}
	return lipgloss.NewStyle().Width(width).Render(line)
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
