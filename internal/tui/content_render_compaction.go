package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (b *contentBuffer) renderCompactionBannerSegment(segment contentSegment, width int) string {
	if segment.compactionData == nil {
		return ""
	}
	return b.renderCompactionBanner(segment.compactionData, width)
}

func (b *contentBuffer) renderCompactionBanner(cd *compactionBannerData, width int) string {
	if width < 12 {
		width = 12
	}

	lines := b.compactionBoxRows(cd, width)

	return renderStyledBox(strings.Join(lines, "\n"), b.styles.Warn.GetForeground(), lipgloss.Color(theme.BgElev), width) + "\n"
}

// compactionBoxRows builds the inner lines of the compaction box.
func (b *contentBuffer) compactionBoxRows(cd *compactionBannerData, width int) []string {
	// Account for box border (2) and padding (2 each side).
	innerWidth := width - 6
	if innerWidth < 1 {
		innerWidth = 1
	}
	return []string{b.renderCompactionHeader(cd, innerWidth)}
}

// renderCompactionHeader renders the single header line of the compaction box.
func (b *contentBuffer) renderCompactionHeader(cd *compactionBannerData, width int) string {
	tag := b.styles.Warn.Bold(true).Render("compaction")

	subtitle := ""
	if cd.subtitle != "" {
		subtitle = " " + b.styles.FgDim.Render(cd.subtitle)
	}

	left := tag + subtitle

	// Right-hand meta: spinner/✓ + elapsed + #count.
	meta := b.renderCompactionHeaderMeta(cd)

	leftW := lipgloss.Width(left)
	metaW := lipgloss.Width(meta)
	padding := width - leftW - metaW
	if padding < 1 {
		padding = 1
	}

	return left + strings.Repeat(" ", padding) + meta
}

// renderCompactionHeaderMeta returns the right-aligned meta string for the header.
func (b *contentBuffer) renderCompactionHeaderMeta(cd *compactionBannerData) string {
	var parts []string

	if cd.finished {
		parts = append(parts, b.styles.SuccessStyle.Render("✓"))
	} else {
		frame := spinnerFrames[cd.spinnerFrame%len(spinnerFrames)]
		parts = append(parts, b.styles.FgMute.Render(frame))
	}

	if cd.elapsed != "" {
		parts = append(parts, b.styles.FgDim.Render(cd.elapsed))
	} else if cd.startTime > 0 {
		parts = append(parts, b.styles.FgDim.Render(formatElapsed(cd.startTime, nanoNow())))
	}

	if cd.compactionCount > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("#%d", cd.compactionCount)))
	}

	return strings.Join(parts, " ")
}
