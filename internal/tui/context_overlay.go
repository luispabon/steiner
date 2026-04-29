package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const contextOverlayMaxLines = 30

// openContextOverlay returns a copy of the state with the overlay open and
// content populated.
func openContextOverlay(content string, width, height int) contextOverlayState {
	shell := OverlayShell{}
	shell = shell.WithDimensions(width, height).WithTitle("context report").openShell()
	lines := strings.Split(content, "\n")
	return contextOverlayState{
		OverlayShell: shell,
		content:      content,
		scrollOffset: 0,
		lineCount:    len(lines),
	}
}

// closeContextOverlay returns a copy of the state with the overlay closed.
func (s contextOverlayState) closeContextOverlay() contextOverlayState {
	s.OverlayShell = s.OverlayShell.closeShell()
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

// renderContextOverlay builds the rendered overlay string.
func (m *Model) renderContextOverlay() string {
	s := m.contextOverlay
	s.OverlayShell = s.OverlayShell.WithDimensions(m.width, m.height)

	innerWidth := s.InnerWidth()

	// Title line
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Fg)).
		Bold(true).
		Width(innerWidth)
	headerLine := titleStyle.Render("Context Report")
	divider := s.Divider()

	// Slice lines for scroll window
	lines := strings.Split(s.content, "\n")
	start := s.scrollOffset
	end := start + contextOverlayMaxLines
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[start:end]

	lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Width(innerWidth)
	var renderedLines []string
	for _, line := range visible {
		renderedLines = append(renderedLines, lineStyle.Render(line))
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
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.BorderSoft)).
		Padding(1, 2)

	return boxStyle.Width(innerWidth).Render(full)
}
