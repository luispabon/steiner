package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
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

// compactionBoxRows builds the inner lines of the compaction box: one header
// row, then, when expanded, one blank row followed by the detail rows.
func (b *contentBuffer) compactionBoxRows(cd *compactionBannerData, width int) []string {
	// boxStyle counts the 2 border cells and the 1-cell padding each side in
	// its Width, so the drawable content area is width - 4. Matching that here
	// keeps the right-aligned meta flush against the border.
	innerWidth := width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	rows := []string{b.renderCompactionHeader(cd, innerWidth)}
	if !cd.expanded {
		return rows
	}
	details := b.compactionDetailRows(cd, innerWidth)
	if len(details) == 0 {
		return rows
	}
	return append(append(rows, ""), details...)
}

// renderCompactionHeader renders the single header line of the compaction box.
// The row never wraps: when the model label and subtitle do not fit, the model
// label is dropped from the meta cluster and the subtitle is truncated.
func (b *contentBuffer) renderCompactionHeader(cd *compactionBannerData, width int) string {
	disclosure := "▸"
	if cd.expanded {
		disclosure = "▾"
	}
	tag := b.styles.FgMute.Render(disclosure+" ") + b.styles.Warn.Bold(true).Render("compaction")
	tagW := lipgloss.Width(tag)

	subtitle := ""
	if cd.subtitle != "" {
		subtitle = " " + b.styles.FgDim.Render(cd.subtitle)
	}

	meta := b.renderCompactionHeaderMeta(cd, true)
	if tagW+lipgloss.Width(subtitle)+lipgloss.Width(meta)+1 > width {
		meta = b.renderCompactionHeaderMeta(cd, false)
		avail := width - tagW - lipgloss.Width(meta) - 1
		subtitle = ""
		if avail > 1 {
			if truncated := truncateOverlayText(cd.subtitle, avail-1); truncated != "" {
				subtitle = " " + b.styles.FgDim.Render(truncated)
			}
		}
	}

	left := tag + subtitle
	padding := width - lipgloss.Width(left) - lipgloss.Width(meta)
	if padding < 1 {
		padding = 1
	}

	return left + strings.Repeat(" ", padding) + meta
}

