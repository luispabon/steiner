package tui

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	contextOverlayMaxLines = 30
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

func (s contextOverlayState) reflow() contextOverlayState {
	rendered, _ := renderMarkdownBlock(markdownBlockParams{
		block:       s.content,
		width:       s.InnerWidth(),
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
	maxOffset := s.lineCount - contextOverlayMaxLines
	if maxOffset < 0 {
		maxOffset = 0
	}
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
	maxOffset := s.lineCount - contextOverlayMaxLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	s.scrollOffset += n
	if s.scrollOffset > maxOffset {
		s.scrollOffset = maxOffset
	}
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
	out = m.buildContextOverlay(&memo.styled)
	w, h = measureOverlay(out)
	memo.store(key, out, w, h)
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

// buildContextOverlay renders the overlay string. styled caches the per-line
// styling across scroll steps.
func (m *Model) buildContextOverlay(styled *contextStyledLines) string {
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

	// Slice rendered markdown lines for the scroll window.
	lines := s.renderedLines
	start := s.scrollOffset
	end := start + contextOverlayMaxLines
	if end > len(lines) {
		end = len(lines)
	}

	styled.reset(lines, innerWidth)
	renderedLines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		renderedLines = append(renderedLines, styled.line(i, lines[i]))
	}
	body := strings.Join(renderedLines, "\n")

	footerText := FooterChip("↑↓") + " scroll   " + FooterChip("esc") + " close"
	footer := s.RenderFooter(footerText)

	full := lipgloss.JoinVertical(lipgloss.Left,
		headerLine,
		divider,
		body,
		s.Divider(),
		footer,
	)
	boxStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.BorderSoft)).
		Padding(1, 2)

	return s.RenderWithBg(boxStyle, full, theme.BgElev)
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
