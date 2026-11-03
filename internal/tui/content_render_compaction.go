package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/usagestats"
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

	return renderStyledBox(strings.Join(lines, "\n"), b.styles.Warn.GetForeground(), lipgloss.Color(b.styles.Palette.ContentBG), width) + "\n"
}

// compactionBoxRows builds the inner lines of the compaction box.
func (b *contentBuffer) compactionBoxRows(cd *compactionBannerData, width int) []string {
	// boxStyle counts the 2 border cells and the 1-cell padding each side in
	// its Width, so the drawable content area is width - 4. Matching that here
	// keeps the right-aligned meta flush against the border.
	innerWidth := width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	rows := []string{b.renderCompactionHeader(cd, innerWidth)}
	// The note is flattened and cut to innerWidth so it stays one row.
	if note := truncateOverlayText(cd.fallbackNote, innerWidth); note != "" {
		rows = append(rows, b.styles.FgMute.Render(note))
	}
	return rows
}

// renderCompactionHeader renders the single header line of the compaction box.
func (b *contentBuffer) renderCompactionHeader(cd *compactionBannerData, width int) string {
	tag := b.styles.Warn.Bold(true).Render("compaction")

	subtitle := ""
	if cd.subtitle != "" {
		subtitle = " " + b.styles.FgDim.Render(cd.subtitle)
	}

	left := tag + subtitle

	// Right-hand meta: cache + elapsed + #count, then the spinner/✓ glyph last.
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
		if rate, ok := usagestats.HitRate(cd.cacheReadTokens, cd.inputTokens, cd.cacheCreateTokens); ok {
			parts = append(parts, b.styles.FgDim.Render("cache "+formatCacheHitRate(rate, ok)))
		}
	}

	if cd.elapsed != "" {
		parts = append(parts, b.styles.FgDim.Render(cd.elapsed))
	} else if cd.startTime > 0 {
		parts = append(parts, b.styles.FgDim.Render(formatElapsed(cd.startTime, nanoNow())))
	}

	if cd.compactionCount > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("#%d", cd.compactionCount)))
	}

	if !cd.finished {
		frame := spinnerFrames[cd.spinnerFrame%len(spinnerFrames)]
		parts = append(parts, b.styles.FgMute.Render(frame))
	} else if !cd.skipped {
		parts = append(parts, b.styles.SuccessStyle.Render("✓"))
	}

	return strings.Join(parts, " ")
}
