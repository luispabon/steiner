package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// renderImagesAttachedSegment renders the images-attached segment with structured rows:
//
//	Images attached:
//	  [img-1] · 800x600 · 245KB · .steiner/tmp/images/20260630_143052_a7f3.png
//	  [img-2] · 1920x1080 · 1.2MB · .steiner/tmp/images/20260630_143107_9c1e.png
//
// The header is bold, each [img-N] identifier is bold, and the metadata/path is dimmed.
// Continuation lines are indented under the metadata (post-ID).
func (b *contentBuffer) renderImagesAttachedSegment(segment contentSegment, width int) string {
	if segment.imagesAttachedData == nil || len(segment.imagesAttachedData.rows) == 0 {
		return ""
	}

	data := segment.imagesAttachedData
	boldStyle := lipgloss.NewStyle().Bold(true)
	header := boldStyle.Render("Images attached:")
	var sb strings.Builder
	sb.WriteString("  " + header + "\n")

	for _, row := range data.rows {
		// Build the ID in bold, then dimensions/size/path dimmed
		boldID := boldStyle.Render("[" + row.id + "]")
		metadata := fmt.Sprintf("%dx%d", row.width, row.height)
		sep := b.styles.FgDim.Render(" · ")
		dimmedMetadata := b.styles.FgDim.Render(metadata + " · " + row.size + " · " + row.path)

		// Compute the visible width of the prefix: "    " + boldID + " · "
		prefixVisual := 4 + lipgloss.Width(boldID) + lipgloss.Width(sep)
		firstLine := "    " + boldID + sep + dimmedMetadata

		// If the entire line fits, render it as-is
		firstLineWidth := lipgloss.Width(firstLine)
		if firstLineWidth <= width {
			sb.WriteString(firstLine + "\n")
			continue
		}

		// Line is too long; wrap with indented continuation
		bodyWidth := width - prefixVisual
		if bodyWidth <= 0 {
			// Terminal too narrow; just render the first line
			sb.WriteString(firstLine + "\n")
			continue
		}

		// Wrap the metadata/path part
		wrapped := lipgloss.NewStyle().Width(bodyWidth).Render(dimmedMetadata)
		wrappedLines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")

		// First line: "    " + ID + " · " + first wrapped line
		if len(wrappedLines) > 0 {
			sb.WriteString("    " + boldID + sep + strings.TrimRight(wrappedLines[0], " ") + "\n")
		}

		// Continuation lines: indented under the metadata
		indent := strings.Repeat(" ", prefixVisual)
		for _, line := range wrappedLines[1:] {
			sb.WriteString(indent + strings.TrimRight(line, " ") + "\n")
		}
	}

	return sb.String()
}
