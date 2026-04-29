package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const fileViewerMaxLines = 40

// fileViewerState holds state for the read-only file viewer overlay.
// The overlay is opened in response to a DisplayFile event; file content is
// read from disk when the overlay opens, not from the event payload.
type fileViewerState struct {
	OverlayShell
	path         string
	lines        []string
	scrollOffset int
	loadErr      string
}

// openFileViewer opens a file viewer overlay for the given path.
// The file is read from disk at open time. Width and height are the current
// terminal dimensions.
func openFileViewer(path string, width, height int) fileViewerState {
	shell := OverlayShell{}
	shell = shell.WithDimensions(width, height).WithTitle("file viewer").openShell()

	var lines []string
	var loadErr string

	data, err := os.ReadFile(path)
	if err != nil {
		loadErr = fmt.Sprintf("cannot read file: %v", err)
	} else {
		raw := strings.Split(string(data), "\n")
		// Trim a trailing empty line that Split adds for files ending with \n.
		if len(raw) > 0 && raw[len(raw)-1] == "" {
			raw = raw[:len(raw)-1]
		}
		lines = raw
	}

	return fileViewerState{
		OverlayShell: shell,
		path:         path,
		lines:        lines,
		scrollOffset: 0,
		loadErr:      loadErr,
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
	headerLine := titleStyle.Render(s.path)
	divider := s.Divider()

	var body string
	if s.loadErr != "" {
		errStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.FgFaint)).
			Width(innerWidth)
		body = errStyle.Render(s.loadErr)
	} else {
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
