package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const fileViewerMaxLines = 40

// fileViewerState holds state for the read-only file viewer overlay.
// The overlay is opened in response to a DisplayFile event and renders the
// preview payload directly.
type fileViewerState struct {
	OverlayShell
	path         string
	language     string
	lines        []string
	scrollOffset int
	truncated    bool
}

// openFileViewer opens a file viewer overlay for the given preview payload.
func openFileViewer(payload output.DisplayFilePayload, width, height int) fileViewerState {
	shell := OverlayShell{}
	shell = shell.WithDimensions(width, height).WithTitle("file viewer").openShell()

	var lines []string
	for _, line := range payload.Preview.Lines {
		lines = append(lines, previewLineText(line))
	}

	return fileViewerState{
		OverlayShell: shell,
		path:         payload.Path,
		language:     payload.Preview.Language,
		lines:        lines,
		scrollOffset: 0,
		truncated:    payload.Preview.Truncated,
	}
}

// closeFileViewer returns a copy of the state with the overlay closed.
func (s fileViewerState) closeFileViewer() fileViewerState {
	s.OverlayShell = s.OverlayShell.closeShell()
	return s
}

// scrollUp moves the scroll position up by n lines.
func (s fileViewerState) scrollUp(n int) fileViewerState {
	s.scrollOffset -= n
	if s.scrollOffset < 0 {
		s.scrollOffset = 0
	}
	return s
}

// scrollDown moves the scroll position down by n lines.
func (s fileViewerState) scrollDown(n int) fileViewerState {
	maxOffset := len(s.lines) - fileViewerMaxLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	s.scrollOffset += n
	if s.scrollOffset > maxOffset {
		s.scrollOffset = maxOffset
	}
	return s
}

// renderFileViewer builds the rendered file viewer overlay string.
func (m *Model) renderFileViewer() string {
	s := m.fileViewer
	s.OverlayShell = s.OverlayShell.WithDimensions(m.width, m.height)

	innerWidth := s.InnerWidth()

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Fg)).
		Bold(true).
		Width(innerWidth)
	header := s.path
	if s.language != "" && s.language != "plain" {
		header += " · " + s.language
	}
	headerLine := titleStyle.Render(header)
	divider := s.Divider()

	var body string
	start := s.scrollOffset
	end := start + fileViewerMaxLines
	if end > len(s.lines) {
		end = len(s.lines)
	}
	var visible []string
	if len(s.lines) > 0 {
		visible = s.lines[start:end]
	}

	lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Width(innerWidth)
	rendered := make([]string, 0, len(visible))
	for _, line := range visible {
		rendered = append(rendered, lineStyle.Render(line))
	}
	body = strings.Join(rendered, "\n")
	if s.truncated {
		if body != "" {
			body += "\n"
		}
		body += lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgFaint)).Render("preview truncated")
	}

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

func previewLineText(line output.PreviewLine) string {
	var b strings.Builder
	for _, span := range line.Spans {
		b.WriteString(span.Text)
	}
	return b.String()
}
