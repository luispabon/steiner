package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// groupAggregate summarises the children of a delegation group for its footer tab.
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

// delegationVisualGroupName returns the shared, trimmed non-empty group label
// for a rendered box. Unlike delivery grouping, this is independent of batch.
func delegationVisualGroupName(group *delegationGroupSegment) string {
	if group == nil || len(group.entries) == 0 || group.entries[0] == nil {
		return ""
	}
	name := strings.TrimSpace(group.entries[0].group)
	if name == "" {
		return ""
	}
	for _, dd := range group.entries[1:] {
		if dd == nil || strings.TrimSpace(dd.group) != name {
			return ""
		}
	}
	return name
}

func (b *contentBuffer) groupNameStyle(group *delegationGroupSegment) lipgloss.Style {
	style := b.styles.FgDim.Foreground(b.styles.AccentColor).Background(lipgloss.Color(b.styles.Palette.ContentBG)).Bold(true)
	key := strings.ToLower(strings.TrimSpace(delegationGroupBorderLabel(group)))
	if tag, ok := b.styles.DelegateTagStyles[key]; ok {
		style = style.Foreground(tag.GetForeground())
	}
	return style
}

// renderDelegationGroupFooter builds the bottom border and an outlined,
// right-aligned title tab with rounded bottom corners. Each line is exactly
// innerWidth+2 cells wide. When space is short the name is truncated first,
// then the stats are dropped.
func (b *contentBuffer) renderDelegationGroupFooter(group *delegationGroupSegment, innerWidth int, borderColor color.Color) string {
	bg := lipgloss.Color(b.styles.Palette.ContentBG)
	border := lipgloss.NewStyle().Foreground(borderColor).Background(bg)
	name := delegationVisualGroupName(group)
	agg := aggregateDelegationGroup(group)
	glyph, stateText := b.groupTitleStats(agg)
	noun := "agents"
	if agg.total == 1 {
		noun = "agent"
	}
	statsPlain := fmt.Sprintf("%d %s · %s %s", agg.total, noun, glyph, stateText)

	// One padding cell on each side, plus " · " between name and stats.
	const lead, trail, join = 1, 1, 3
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

	if name == "" && !showStats {
		return border.Render("└" + strings.Repeat("─", innerWidth) + "┘")
	}

	label := b.styles.FgDim.Background(bg)
	var title strings.Builder
	used := 0
	write := func(s string, style lipgloss.Style) {
		title.WriteString(style.Render(s))
		used += lipgloss.Width(s)
	}
	write(" ", label)
	if name != "" {
		write(name, b.groupNameStyle(group))
	}
	if name != "" && showStats {
		write(" · ", label)
	}
	if showStats {
		write(fmt.Sprintf("%d %s · ", agg.total, noun), label)
		write(glyph, b.groupGlyphStyle(agg).Background(bg))
		write(" "+stateText, label)
	}
	write(" ", label)

	indent := innerWidth - used
	leftCorner := "┬"
	if indent == 0 {
		leftCorner = "├"
	}
	bottom := border.Render("└" + strings.Repeat("─", max(0, indent-1)))
	if indent == 0 {
		bottom = ""
	}
	bottom += border.Render(leftCorner + strings.Repeat("─", used) + "┤")
	padding := lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", indent))
	return bottom + "\n" + padding + border.Render("│") + title.String() + border.Render("│") +
		"\n" + padding + border.Render("╰"+strings.Repeat("─", used)+"╯")
}
