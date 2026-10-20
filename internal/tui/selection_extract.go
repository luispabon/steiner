package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// extractViewportText extracts plain text for a viewport selection from the
// full viewport content lines. Selection lines are unpadded content lines and
// columns are content-relative, so the extracted text is independent of the
// current scroll position. Viewport content lines can carry box-drawing
// chrome (tool and delegation frames), so each part gets the same
// stripBoxChrome post-processing as extractText.
func (m *Model) extractViewportText() string {
	if !m.selection.hasSelection() {
		return ""
	}
	lines := m.viewport.Lines()
	start, end := m.selection.canonical()
	var parts []string
	for i := start.line; i <= end.line; i++ {
		idx := i + m.contentTopPad
		if idx < 0 || idx >= len(lines) {
			continue
		}
		raw := ansi.Strip(lines[idx])
		sc := 0
		ec := runewidth.StringWidth(raw)
		if i == start.line {
			sc = start.col
		}
		if i == end.line {
			ec = end.col
		}
		extracted := truncateByWidth(raw, sc, ec)
		extracted = strings.TrimRight(extracted, " ")
		extracted = stripBoxChrome(extracted)
		extracted = strings.TrimRight(extracted, " ")
		parts = append(parts, extracted)
	}
	return strings.Join(parts, "\n")
}

// extractText extracts plain text from lines for the given selection state.
// Lines may be pre-stripped or contain ANSI sequences (stripped internally).
// Column values are visual column positions (terminal cells), not byte offsets.
// regionLeft and regionRight constrain intermediate lines to the active region's
// content bounds when regionRight > 0. Pass 0, 0 to disable constraining.
func extractText(lines []string, state selectionState, regionLeft, regionRight int) string {
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
		ec := runewidth.StringWidth(raw)
		if i == start.line {
			sc = start.col
		}
		if i == end.line {
			ec = end.col
		}
		if regionRight > 0 {
			sc = max(regionLeft, sc)
			ec = min(regionRight, ec)
		}
		extracted := truncateByWidth(raw, sc, ec)
		extracted = strings.TrimRight(extracted, " ")
		extracted = stripBoxChrome(extracted)
		extracted = strings.TrimRight(extracted, " ")
		parts = append(parts, extracted)
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

// isBoxDrawing reports whether r is a box-drawing character from the set
// │┌┐└┘─├┤╭╮╯╰.
func isBoxDrawing(r rune) bool {
	switch r {
	case '│', '┌', '┐', '└', '┘', '─', '├', '┤', '╭', '╮', '╯', '╰':
		return true
	}
	return false
}

// stripBoxChrome removes leading and trailing box-drawing characters and
// adjacent spaces from a string. Only one run of adjacent spaces touching
// each border is consumed as "padding"; interior intentional indentation
// is preserved. Empty string in produces empty string out; a string of only
// border chars/spaces produces empty string out.
func stripBoxChrome(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}

	// Strip leading box-drawing characters.
	left := 0
	for left < len(runes) && isBoxDrawing(runes[left]) {
		left++
	}
	// Strip at most one space after leading box-drawing.
	if left < len(runes) && runes[left] == ' ' {
		left++
	}

	// Strip trailing box-drawing characters.
	right := len(runes) - 1
	for right >= left && isBoxDrawing(runes[right]) {
		right--
	}
	// Strip at most one space before trailing box-drawing.
	if right >= left && runes[right] == ' ' {
		right--
	}

	if left > right {
		return ""
	}

	result := runes[left : right+1]
	// If the result contains only box-drawing characters and spaces, return empty.
	for _, r := range result {
		if !isBoxDrawing(r) && r != ' ' {
			return string(result)
		}
	}
	return ""
}
