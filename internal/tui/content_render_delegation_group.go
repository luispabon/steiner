package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// groupAggregate summarises the children of a delegation group for the title
// embedded in its top border.
type groupAggregate struct {
	total, complete, failed, budget int
	active                          *delegationDisplayState // first active child, nil when none
}

func aggregateDelegationGroup(group *delegationGroupSegment) groupAggregate {
	agg := groupAggregate{total: len(group.entries)}
	for _, dd := range group.entries {
		switch dd.status {
		case "active":
			if agg.active == nil {
				agg.active = dd
			}
		case "complete":
			agg.complete++
		case "failed":
			agg.failed++
		case "budget_exhausted":
			agg.budget++
		}
	}
	return agg
}

// groupTitleStats returns the glyph and state text shown after "N agents · " in
// the group title.
// Running children win over failures so the title keeps spinning until the
// whole group has settled.
func (b *contentBuffer) groupTitleStats(agg groupAggregate) (glyph, text string) {
	switch {
	case agg.active != nil:
		frame := "⧖"
		if !agg.active.queuedForSlot && !agg.active.cacheWaiting {
			frame = spinnerFrames[agg.active.spinnerFrame%len(spinnerFrames)]
		}
		return frame, fmt.Sprintf("%d/%d", agg.complete, agg.total)
	case agg.failed > 0:
		return "✗", fmt.Sprintf("%d failed", agg.failed)
	case agg.budget > 0:
		return "!", fmt.Sprintf("%d budget exhausted", agg.budget)
	default:
		return "✓", fmt.Sprintf("%d/%d", agg.complete, agg.total)
	}
}

func (b *contentBuffer) groupGlyphStyle(agg groupAggregate) lipgloss.Style {
	switch {
	case agg.active != nil:
		return b.styles.FgMute
	case agg.failed > 0:
		return b.styles.ErrorStyle
	case agg.budget > 0:
		return b.styles.Warn
	default:
		return b.styles.SuccessStyle
	}
}

// renderDelegationGroupTopBorder builds the top border line of a group box with
// the group name (bold) and aggregate status embedded in it. innerWidth is the
// number of cells between the two corner glyphs. When space is short the name
// is truncated first, then the stats are dropped, then the name; the line is
// always exactly innerWidth+2 cells wide.
func (b *contentBuffer) renderDelegationGroupTopBorder(group *delegationGroupSegment, innerWidth int, borderColor color.Color) string {
	bg := lipgloss.Color(b.styles.Palette.ContentBG)
	border := lipgloss.NewStyle().Foreground(borderColor).Background(bg)
	name := strings.TrimSpace(group.entries[0].group)
	agg := aggregateDelegationGroup(group)
	glyph, stateText := b.groupTitleStats(agg)
	noun := "agents"
	if agg.total == 1 {
		noun = "agent"
	}
	statsPlain := fmt.Sprintf("%d %s · %s %s", agg.total, noun, glyph, stateText)

	// Fixed cells: "─ " before the first piece, " " after the last, plus the
	// " ─ " between name and stats when both are shown.
	const lead, trail, join = 2, 1, 3
	showStats := true
	avail := innerWidth - lead - trail
	statsWidth := lipgloss.Width(statsPlain)
	if name != "" {
		nameAvail := avail - join - statsWidth
		if nameAvail < 1 {
			showStats = false
			nameAvail = avail
		}
		if nameAvail < 1 {
			name = ""
		} else {
			name = truncateRunes(name, nameAvail)
		}
	}
	if name == "" && statsWidth > avail {
		showStats = false
	}

	var title strings.Builder
	used := 0
	write := func(s string, style lipgloss.Style) {
		title.WriteString(style.Background(bg).Render(s))
		used += lipgloss.Width(s)
	}
	if name != "" || showStats {
		write("─ ", border)
	}
	if name != "" {
		write(name, b.styles.FgDim.Bold(true))
	}
	if name != "" && showStats {
		write(" ─ ", border)
	}
	if showStats {
		write(fmt.Sprintf("%d %s · ", agg.total, noun), b.styles.FgDim)
		write(glyph, b.groupGlyphStyle(agg))
		write(" "+stateText, b.styles.FgDim)
	}
	if used > 0 {
		write(" ", border)
	}
	fill := max(0, innerWidth-used)
	return border.Render("┌") + title.String() + border.Render(strings.Repeat("─", fill)+"┐")
}
