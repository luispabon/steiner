package tui

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	// contextOverlayChromeLines counts the header, the two dividers, the footer,
	// the rounded border and the vertical padding rows around the scrollable body.
	contextOverlayChromeLines = 8
)

// contextOverlayState holds the state for the /context report overlay modal.
type contextOverlayState struct {
	OverlayShell
	title             string
	content           string
	renderedLines     []string
	scrollOffset      int
	lineCount         int
	styles            *theme.Styles
	glamourStyleSheet glamour.TermRendererOption
	renderer          *glamour.TermRenderer
	renderWidth       int
	// agents lists the orchestrator (index 0) followed by the session's
	// sub-agents. An empty list marks a single-pane report such as cache
	// stats.
	agents       []contextOverlayAgent
	selected     int
	agentsScroll int
	// agentsRev increments whenever the rebuilt agent rows differ, so the
	// render memo can ignore the slice identity.
	agentsRev int
	// reports caches agent reports by agent ID; "" is the orchestrator.
	reports map[string]string
}

// openContextOverlay returns a copy of the state with the overlay open and
// content populated.
func openContextOverlay(title, content string, width, height int, styles *theme.Styles, styleSheet glamour.TermRendererOption) contextOverlayState {
	shell := OverlayShell{}.WithPreferredWidth(120)
	shell = shell.WithDimensions(width, height).WithTitle(strings.TrimSpace(title)).openShell()
	return contextOverlayState{
		OverlayShell:      shell,
		title:             strings.TrimSpace(title),
		content:           content,
		styles:            styles,
		glamourStyleSheet: styleSheet,
	}.reflow()
}

// visibleHeight returns the number of body rows that fit the terminal height.
// The narrow layout spends two extra rows on its agent cycler.
func (s contextOverlayState) visibleHeight() int {
	if s.narrowMode() {
		return s.bodyVisibleHeight(contextOverlayChromeLines + contextOverlayNarrowChromeLines)
	}
	return s.bodyVisibleHeight(contextOverlayChromeLines)
}

// maxScrollOffset returns the scroll offset of the last page.
func (s contextOverlayState) maxScrollOffset() int {
	return max(0, s.lineCount-s.visibleHeight())
}

// atEnd reports whether the view shows the last page of a report that
// overflows the body. A report that fits on one page is never at its end.
func (s contextOverlayState) atEnd() bool {
	maxOffset := s.maxScrollOffset()
	return maxOffset > 0 && s.scrollOffset >= maxOffset
}

func (s contextOverlayState) reflow() contextOverlayState {
	rendered, _ := renderMarkdownBlock(markdownBlockParams{
		block:       s.content,
		width:       s.reportWidth(),
		styles:      s.styles,
		styleSheet:  s.glamourStyleSheet,
		renderer:    &s.renderer,
		renderWidth: &s.renderWidth,
	})
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	s.renderedLines = lines
	s.lineCount = len(lines)
	maxOffset := s.maxScrollOffset()
	if s.scrollOffset > maxOffset {
		s.scrollOffset = maxOffset
	}
	if s.scrollOffset < 0 {
		s.scrollOffset = 0
	}
	return s
}

// closeContextOverlay returns a copy of the state with the overlay closed.
func (s contextOverlayState) closeContextOverlay() contextOverlayState {
	s.OverlayShell = s.closeShell()
	return s
}

// scrollUp moves the scroll position up by n lines.
func (s contextOverlayState) scrollUp(n int) contextOverlayState {
	s.scrollOffset -= n
	if s.scrollOffset < 0 {
		s.scrollOffset = 0
	}
	return s
}

// scrollDown moves the scroll position down by n lines.
func (s contextOverlayState) scrollDown(n int) contextOverlayState {
	maxOffset := s.maxScrollOffset()
	s.scrollOffset += n
	if s.scrollOffset > maxOffset {
		s.scrollOffset = maxOffset
	}
	return s
}

// scrollToEnd moves the scroll position to the last page of the report.
func (s contextOverlayState) scrollToEnd() contextOverlayState {
	s.scrollOffset = s.maxScrollOffset()
	return s
}

// renderContextOverlay returns the rendered overlay string, reusing the
// previous render while nothing it reads has changed.
func (m *Model) renderContextOverlay() string {
	out, _, _ := m.contextOverlayRendered()
	return out
}

// contextOverlayRendered returns the rendered overlay and its cell size.
func (m *Model) contextOverlayRendered() (out string, w, h int) {
	memo := &m.overlayMemos.context
	key := m.contextRenderKey()
	if e := memo.lookup(key); e != nil {
		return e.out, e.w, e.h
	}
	var hit contextOverlayHit
	out, hit = m.buildContextOverlay(&memo.styled)
	w, h = measureOverlay(out)
	memo.store(key, out, w, h, hit)
	return out, w, h
}

// measureOverlay returns the widest line width and the line count of a
// rendered overlay.
func measureOverlay(rendered string) (w, h int) {
	lines := strings.Split(rendered, "\n")
	for _, line := range lines {
		w = max(w, lipgloss.Width(line))
	}
	return w, len(lines)
}

