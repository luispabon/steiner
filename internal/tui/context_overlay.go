package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	contextOverlayMaxLines = 30
	contextOverlayWidth    = 120
)

// contextOverlayState holds the state for the /context report overlay modal.
type contextOverlayState struct {
	OverlayShell
	content           string
	renderedLines     []string
	scrollOffset      int
	lineCount         int
	fixedWidth        int
	styles            theme.Styles
	glamourStyleSheet glamour.TermRendererOption
	renderer          *glamour.TermRenderer
	renderWidth       int
}

// contextInnerWidth returns the inner content width, using the fixed width
// when set, otherwise falling back to the overlay shell's computed width.
func (s contextOverlayState) contextInnerWidth() int {
	if s.fixedWidth > 0 {
		// fixedWidth is the total box width; subtract border (2) and padding (2) = 4.
		return s.fixedWidth - 4
	}
	return s.InnerWidth()
}

// openContextOverlay returns a copy of the state with the overlay open and
// content populated.
func openContextOverlay(content string, width, height int, styles theme.Styles, styleSheet glamour.TermRendererOption) contextOverlayState {
	shell := OverlayShell{}
	shell = shell.WithDimensions(width, height).WithTitle("context report").openShell()
	return contextOverlayState{
		OverlayShell:      shell,
		content:           content,
		fixedWidth:        contextOverlayWidth,
		styles:            styles,
		glamourStyleSheet: styleSheet,
	}.reflow()
}

func (s contextOverlayState) reflow() contextOverlayState {
	rendered, _ := renderMarkdownBlock(s.content, s.contextInnerWidth(), s.styles, s.glamourStyleSheet, &s.renderer, &s.renderWidth)
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

// contextDivider returns a horizontal rule sized to the context overlay's inner width.
func (s contextOverlayState) contextDivider() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", s.contextInnerWidth()))
}

// contextRenderFooter renders a footer bar sized to the context overlay's inner width.
func (s contextOverlayState) contextRenderFooter(footerText string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Width(s.contextInnerWidth()).Render(footerText)
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

	innerWidth := s.contextInnerWidth()

	// Title line
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Fg)).
		Bold(true).
		Width(innerWidth)
	headerLine := titleStyle.Render("Context Report")
	divider := s.contextDivider()

	// Slice rendered markdown lines for the scroll window.
	lines := s.renderedLines
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
	footer := s.contextRenderFooter(footerText)

	full := lipgloss.JoinVertical(lipgloss.Left,
		headerLine,
		divider,
		body,
		s.contextDivider(),
		footer,
	)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.BorderSoft)).
		Padding(1, 2)

	return boxStyle.Width(contextOverlayWidth).Render(full)
}
