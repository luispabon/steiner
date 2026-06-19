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
	timestamp := b.renderStatusTimestamp(segment, width)
	topMargin := ""
	if timestamp != "" {
		topMargin = "\n"
	}

	if body == "" {
		return topMargin + b.appendRightAlignedTimestamp(bullet+"  "+tag, timestamp, width) + "\n"
	}

	sep := b.styles.FgMute.Render("·")

	// Compute the visible widths of the prefix parts so continuation lines
	// indent under the body, not under the "status" tag.
	prefixVisual := lipgloss.Width(bullet) + 2 + lipgloss.Width(tag) + 1 + lipgloss.Width(sep) + 1
	if width < prefixVisual+1 {
		// Terminal too narrow to wrap; just emit the first line.
		firstLine := bullet + "  " + tag + " " + sep + " " + bodyStyle.Render(body)
		return topMargin + b.appendRightAlignedTimestamp(firstLine, timestamp, width) + "\n"
	}
	bodyWidth := width - prefixVisual
	if timestamp != "" {
		timestampWidth := lipgloss.Width(timestamp)
		if bodyWidth > timestampWidth+1 {
			bodyWidth -= timestampWidth + 1
		}
	}

	wrapped := lipgloss.NewStyle().Width(bodyWidth).Render(body)
	wrappedLines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	firstBody := ""
	if len(wrappedLines) > 0 {
		firstBody = strings.TrimRight(wrappedLines[0], " ")
	}
	firstLine := bullet + "  " + tag + " " + sep + " " + bodyStyle.Render(firstBody)
	firstLine = b.appendRightAlignedTimestamp(firstLine, timestamp, width)
	if len(wrappedLines) <= 1 {
		return topMargin + firstLine + "\n"
	}

	indent := strings.Repeat(" ", prefixVisual)
	var sb strings.Builder
	sb.WriteString(topMargin)
	sb.WriteString(firstLine + "\n")
	for _, line := range wrappedLines[1:] {
		sb.WriteString(indent + bodyStyle.Render(strings.TrimRight(line, " ")) + "\n")
	}
	return sb.String()
}

func (b *contentBuffer) renderStatusTimestamp(segment contentSegment, width int) string {
	if width < 1 {
		return ""
	}
	label := formatContentTimestamp(segment.timestamp)
	if label == "" {
		return ""
	}
	return b.styles.FgDim.Render(label)
}

func (b *contentBuffer) appendRightAlignedTimestamp(line, timestamp string, width int) string {
	if timestamp == "" {
		return line
	}
	lineWidth := lipgloss.Width(line)
	timestampWidth := lipgloss.Width(timestamp)
	padding := width - lineWidth - timestampWidth
	if padding < 1 {
		padding = 1
	}
	return line + strings.Repeat(" ", padding) + timestamp
}
