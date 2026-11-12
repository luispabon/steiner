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

// Priorities for compacting the header meta cluster. Lower values fit first,
// so a narrow header keeps the progress glyph before the rate and timing text.
const (
	compactionMetaGlyphPriority = iota
	compactionMetaModelPriority
	compactionMetaCachePriority
	compactionMetaElapsedPriority
	compactionMetaCountPriority
)

// compactionHeaderPart is one candidate cell group of the header meta cluster.
// Parts are listed in display order; priority decides which survive a narrow
// header, and only the model label may be truncated to make room.
type compactionHeaderPart struct {
	plain    string // unstyled text used for width arithmetic
	styled   string
	priority int
	model    bool
}

// renderCompactionHeader renders the single header line of the compaction box.
// The content is fitted to exactly width cells: the tag truncates, then meta
// parts are admitted by priority, then the subtitle takes what remains. One
// cell is always reserved for padding, so the row never wraps.
func (b *contentBuffer) renderCompactionHeader(cd *compactionBannerData, width int) string {
	budget := width - 1
	if budget < 1 {
		budget = 1
	}

	left := b.compactionHeaderLeft(cd, budget)
	leftW := lipgloss.Width(left)
	meta := b.compactionHeaderMeta(cd, leftW, budget)

	subtitle := ""
	if remaining := width - 1 - leftW - lipgloss.Width(meta); remaining >= 2 && cd.subtitle != "" {
		if truncated := truncateOverlayText(cd.subtitle, remaining-1); truncated != "" {
			subtitle = " " + b.styles.FgDim.Render(truncated)
		}
	}

	left += subtitle
	padding := width - lipgloss.Width(left) - lipgloss.Width(meta)
	if padding < 1 {
		padding = 1
	}

	return left + strings.Repeat(" ", padding) + meta
}

// compactionHeaderLeft assembles the disclosure triangle and tag within budget.
// The tag truncates first; the triangle survives unless the budget cannot hold
// it with at least one tag cell.
func (b *contentBuffer) compactionHeaderLeft(cd *compactionBannerData, budget int) string {
	const tagText = "compaction"

	disclosure := "▸ "
	if cd.expanded {
		disclosure = "▾ "
	}
	triangle := b.styles.FgMute.Render(disclosure)

	left := triangle + b.styles.Warn.Bold(true).Render(tagText)
	if lipgloss.Width(left) <= budget {
		return left
	}

	tagRoom := budget
	left = ""
	if budget-lipgloss.Width(triangle) >= 1 {
		left = triangle
		tagRoom = budget - lipgloss.Width(triangle)
	}
	return left + b.styles.Warn.Bold(true).Render(truncateOverlayText(tagText, tagRoom))
}

// compactionHeaderMeta selects meta parts by priority and returns them joined in
// display order with a middot between elements. The progress glyph stays
// attached to the element before it. Only the model label may be truncated to
// make room.
func (b *contentBuffer) compactionHeaderMeta(cd *compactionBannerData, leftWidth, budget int) string {
	parts := b.compactionHeaderMetaParts(cd)
	chosen := make([]string, len(parts))
	for priority := compactionMetaGlyphPriority; priority <= compactionMetaCountPriority; priority++ {
		for i := range parts {
			part := parts[i]
			if part.priority != priority || chosen[i] != "" {
				continue
			}
			candidate := append([]string(nil), chosen...)
			candidate[i] = part.styled
			if leftWidth+compactionMetaWidth(parts, candidate) <= budget {
				chosen = candidate
				continue
			}
			// A shorter label is only worth showing when it still reads as one.
			if !part.model {
				continue
			}
			without := append([]string(nil), chosen...)
			without[i] = ""
			room := budget - leftWidth - compactionMetaWidth(parts, without) - compactionMetaSepAfter(parts, without, i)
			if room < 4 {
				continue
			}
			truncated := truncateOverlayText(part.plain, room)
			if truncated == "" {
				continue
			}
			candidate = append([]string(nil), chosen...)
			candidate[i] = b.styles.FgMute.Render(truncated)
			chosen = candidate
		}
	}
	return compactionMetaString(parts, chosen)
}

// compactionMetaSeparator is the cell group linking a chosen part to the element
// shown before it. The progress glyph stays attached with a space; every other
// part is separated by a middot.
func compactionMetaSeparator(part compactionHeaderPart) string {
	if part.priority == compactionMetaGlyphPriority {
		return " "
	}
	return " · "
}

// compactionMetaString joins the chosen parts in display order.
func compactionMetaString(parts []compactionHeaderPart, chosen []string) string {
	var out strings.Builder
	wrote := false
	for i, piece := range chosen {
		if piece == "" {
			continue
		}
		if wrote {
			out.WriteString(compactionMetaSeparator(parts[i]))
		}
		out.WriteString(piece)
		wrote = true
	}
	return out.String()
}

// compactionMetaWidth returns the rendered width of the chosen meta parts.
func compactionMetaWidth(parts []compactionHeaderPart, chosen []string) int {
	return lipgloss.Width(compactionMetaString(parts, chosen))
}

// compactionMetaSepAfter returns the width of the separator that follows the
// part at index when it is chosen, or zero when it is the last shown part.
func compactionMetaSepAfter(parts []compactionHeaderPart, chosen []string, index int) int {
	for i := index + 1; i < len(chosen); i++ {
		if chosen[i] != "" {
			return lipgloss.Width(compactionMetaSeparator(parts[i]))
		}
	}
	return 0
}

// compactionHeaderMetaParts returns the header meta candidates in display
// order: model, cache, elapsed, count, then the spinner or success glyph.
func (b *contentBuffer) compactionHeaderMetaParts(cd *compactionBannerData) []compactionHeaderPart {
	var parts []compactionHeaderPart

	if label := compactionModelLabel(cd); label != "" {
		parts = append(parts, compactionHeaderPart{
			plain:    label,
			styled:   b.styles.FgMute.Render(label),
			priority: compactionMetaModelPriority,
			model:    true,
		})
	}

	if cd.finished {
		if rate, ok := usagestats.HitRate(cd.cacheReadTokens, cd.inputTokens, cd.cacheCreateTokens); ok {
			plain := "cache " + formatCacheHitRate(rate, ok)
			parts = append(parts, compactionHeaderPart{plain: plain, styled: b.styles.FgDim.Render(plain), priority: compactionMetaCachePriority})
		}
	}

	elapsed := cd.elapsed
	if elapsed == "" && cd.startTime > 0 {
		elapsed = formatElapsed(cd.startTime, nanoNow())
	}
	if elapsed != "" {
		parts = append(parts, compactionHeaderPart{plain: elapsed, styled: b.styles.FgDim.Render(elapsed), priority: compactionMetaElapsedPriority})
	}

	if cd.compactionCount > 0 {
		plain := fmt.Sprintf("#%d", cd.compactionCount)
		parts = append(parts, compactionHeaderPart{plain: plain, styled: b.styles.FgDim.Render(plain), priority: compactionMetaCountPriority})
	}

	if !cd.finished {
		frame := spinnerFrames[cd.spinnerFrame%len(spinnerFrames)]
		parts = append(parts, compactionHeaderPart{plain: frame, styled: b.styles.FgMute.Render(frame), priority: compactionMetaGlyphPriority})
	} else if !cd.skipped {
		parts = append(parts, compactionHeaderPart{plain: "✓", styled: b.styles.SuccessStyle.Render("✓"), priority: compactionMetaGlyphPriority})
	}

	return parts
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