// renderCompactionHeaderMeta returns the right-aligned meta string for the
// header. The model label leads the cluster and is omitted when includeModel is
// false, which keeps the header on one row at narrow widths.
func (b *contentBuffer) renderCompactionHeaderMeta(cd *compactionBannerData, includeModel bool) string {
	var parts []string

	if includeModel {
		if label := compactionModelLabel(cd); label != "" {
			parts = append(parts, b.styles.FgMute.Render(label))
		}
	}

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

// compactionModelLabel returns the model label shown in the header meta: the
// configured alias when one exists, else the canonical provider/model. A
// skipped fallback reports that in the model slot instead of naming a model.
func compactionModelLabel(cd *compactionBannerData) string {
	if cd.fallbackStatus == output.CompactionFallbackSkipped {
		return "fallback skipped"
	}
	if cd.compactionModelAlias != "" {
		return cd.compactionModelAlias
	}
	return cd.compactionModel
}

// compactionDetailRows builds the expanded detail rows of the compaction box.
// Rows whose data is unknown are omitted, so an in-progress banner shows only
// the model and fallback it already knows.
func (b *contentBuffer) compactionDetailRows(cd *compactionBannerData, width int) []string {
	details := []struct{ key, value string }{
		{"model", compactionModelDetail(cd)},
		{"fallback", compactionFallbackDetail(cd)},
		{"tokens", compactionTokensDetail(cd)},
		{"window", compactionWindowDetail(cd)},
		{"history", compactionHistoryDetail(cd)},
		{"summary", compactionSummaryDetail(cd)},
		{"cache", compactionCacheDetail(cd)},
	}
	var rows []string
	for _, detail := range details {
		if detail.value == "" {
			continue
		}
		rows = append(rows, b.renderCompactionDetailRow(detail.key, detail.value, width))
	}
	return rows
}

// renderCompactionDetailRow renders one keyed detail row. The key column is 8
// cells wide; the value is flattened and truncated to the inner width so a long
// reason cannot wrap.
func (b *contentBuffer) renderCompactionDetailRow(key, value string, width int) string {
	keyColumn := fmt.Sprintf("%-8s ", key)
	if lipgloss.Width(keyColumn) >= width {
		return b.styles.FgMute.Render(truncateOverlayText(key+" "+value, width))
	}
	return b.styles.FgMute.Render(keyColumn) + truncateOverlayText(value, width-lipgloss.Width(keyColumn))
}

func compactionModelDetail(cd *compactionBannerData) string {
	switch {
	case cd.compactionModelAlias != "" && cd.compactionModel != "" && cd.compactionModelAlias != cd.compactionModel:
		return cd.compactionModelAlias + " (" + cd.compactionModel + ")"
	case cd.compactionModelAlias != "":
		return cd.compactionModelAlias
	default:
		return cd.compactionModel
	}
}

func compactionFallbackDetail(cd *compactionBannerData) string {
	var parts []string
	switch cd.fallbackStatus {
	case output.CompactionFallbackUsed:
		parts = append(parts, "used")
	case output.CompactionFallbackSkipped:
		parts = append(parts, "skipped")
	default:
		return ""
	}
	if reason := strings.TrimSpace(cd.fallbackReason); reason != "" {
		parts = append(parts, reason)
	}
	return strings.Join(parts, " · ")
}

func compactionTokensDetail(cd *compactionBannerData) string {
	if cd.beforeTokens == 0 && cd.afterTokens == 0 {
		return ""
	}
	value := fmt.Sprintf("%s → %s prompt", formatCompactCount(cd.beforeTokens), formatCompactCount(cd.afterTokens))
	if cd.beforePct != 0 || cd.afterPct != 0 {
		value += fmt.Sprintf(" · %s → %s context", formatCompactionPercent(cd.beforePct), formatCompactionPercent(cd.afterPct))
	}
	return value
}

func compactionWindowDetail(cd *compactionBannerData) string {
	var parts []string
	if cd.contextWindow > 0 {
		parts = append(parts, formatCompactCount(cd.contextWindow)+" context")
	}
	if cd.thresholdTokens > 0 || cd.compactionThreshold > 0 {
		threshold := "threshold " + formatCompactionPercent(cd.compactionThreshold)
		if cd.thresholdTokens > 0 {
			threshold += " (" + formatCompactCount(cd.thresholdTokens) + ")"
		}
		parts = append(parts, threshold)
		if cd.thresholdAchieved {
			parts = append(parts, "achieved")
		} else {
			parts = append(parts, "not achieved")
		}
	}
	return strings.Join(parts, " · ")
}

func compactionHistoryDetail(cd *compactionBannerData) string {
	var parts []string
	if cd.compactedTurns > 0 || cd.compactedMessages > 0 {
		parts = append(parts, fmt.Sprintf("%d turns / %d messages compacted", cd.compactedTurns, cd.compactedMessages))
	}
	if cd.retainedTurns > 0 || cd.retainedMessages > 0 {
		parts = append(parts, fmt.Sprintf("%d turns / %d messages retained", cd.retainedTurns, cd.retainedMessages))
	}
	if cd.retainedRawTurns > 0 {
		parts = append(parts, fmt.Sprintf("%d raw turns retained", cd.retainedRawTurns))
	}
	return strings.Join(parts, " · ")
}

func compactionSummaryDetail(cd *compactionBannerData) string {
	var parts []string
	if mode := strings.TrimSpace(cd.mode); mode != "" {
		parts = append(parts, mode+" mode")
	}
	if cd.summaryBytes > 0 {
		parts = append(parts, tuiFormatSize(cd.summaryBytes))
	}
	if cd.summaryTokenBudget > 0 {
		parts = append(parts, fmt.Sprintf("budget %d tokens", cd.summaryTokenBudget))
	}
	if cd.truncated {
		parts = append(parts, "truncated")
	}
	return strings.Join(parts, " · ")
}

func compactionCacheDetail(cd *compactionBannerData) string {
	if cd.cacheReadTokens == 0 && cd.inputTokens == 0 && cd.cacheCreateTokens == 0 {
		return ""
	}
	return fmt.Sprintf("read %s · input %s · create %s",
		formatCompactCount(cd.cacheReadTokens), formatCompactCount(cd.inputTokens), formatCompactCount(cd.cacheCreateTokens))
}

func formatCompactionPercent(value float64) string {
	return fmt.Sprintf("%.0f%%", value)
}
