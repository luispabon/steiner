package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type selectionPoint struct {
	line, col int
}

type selectionState struct {
	start, end selectionPoint
	active     bool
}

// hasSelection reports whether a non-trivial selection exists.
func (s selectionState) hasSelection() bool {
	return s.start != s.end || s.active
}

// canonical returns (start, end) normalised so start <= end by line, then col.
func (s selectionState) canonical() (selectionPoint, selectionPoint) {
	if s.start.line < s.end.line || (s.start.line == s.end.line && s.start.col <= s.end.col) {
		return s.start, s.end
	}
	return s.end, s.start
}

// clear returns the zero selectionState.
func (s selectionState) clear() selectionState {
	return selectionState{}
}

// termToContent maps terminal coordinates to content-space (viewport-line, col).
// termY, termX are 0-indexed terminal cell coordinates.
// yOffset is viewport.YOffset (scroll offset).
// sidebarVisible and sidebarPosition describe sidebar layout.
func termToContent(termX, termY, yOffset int, sidebarVisible bool, sidebarPosition string) selectionPoint {
	contentLine := (termY - 1) + yOffset // PaddingTop(1) of ContentPane
	leftPad := 3                         // PaddingLeft(3) of ContentPane
	if sidebarVisible && sidebarPosition != "right" {
		leftPad += sidebarWidth + 1
	}
	contentCol := termX - leftPad
	if contentCol < 0 {
		contentCol = 0
	}
	return selectionPoint{line: contentLine, col: contentCol}
}

// extractText extracts plain text from lines for the given selection state.
// lines are the raw viewport lines (may contain ANSI sequences).
func extractText(lines []string, state selectionState) string {
	if !state.hasSelection() {
		return ""
	}
	start, end := state.canonical()
	var parts []string
	for i := start.line; i <= end.line; i++ {
		if i < 0 || i >= len(lines) {
			continue
		}
		raw := ansi.Strip(lines[i])
		startCol, endCol := 0, len(raw)
		if i == start.line {
			if start.col < len(raw) {
				startCol = start.col
			} else {
				startCol = len(raw)
			}
		}
		if i == end.line {
			if end.col < len(raw) {
				endCol = end.col
			} else {
				endCol = len(raw)
			}
		}
		if startCol > endCol {
			startCol = endCol
		}
		parts = append(parts, raw[startCol:endCol])
	}
	return strings.Join(parts, "\n")
}

// applyHighlight post-processes a viewport.View() output string, applying
// selStyle highlight to the selected segment of each visible line.
// yOffset is viewport.YOffset; viewportWidth is viewport.Width.
func applyHighlight(viewportOutput string, yOffset int, state selectionState, selStyle lipgloss.Style, viewportWidth int) string {
	if !state.hasSelection() {
		return viewportOutput
	}
	start, end := state.canonical()
	lines := strings.Split(viewportOutput, "\n")
	for i, line := range lines {
		contentLine := yOffset + i
		if contentLine < start.line || contentLine > end.line {
			continue
		}
		startCol, endCol := 0, viewportWidth
		if contentLine == start.line {
			startCol = start.col
		}
		if contentLine == end.line {
			endCol = end.col
		}
		before := ansi.Cut(line, 0, startCol)
		mid := selStyle.Render(ansi.Strip(ansi.Cut(line, startCol, endCol)))
		after := ansi.Cut(line, endCol, viewportWidth)
		lines[i] = before + mid + after
	}
	return strings.Join(lines, "\n")
}

// copyToClipboard returns a tea.Cmd that writes text to the system clipboard
// via OSC52 (works over SSH/tmux).
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		fmt.Fprint(os.Stdout, ansi.SetSystemClipboard(text))
		return nil
	}
}
