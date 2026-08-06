package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func stripProviderURL(url string) string {
	url = strings.TrimSpace(url)
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimSuffix(url, "/v1")
	url = strings.TrimSuffix(url, "/")
	return url
}

// contextGaugeLine renders the bar, percentage, and used/budget counts on a
// single line: "██          9%   92k / 1.0m".
func (s sidebarState) contextGaugeLine(width int) string {
	pct := occupancyPercent(s.promptUsed, s.contextBudget)

	var thresholdStyle lipgloss.Style
	switch {
	case pct > 90:
		thresholdStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Removed))
	case pct > 70:
		thresholdStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn))
	default:
		thresholdStyle = s.styles.Accent
	}
	emptyWithBg := lipgloss.NewStyle().Background(lipgloss.Color(theme.Black))
	barWithBg := thresholdStyle.Background(lipgloss.Color(theme.Black))

	barWidth := min(10, max(4, width-16))
	filled := 0
	if s.contextBudget > 0 && barWidth > 0 {
		filled = (s.promptUsed * barWidth) / s.contextBudget
		if filled > barWidth {
			filled = barWidth
		}
	}
	bar := barWithBg.Render(strings.Repeat("█", filled)) +
		emptyWithBg.Render(strings.Repeat(" ", barWidth-filled))

	pctField := barWithBg.Render(fmt.Sprintf("%4s", fmt.Sprintf("%d%%", pct)))
	prefix := bar + emptyWithBg.Render("  ") + pctField

	var countsStr string
	if s.contextBudget <= 0 {
		countsStr = sidebarPromptCount(s.promptUsed, s.contextBudget)
	} else {
		countsStr = fmt.Sprintf("%s / %s", formatCompactCount(s.promptUsed), formatCompactCount(s.contextBudget))
	}
	counts := s.styledWithBg(s.styles.FgDim, countsStr)

	pad := width - lipgloss.Width(prefix) - lipgloss.Width(counts)
	if pad < 1 {
		pad = 1
	}
	return prefix + emptyWithBg.Render(strings.Repeat(" ", pad)) + counts
}

func (s sidebarState) compactDotLine() string {
	if !s.compaction.Active() {
		return ""
	}
	dot := "●"
	if s.tickCount%2 == 0 {
		dot = "○"
	}
	dotStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn)).Background(lipgloss.Color(theme.Black))
	return dotStyle.Render(dot) + s.styledWithBg(s.styles.FgDim, " compacting…")
}