// buildContextOverlay renders the overlay string and the sidebar hit map.
// styled caches the per-line styling across scroll steps.
func (m *Model) buildContextOverlay(styled *contextStyledLines) (string, contextOverlayHit) {
	s := m.contextOverlay
	s.OverlayShell = s.WithDimensions(m.width, m.height)

	innerWidth := s.InnerWidth()

	// Title line
	titleStyle := lipgloss.NewStyle().
		Foreground(m.styles.AccentColor).
		Bold(true).
		Width(innerWidth)
	title := strings.TrimSpace(s.title)
	if title == "" {
		title = "Report"
	}
	headerLine := titleStyle.Render(title)
	divider := s.Divider()

	rows := s.visibleHeight()
	styled.reset(s.renderedLines, s.reportWidth())

	hit := contextOverlayHit{
		narrow:       s.narrowMode(),
		bodyTop:      4,
		sidebarLeft:  2,
		sidebarRight: 2 + contextOverlaySidebarWidth,
	}
	var bodyLines []string
	switch {
	case s.twoPane():
		detail := s.detailLines(styled, rows)
		sidebar, targets := s.sidebarLines()
		hit.targets = targets
		paneDivider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render("│")
		bodyLines = make([]string, rows)
		for i := range rows {
			bodyLines[i] = sidebar[i] + paneDivider + " " + detail[i]
		}
	case s.reportMode():
		bodyLines = s.detailLines(styled, rows)
		// Narrow mode inserts the cycler line and its divider above the body.
		hit.bodyTop += contextOverlayNarrowChromeLines
	default:
		bodyLines = s.singlePaneLines(styled)
	}

	footerText := FooterChip("↑↓") + " scroll   " + FooterChip("esc") + " close"
	parts := []string{headerLine, divider}
	if s.reportMode() {
		footerText = FooterChip("↑↓") + " scroll   " + FooterChip("tab") + " agent   " + FooterChip("esc") + " close"
	}
	if s.narrowMode() {
		parts = append(parts, s.narrowCyclerLine(innerWidth), s.Divider())
	}
	parts = append(parts, strings.Join(bodyLines, "\n"), s.Divider(), s.RenderFooter(footerText))

	full := lipgloss.JoinVertical(lipgloss.Left, parts...)
	boxStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.BorderSoft)).
		Padding(1, 2)

	return s.RenderWithBg(boxStyle, full, theme.BgElev), hit
}

// detailLines returns exactly rows styled report lines for the scroll window,
// padding past the end of the report with blank rows.
func (s contextOverlayState) detailLines(styled *contextStyledLines, rows int) []string {
	width := s.reportWidth()
	out := make([]string, rows)
	for i := range out {
		index := s.scrollOffset + i
		if index >= 0 && index < len(s.renderedLines) {
			out[i] = styled.line(index, s.renderedLines[index])
			continue
		}
		out[i] = strings.Repeat(" ", width)
	}
	return out
}

// singlePaneLines returns the styled report lines in the scroll window without
// padding, so a single-pane report keeps its natural height.
func (s contextOverlayState) singlePaneLines(styled *contextStyledLines) []string {
	start := min(max(0, s.scrollOffset), len(s.renderedLines))
	end := min(start+s.visibleHeight(), len(s.renderedLines))
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, styled.line(i, s.renderedLines[i]))
	}
	return out
}

// contextOverlayHit maps rendered body rows to the agent index each row
// selects. Narrow mode has no sidebar, so it carries no targets.
type contextOverlayHit struct {
	narrow       bool
	bodyTop      int
	sidebarLeft  int
	sidebarRight int
	targets      []int
}

// contextOverlayHitResult is the outcome of a pointer lookup on the context
// overlay. agent is -1 when the cell selects no agent.
type contextOverlayHitResult struct {
	inside    bool
	inSidebar bool
	agent     int
}

// contextOverlayHitAt maps a terminal cell to the agent row drawn under it. It
// uses the unclipped overlay origin because composeCenteredOverlay positions
// the overlay unclipped while contextOverlayBounds clamps it.
func (m *Model) contextOverlayHitAt(x, y int) (contextOverlayHitResult, bool) {
	if !m.contextOverlay.IsOpen() {
		return contextOverlayHitResult{}, false
	}
	_, w, h := m.contextOverlayRendered()
	entry := m.overlayMemos.context.lookup(m.contextRenderKey())
	if entry == nil || w <= 0 || h <= 0 {
		return contextOverlayHitResult{}, false
	}
	localX := x - (m.width-w)/2
	localY := y - (m.height-h)/2
	if localX < 0 || localX >= w || localY < 0 || localY >= h {
		return contextOverlayHitResult{}, false
	}
	result := contextOverlayHitResult{inside: true, agent: -1}
	hit := entry.hit
	if hit.narrow {
		return result, true
	}
	row := localY - hit.bodyTop
	if row < 0 || row >= len(hit.targets) || localX < hit.sidebarLeft || localX >= hit.sidebarRight {
		return result, true
	}
	result.inSidebar = true
	if target := hit.targets[row]; target >= 0 {
		result.agent = target
	}
	return result, true
}

// contextOverlayBounds returns the centered context overlay rectangle clipped
// to the terminal dimensions.
func (m *Model) contextOverlayBounds() (x, y, w, h int) {
	_, w, h = m.contextOverlayRendered()
	startX := (m.width - w) / 2
	startY := (m.height - h) / 2
	endX := min(m.width, startX+w)
	endY := min(m.height, startY+h)
	x = max(0, startX)
	y = max(0, startY)
	w = endX - x
	h = endY - y
	if w <= 0 || h <= 0 {
		return 0, 0, 0, 0
	}
	return x, y, w, h
}

func (m *Model) contextOverlayCapturesMouse(x, y int) bool {
	if !m.contextOverlay.IsOpen() || m.fileList.IsOpen() || m.mcpOverlay.IsOpen() || m.lspOverlay.IsOpen() ||
		m.workflowHandoff.IsOpen() || m.orchestrationPicker.IsOpen() || m.anyConfirmModalOpen() {
		return false
	}
	boundsX, boundsY, boundsW, boundsH := m.contextOverlayBounds()
	return boundsW > 0 && boundsH > 0 &&
		x >= boundsX && x < boundsX+boundsW && y >= boundsY && y < boundsY+boundsH
}
