package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
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
// Column values are visual column positions (terminal cells), not byte offsets.
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
		sc := 0
		ec := len([]rune(raw))
		if i == start.line {
			sc = start.col
		}
		if i == end.line {
			ec = end.col
		}
		parts = append(parts, truncateByWidth(raw, sc, ec))
	}
	return strings.Join(parts, "\n")
}

// truncateByWidth extracts the substring between visual columns start and end.
func truncateByWidth(s string, start, end int) string {
	if start >= end {
		return ""
	}
	col := 0
	byteStart := len(s)
	byteEnd := len(s)
	for i, r := range s {
		if col >= end {
			byteEnd = i
			break
		}
		if col >= start && byteStart == len(s) {
			byteStart = i
		}
		col += runewidth.RuneWidth(r)
	}
	if byteStart > byteEnd {
		byteStart = byteEnd
	}
	return s[byteStart:byteEnd]
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

// copyToClipboard returns a tea.Cmd that writes text to the system clipboard.
// Tries wl-copy (Wayland), xclip, xsel, then falls back to OSC52.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		if clipboardExec(text) {
			return nil
		}
		_, _ = fmt.Fprint(os.Stdout, ansi.SetSystemClipboard(text))
		return nil
	}
}

func clipboardExec(text string) bool {
	type candidate struct {
		name string
		args []string
		env  string
	}
	candidates := []candidate{
		{"wl-copy", nil, "WAYLAND_DISPLAY"},
		{"xclip", []string{"-selection", "clipboard"}, "DISPLAY"},
		{"xsel", []string{"--clipboard", "--input"}, "DISPLAY"},
	}
	for _, c := range candidates {
		if c.env != "" && os.Getenv(c.env) == "" {
			continue
		}
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, c.args...)
		cmd.Stdin = strings.NewReader(text)
		if cmd.Run() == nil {
			return true
		}
	}
	return false
}
