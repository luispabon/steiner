package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/luispabon/steiner/internal/output"
)

func truncateRunes(text string, width int) string {
	if width < 1 {
		return ""
	}
	return ansi.Truncate(text, width, "…")
}

// singleLineReplacer turns newlines and tabs into spaces. It is built once
// because strings.NewReplacer allocates on every call.
var singleLineReplacer = strings.NewReplacer("\n", " ", "\t", " ")

// singleLineDisplayText sanitizes model-supplied text for a display cell that
// must stay on one line. Newlines and tabs become spaces so they cannot start a
// new row or shift the row-to-target mapping. Stored text is not changed.
func singleLineDisplayText(text string) string {
	sanitized := output.SanitizeDisplayText(text)
	if !strings.ContainsAny(sanitized, "\n\t") {
		return sanitized
	}
	return singleLineReplacer.Replace(sanitized)
}

// normalizedDisplayText collapses whitespace, strips terminal controls, then collapses again so
// removed escapes never leave double spaces and control separators never join words.
func normalizedDisplayText(text string) string {
	return normalizeDelegationText(output.SanitizeDisplayText(normalizeDelegationText(text)))
}

func truncateOverlayText(text string, width int) string {
	text = normalizedDisplayText(text)
	if width <= 0 || text == "" {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(text)
	for len(runes) > 0 {
		candidate := string(runes) + "…"
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
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

func fitTextMiddle(text string, width int) string {
	text = strings.TrimSpace(text)
	textWidth := lipgloss.Width(text)
	if width > 0 && textWidth <= width {
		return text
	}
	if width <= 0 {
		return text
	}
	if width <= 1 {
		return "…"
	}

	runes := []rune(text)
	ellipsisWidth := lipgloss.Width("…")
	availableWidth := max(0, width-ellipsisWidth)
	leftBudget := availableWidth / 2
	rightBudget := availableWidth - leftBudget

	// Consume a contiguous prefix and suffix, each stopping at the first
	// rune that would overflow its budget (never skipping a rune that
	// doesn't fit and continuing past it, which would drop characters
	// from the middle of the kept segments instead of just the ellipsis
	// region).
	leftEnd := 0
	leftWidth := 0
	for leftEnd < len(runes) {
		runeWidth := lipgloss.Width(string(runes[leftEnd]))
		if leftWidth+runeWidth > leftBudget {
			break
		}
		leftWidth += runeWidth
		leftEnd++
	}

	rightStart := len(runes)
	rightWidth := 0
	for rightStart > leftEnd {
		runeWidth := lipgloss.Width(string(runes[rightStart-1]))
		if rightWidth+runeWidth > rightBudget {
			break
		}
		rightWidth += runeWidth
		rightStart--
	}

	return string(runes[:leftEnd]) + "…" + string(runes[rightStart:])
}

func fitText(text string, width int) string {
	text = strings.TrimSpace(text)
	if width > 0 && len(text) <= width {
		return text
	}
	runes := []rune(text)
	if width <= 0 || len(runes) <= width {
		return text
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func padOverlayLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	lineWidth := lipgloss.Width(line)
	if lineWidth >= width {
		return line
	}
	return line + strings.Repeat(" ", width-lineWidth)
}

// padCells is padOverlayLine for single-line strings, measured with ansi
// directly to skip lipgloss.Width's line splitting.
func padCells(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if w := ansi.StringWidth(line); w < width {
		return line + strings.Repeat(" ", width-w)
	}
	return line
}
