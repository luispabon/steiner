package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// renderCenteredDashes returns a width-filling dashed line with the label centered.
func (b *contentBuffer) renderCenteredDashes(label string, width int) string {
	dash := "─"
	// Build the full dash line with label in the middle.
	// Format: "─── label ───"
	labelStr := " " + label + " "
	labelLen := lipgloss.Width(labelStr)
	if labelLen > width-2 {
		// Label too wide; truncate by display width (rune/grapheme safe).
		return b.styles.FgDim.Render(ansi.Truncate(labelStr, max(1, width), ""))
	}
	dashCount := (width - labelLen) / 2
	// Ensure at least 1 dash on each side
	if dashCount < 1 {
		dashCount = 1
	}
	left := strings.Repeat(dash, dashCount)
	right := strings.Repeat(dash, width-lipgloss.Width(left)-labelLen)
	return b.styles.FgDim.Render(left + labelStr + right)
}

// renderSeparatorSegment renders a centered dashed separator line.
// Phase separators get blank lines above and below; other separators follow
// standard spacing (closing separators add a leading newline).
func (b *contentBuffer) renderSeparatorSegment(segment contentSegment, width int) string {
	if segment.separatorData == nil {
		return ""
	}
	sd := segment.separatorData
	label := sd.label
	if sd.closing {
		label = "End of " + label
	}
	line := b.renderCenteredDashes(label, width)
	if sd.phase {
		return "\n" + line + "\n"
	}
	if sd.closing {
		return "\n" + line + "\n"
	}
	return line + "\n"
}
