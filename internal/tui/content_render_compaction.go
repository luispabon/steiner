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

	var rows []string
	rows = append(rows, b.renderCompactionHeader(cd, innerWidth))

	if cd.collapsed {
		return rows
	}

	// Divider
	rows = append(rows, b.styles.FgDim.Render(strings.Repeat("─", innerWidth)))

	// Key/value detail rows — omit zero/empty fields.
	rows = append(rows, b.compactionDetailRows(cd, innerWidth)...)

	// Divider before footer.
	rows = append(rows, b.styles.FgDim.Render(strings.Repeat("─", innerWidth)))

	// Footer stats.
	rows = append(rows, b.renderCompactionFooter(cd))

	return rows
}

// renderCompactionHeader renders the single header line of the compaction box.
func (b *contentBuffer) renderCompactionHeader(cd *compactionBannerData, width int) string {
	disclosure := "▾"
	if cd.collapsed {
		disclosure = "▸"
	}

	tag := b.styles.Warn.Bold(true).Render("compaction")

	subtitle := ""
	if cd.subtitle != "" {
		subtitle = " " + b.styles.FgDim.Render(cd.subtitle)
	}

	left := disclosure + " " + tag + subtitle

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

// compactionKV holds a single key/value detail pair for the expanded compaction box.
type compactionKV struct {
	key string
	val string
}

// compactionDetailPairs builds the ordered list of key/value pairs from cd,
// omitting any entry whose data is zero or empty.
func compactionDetailPairs(cd *compactionBannerData) []compactionKV {
	var pairs []compactionKV
	if cd.compactedTurns > 0 || cd.compactedMessages > 0 {
		pairs = append(pairs, compactionKV{"Compacted", fmt.Sprintf("%d turns, %d messages", cd.compactedTurns, cd.compactedMessages)})
	}
	if cd.retainedTurns > 0 || cd.retainedMessages > 0 {
		pairs = append(pairs, compactionKV{"Retained", fmt.Sprintf("%d turns, %d messages", cd.retainedTurns, cd.retainedMessages)})
	}
	if cd.mode != "" {
		pairs = append(pairs, compactionKV{"Mode", cd.mode})
	}
	if cd.beforeTokens > 0 {
		val := formatCompactCount(cd.beforeTokens)
		if cd.beforePct > 0 {
			val += fmt.Sprintf(" (%.0f%%)", cd.beforePct)
		}
		pairs = append(pairs, compactionKV{"Before", val})
	}
	if cd.afterTokens > 0 {
		val := formatCompactCount(cd.afterTokens)
		if cd.afterPct > 0 {
			val += fmt.Sprintf(" (%.0f%%)", cd.afterPct)
		}
		pairs = append(pairs, compactionKV{"After", val})
	}
	if cd.summaryTitle != "" {
		pairs = append(pairs, compactionKV{"Summary", cd.summaryTitle})
	} else if cd.summary != "" {
		pairs = append(pairs, compactionKV{"Summary", cd.summary})
	}
	return pairs
}

// compactionDetailRows returns key/value rows for the expanded view.
// Rows with zero/empty data are omitted.
func (b *contentBuffer) compactionDetailRows(cd *compactionBannerData, width int) []string {
	pairs := compactionDetailPairs(cd)
	if len(pairs) == 0 {
		return nil
	}

	// Compute key column width.
	keyW := 0
	for _, p := range pairs {
		if len(p.key) > keyW {
			keyW = len(p.key)
		}
	}

	rows := make([]string, 0, len(pairs))
	valW := width - keyW - 2 // 2 for ": " separator
	if valW < 1 {
		valW = 1
	}
	for _, p := range pairs {
		key := b.styles.FgMute.Render(fmt.Sprintf("%-*s", keyW, p.key))
		val := b.styles.FgDim.Render(truncateRunes(p.val, valW))
		rows = append(rows, key+": "+val)
	}
	return rows
}

// renderCompactionFooter renders the footer stats line for the expanded view.
func (b *contentBuffer) renderCompactionFooter(cd *compactionBannerData) string {
	parts := make([]string, 0, 2)
	if cd.compactionCount > 0 {
		parts = append(parts, fmt.Sprintf("Compaction #%d", cd.compactionCount))
	}
	dur := cd.elapsed
	if dur == "" && cd.startTime > 0 {
		dur = formatElapsed(cd.startTime, nanoNow())
	}
	if dur != "" {
		parts = append(parts, "Duration: "+dur)
	}
	return b.styles.FgDim.Render(strings.Join(parts, " · "))
}
