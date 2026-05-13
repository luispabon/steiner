package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

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

func (s sidebarState) tokenBarLine(width int) string {
	pct := occupancyPercent(s.promptUsed, s.contextBudget)
	barWidth := max(4, width-2)

	var barStyle lipgloss.Style
	switch {
	case pct > 90:
		barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Removed))
	case pct > 70:
		barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn))
	default:
		barStyle = s.styles.Accent
	}

	filled := 0
	if s.contextBudget > 0 && barWidth > 0 {
		filled = (s.promptUsed * barWidth) / s.contextBudget
		if filled > barWidth {
			filled = barWidth
		}
	}

	barWithBg := barStyle.Background(lipgloss.Color(theme.Black))
	emptyWithBg := lipgloss.NewStyle().Background(lipgloss.Color(theme.Black))
	return barWithBg.Render(strings.Repeat("█", filled)) +
		emptyWithBg.Render(strings.Repeat(" ", barWidth-filled))
}

func (s sidebarState) tokenUsageLine(width int) string {
	pct := occupancyPercent(s.promptUsed, s.contextBudget)
	usageStr := sidebarPromptCount(s.promptUsed, s.contextBudget)
	pctStr := fmt.Sprintf("%d%%", pct)
	pad := width - len(usageStr) - len(pctStr)
	if pad < 1 {
		pad = 1
	}
	return s.styledWithBg(s.styles.FgDim, usageStr+strings.Repeat(" ", pad)+pctStr)
}

func (s sidebarState) compactDotLine() string {
	active := strings.TrimSpace(s.compaction) != "" && s.compaction != "idle"
	if active {
		dot := "●"
		if s.tickCount%2 == 0 {
			dot = "○"
		}
		dotStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn)).Background(lipgloss.Color(theme.Black))
		return dotStyle.Render(dot) + s.styledWithBg(s.styles.FgDim, " compacting…")
	}
	return s.styledWithBg(s.styles.FgFaint, "●") + s.styledWithBg(s.styles.FgDim, " auto @ 90%")
}
