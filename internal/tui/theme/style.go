package theme

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	ansiResetLong  = "\x1b[0m"
	ansiResetShort = "\x1b[m"
	ansiBgReset    = "\x1b[49m"
)

// ColorHex converts a color.Color to a "#RRGGBB" hex string suitable for
// use with HighlightMatch, WithBg, and PadLines.
// Returns an empty string for nil or zero-alpha colors.
func ColorHex(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	// RGBA returns 16-bit values; shift down to 8-bit.
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// bgEscape returns an ANSI escape sequence that sets the background to the
// given color. Unlike lipgloss.Style.Render, this does not depend on the
// terminal's color profile — it always produces a 24-bit truecolor sequence.
func bgEscape(bg string) string {
	hex := strings.TrimPrefix(bg, "#")
	if len(hex) != 6 {
		return ""
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 64)
	g, _ := strconv.ParseInt(hex[2:4], 16, 64)
	b, _ := strconv.ParseInt(hex[4:6], 16, 64)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// WithBg ensures every line in s has its background set to bg.
// It re-applies the background after ANSI reset sequences and at the start of
// each logical line. Idempotent for the same bg.
func WithBg(s string, bg string) string {
	bgSeq := bgEscape(bg)
	longResetBg := ansiResetLong + bgSeq
	shortResetBg := ansiResetShort + bgSeq
	bgReset := ansiBgReset + bgSeq

	trailingNewlines := len(s) - len(strings.TrimRight(s, "\n"))
	if trailingNewlines > 0 {
		s = strings.TrimRight(s, "\n")
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = strings.ReplaceAll(line, ansiResetLong, longResetBg)
		line = strings.ReplaceAll(line, ansiResetShort, shortResetBg)
		line = strings.ReplaceAll(line, ansiBgReset, bgReset)
		if line == "" {
			lines[i] = bgSeq + " " + ansiResetLong
		} else {
			lines[i] = bgSeq + line
		}
	}
	result := strings.Join(lines, "\n")
	if trailingNewlines > 0 {
		result += strings.Repeat("\n", trailingNewlines)
	}
	return result
}

// PadLines pads every line in s to the given width by appending spaces
// rendered with the specified background color. Lines already at or
// beyond the target width are left unchanged.
func PadLines(s string, width int, bg string) string {
	if width < 1 {
		return s
	}
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(bg))
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		w := lipgloss.Width(line)
		if w < width {
			pad := bgStyle.Render(strings.Repeat(" ", width-w))
			lines[i] = line + pad
		}
	}
	return strings.Join(lines, "\n")
}

// HighlightMatch wraps text in explicit ANSI emphasis so matched glyphs stay
// visible even when rendering without an attached TTY profile.
func HighlightMatch(text string, fg string) string {
	if text == "" {
		return ""
	}
	style := lipgloss.NewStyle().Bold(true).Underline(true)
	if fg != "" {
		style = style.Foreground(lipgloss.Color(fg))
	}
	return style.Render(text)
}
