package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// scratchpadOverlayState holds the state for the scratchpad overlay modal.
type scratchpadOverlayState struct {
	OverlayShell
	intent        string
	decisions     string
	openItems     string
	next          string
	renderedLines []string
	scrollOffset  int
	lineCount     int
	styles        theme.Styles
}

// openScratchpadOverlay returns a copy of the state with the overlay open and
// fields populated from the current sidebar scratchpad values.
func (s scratchpadOverlayState) openScratchpadOverlay(width, height int, intent, decisions, openItems, next string, styles theme.Styles) scratchpadOverlayState {
	shell := OverlayShell{}.WithPreferredWidth(80)
	shell = shell.WithDimensions(width, height).openShell()
	return scratchpadOverlayState{
		OverlayShell: shell,
		intent:       intent,
		decisions:    decisions,
		openItems:    openItems,
		next:         next,
		styles:       styles,
	}.reflow()
}

// reflow splits the scratchpad content into lines and resets scroll bounds.
func (s scratchpadOverlayState) reflow() scratchpadOverlayState {
	innerWidth := s.InnerWidth()
	var bodyLines []string
	anyField := s.intent != "" || s.decisions != "" || s.next != "" || s.openItems != ""
	if !anyField {
		bodyLines = append(bodyLines, "no active task")
	} else {
		if s.intent != "" {
			bodyLines = append(bodyLines, s.renderField("Intent", s.intent, innerWidth)...)
		}
		if s.decisions != "" {
			bodyLines = append(bodyLines, s.renderField("Decided", s.decisions, innerWidth)...)
		}
		if s.openItems != "" {
			bodyLines = append(bodyLines, s.renderField("Open", s.openItems, innerWidth)...)
		}
		if s.next != "" {
			bodyLines = append(bodyLines, s.renderField("Next", s.next, innerWidth)...)
		}
	}
	s.renderedLines = bodyLines
	s.lineCount = len(bodyLines)
	maxOffset := s.lineCount - scratchpadMaxLines
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

// scrollUp moves the scroll position up by n lines.
func (s scratchpadOverlayState) scrollUp(n int) scratchpadOverlayState {
	s.scrollOffset -= n
	if s.scrollOffset < 0 {
		s.scrollOffset = 0
	}
	return s
}

// scrollDown moves the scroll position down by n lines.
func (s scratchpadOverlayState) scrollDown(n int) scratchpadOverlayState {
	maxOffset := s.lineCount - scratchpadMaxLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	s.scrollOffset += n
	if s.scrollOffset > maxOffset {
		s.scrollOffset = maxOffset
	}
	return s
}

// closeScratchpadOverlay returns a copy of the state with the overlay closed.
func (s scratchpadOverlayState) closeScratchpadOverlay() scratchpadOverlayState {
	s.OverlayShell = s.closeShell()
	return s
}

// scratchpadMaxLines is the maximum number of lines visible in the scratchpad
// overlay before scrolling is needed.
const scratchpadMaxLines = 20

// renderScratchpadOverlay builds the rendered scratchpad overlay string.
func (s scratchpadOverlayState) renderScratchpadOverlay() string {
	innerWidth := s.InnerWidth()

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Fg)).
		Bold(true).
		Width(innerWidth)
	headerLine := titleStyle.Render("scratchpad")
	divider := s.Divider()

	// Slice rendered lines for the scroll window.
	start := s.scrollOffset
	end := start + scratchpadMaxLines
	if end > s.lineCount {
		end = s.lineCount
	}
	visible := s.renderedLines[start:end]

	lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Width(innerWidth)
	var renderedLines []string
	for _, line := range visible {
		renderedLines = append(renderedLines, lineStyle.Render(line))
	}
	body := strings.Join(renderedLines, "\n")

	// Footer: show scroll hints if content exceeds the visible window.
	hasScroll := s.lineCount > scratchpadMaxLines
	var footerParts []string
	if hasScroll {
		footerParts = append(footerParts, FooterChip("↑↓")+" scroll")
	}
	footerParts = append(footerParts, FooterChip("esc")+" close")
	footerText := strings.Join(footerParts, "   ")
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

	return s.RenderWithBg(boxStyle, full, lipgloss.Color(theme.BgElev))
}

// renderField renders a single scratchpad field with label and wrapped value.
func (s scratchpadOverlayState) renderField(label, value string, innerWidth int) []string {
	if value == "" {
		return nil
	}
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgDim)).
		Width(innerWidth)
	labelLine := labelStyle.Render(label + ":")
	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Fg)).
		Width(innerWidth).
		PaddingLeft(2)
	valueLine := valueStyle.Render(fmt.Sprintf("• %s", value))
	return []string{"", labelLine, valueLine}
}
