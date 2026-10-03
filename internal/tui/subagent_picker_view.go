package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (s subAgentPickerOverlay) View() string {
	if !s.IsOpen() {
		return ""
	}
	w := s.InnerWidth()
	mute := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute))

	title := s.styles.Accent.Render(fmt.Sprintf("sub-agents · %d running · %d finished", s.totalRunning, s.totalDone))
	query := s.query
	if query == "" {
		query = mute.Render("filter sub-agents…")
	}
	lines := []string{
		lipgloss.NewStyle().MaxWidth(w).Render(title),
		lipgloss.NewStyle().Width(w).MaxWidth(w).Render(s.styles.Accent.Render("▶") + " " + query),
		"",
	}

	if len(s.rows) == 0 {
		lines = append(lines, mute.Render("no matching sub-agents"))
	}
	now := time.Now().UnixNano()
	end := min(s.scrollOffset+s.visibleRows(), len(s.rows))
	for i := s.scrollOffset; i < end; i++ {
		lines = append(lines, s.renderRow(i, w, now))
	}
	if end < len(s.rows) {
		lines = append(lines, mute.Render(fmt.Sprintf("… and %d more", len(s.rows)-end)))
	}

	footer := FooterChip("↑↓") + " select   " + FooterChip("↵") + " jump   " + FooterChip("esc") + " close"
	lines = append(lines, s.Divider(), s.RenderFooter(footer))
	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return s.RenderWithBg(s.styles.PaletteOverlay, body, theme.BgElev)
}

func (s subAgentPickerOverlay) renderRow(i, w int, now int64) string {
	row := s.rows[i]
	switch {
	case row.section:
		return lipgloss.NewStyle().MaxWidth(w).Render(s.styles.FgFaint.Render(row.heading))
	case row.groupHdr:
		return lipgloss.NewStyle().MaxWidth(w).Render(s.styles.FgMute.Render("┌ " + row.heading))
	}
	if i == s.selection {
		return s.styles.AccentBg.Width(w).MaxWidth(w).Render(plainCardRow(row, w, now))
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s.styledCardRow(row, w, now))
}

// cardParts holds the unstyled pieces of a card row.
type cardParts struct {
	prefix, icon, typ, id, elapsed, task string
}

func cardRowParts(row subAgentPickerRow, w int, now int64) cardParts {
	dd := row.dd
	p := cardParts{prefix: "  ", id: fitText(dd.agentID, 8)}
	if row.inGroup {
		p.prefix = "│ "
	}
	switch {
	case dd.status == "active" && dd.queuedForSlot:
		p.icon, p.elapsed = "◌", "queued"
	case dd.status == "active":
		p.icon = spinnerFrames[dd.spinnerFrame%len(spinnerFrames)]
		p.elapsed = formatRosterElapsed(dd.startTime, now)
	case dd.status == "failed":
		p.icon, p.elapsed = "✗", dd.elapsed
	default:
		p.icon, p.elapsed = "✓", dd.elapsed
	}
	p.typ = fitText(dd.effectiveTypeLabel(), 10)
	p.typ += strings.Repeat(" ", max(0, 10-lipgloss.Width(p.typ)))
	used := lipgloss.Width(p.prefix) + 2 + 11 + lipgloss.Width(p.id) + 1 + lipgloss.Width(p.elapsed) + 2
	p.task = fitText(strings.Join(strings.Fields(dd.taskPreview), " "), max(0, w-used))
	return p
}

func (p cardParts) join() string {
	return p.prefix + p.icon + " " + p.typ + " " + p.id + " " + p.elapsed + "  " + p.task
}

func plainCardRow(row subAgentPickerRow, w int, now int64) string {
	return cardRowParts(row, w, now).join()
}

func (s subAgentPickerOverlay) styledCardRow(row subAgentPickerRow, w int, now int64) string {
	p := cardRowParts(row, w, now)
	dd := row.dd
	finished := dd.status != "active"
	iconStyle := lipgloss.NewStyle().Foreground(s.styles.AccentColor)
	switch {
	case dd.queuedForSlot && !finished:
		iconStyle = s.styles.FgMute
	case dd.status == "failed":
		iconStyle = s.styles.ErrorStyle
	case dd.status == "complete":
		iconStyle = s.styles.SuccessStyle
	}
	typeStyle, ok := s.styles.DelegateTagStyles[strings.ToLower(dd.effectiveTypeLabel())]
	if !ok {
		typeStyle = s.styles.ToolTagDefault
	}
	meta := s.styles.FgMute
	if finished {
		typeStyle, meta = s.styles.FgDim, s.styles.FgFaint
	}
	return s.styles.FgMute.Render(p.prefix) + iconStyle.Render(p.icon) + " " + typeStyle.Render(p.typ) + " " +
		meta.Render(p.id+" "+p.elapsed) + "  " + s.styles.FgDim.Render(p.task)
}
