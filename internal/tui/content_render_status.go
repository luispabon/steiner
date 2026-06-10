package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// statusLinePrefix is the literal prefix used throughout the codebase to mark
// system status lines. appendLine strips it before storing the segment so the
// renderer can re-emit it in styled form.
const statusLinePrefix = "status: "

// renderStatusSegment renders a system status line as:
//
//	○  status · <body>
//
// The bullet and separator are muted, the "status" tag is bold on the global
// accent colour, and the body uses the default text foreground so it stays
// readable without competing with the accent. A trailing "·" on the body is
// stripped to avoid a double-dot when the body itself ends with a period. An
// empty body renders as "○  status" with no separator. Multi-line bodies
// indent continuation lines under the body, not under the tag.
func (b *contentBuffer) renderStatusSegment(segment contentSegment, width int) string {
	body := strings.TrimRight(strings.TrimSpace(segment.text), "·")
	body = strings.TrimRight(body, " ")

	bullet := b.styles.FgMute.Render("○")
	tag := b.styles.StatusTag.Render("status")
	bodyStyle := b.baseTextStyle()

	if body == "" {
		return bullet + "  " + tag + "\n"
	}

	sep := b.styles.FgMute.Render("·")
	firstLine := bullet + "  " + tag + " " + sep + " " + bodyStyle.Render(body)

	// Compute the visible widths of the prefix parts so continuation lines
	// indent under the body, not under the "status" tag.
	prefixVisual := lipgloss.Width(bullet) + 2 + lipgloss.Width(tag) + 1 + lipgloss.Width(sep) + 1
	if width < prefixVisual+1 {
		// Terminal too narrow to wrap; just emit the first line.
		return firstLine + "\n"
	}
	bodyWidth := width - prefixVisual

	wrapped := lipgloss.NewStyle().Width(bodyWidth).Render(body)
	wrappedLines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	if len(wrappedLines) <= 1 {
		return firstLine + "\n"
	}

	indent := strings.Repeat(" ", prefixVisual)
	var sb strings.Builder
	sb.WriteString(firstLine + "\n")
	for _, line := range wrappedLines[1:] {
		sb.WriteString(indent + bodyStyle.Render(strings.TrimRight(line, " ")) + "\n")
	}
	return sb.String()
}
