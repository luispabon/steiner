package theme

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// bgEscape returns an ANSI escape sequence that sets the background to the
// given color. Unlike lipgloss.Style.Render, this does not depend on the
// terminal's color profile — it always produces a 24-bit truecolor sequence.
func bgEscape(bg lipgloss.Color) string {
	hex := strings.TrimPrefix(string(bg), "#")
	if len(hex) != 6 {
		return ""
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 64)
	g, _ := strconv.ParseInt(hex[2:4], 16, 64)
	b, _ := strconv.ParseInt(hex[4:6], 16, 64)
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// WithBg ensures every line in s has its background set to bg.
// It re-applies the background after every ANSI reset sequence (\x1b[0m)
// and at the start of each logical line. Idempotent for the same bg.
func WithBg(s string, bg lipgloss.Color) string {
	bgSeq := bgEscape(bg)
	reset := "\x1b[0m"
	resetBg := reset + bgSeq
	bgReset := "\x1b[49m" + bgSeq

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = strings.ReplaceAll(line, reset, resetBg)
		line = strings.ReplaceAll(line, "\x1b[49m", bgReset)
		if line == "" {
			lines[i] = bgSeq + " " + reset
		} else {
			lines[i] = bgSeq + line
		}
	}
	return strings.Join(lines, "\n")
}

// PadLines pads every line in s to the given width by appending spaces
// rendered with the specified background color. Lines already at or
// beyond the target width are left unchanged.
func PadLines(s string, width int, bg lipgloss.Color) string {
	if width < 1 {
		return s
	}
	bgStyle := lipgloss.NewStyle().Background(bg)
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
