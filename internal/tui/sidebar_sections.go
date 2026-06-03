package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (s sidebarState) lines(width, innerHeight int) []string {
	static := s.staticLines(width)
	if len(static) > innerHeight {
		static = static[:innerHeight]
	}

	sorted := sortedModifiedFiles(s.modifiedFiles)
	availForFiles := max(0, innerHeight-len(static))
	overflow := innerHeight > 0 && len(sorted) > availForFiles
	displayCount := len(sorted)
	if overflow {
		displayCount = max(0, availForFiles-1)
	}

	lines := static
	for i := 0; i < displayCount && i < len(sorted); i++ {
		lines = append(lines, s.modifiedFileLine(sorted[i], width))
	}
	if overflow {
		lines = append(lines, s.styledWithBg(s.styles.FgMute, fmt.Sprintf("↓ %d more", len(sorted)-displayCount)))
	}
	return lines
}

func (s sidebarState) staticLines(width int) []string {
	lines := append([]string{}, s.brandLines(width)...)
	lines = append(lines, lipgloss.NewStyle().Background(lipgloss.Color(theme.Black)).Render(""))
	lines = append(lines, s.styledWithBg(s.styles.FgMute, strings.Repeat("─", max(0, width))))
	lines = append(lines, s.modelSection(width)...)
	if strings.TrimSpace(s.activeSkill) != "" {
		lines = append(lines, s.skillSection(width)...)
	}
	lines = append(lines, s.contextSection(width)...)
	lines = append(lines, s.repositorySection(width)...)
	lines = append(lines, "", cardLabel(fmt.Sprintf("modified files · %d", len(s.modifiedFiles)), s.styles))
	return lines
}

func (s sidebarState) modelSection(width int) []string {
	fgBright := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	lines := []string{
		"",
		cardLabel("model", s.styles),
		cardField("name", fgBright, fitText(safeText(s.model), width-7), s.styles),
	}
	if q := strings.TrimSpace(s.quant); q != "" {
		lines = append(lines, cardField("quant", s.styles.FgDim, fitText(q, width-7), s.styles))
	}
	host := fitText(stripProviderURL(s.provider), width-7)
	if host == "" {
		host = "n/a"
	}
	return append(lines, cardField("host", s.styles.FgDim, host, s.styles))
}

func (s sidebarState) contextSection(width int) []string {
	return []string{
		"",
		cardLabel("context", s.styles),
		s.tokenBarLine(width),
		s.tokenUsageLine(width),
		s.compactDotLine(),
	}
}

func (s sidebarState) skillSection(width int) []string {
	fgBright := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	return []string{
		"",
		cardLabel("skill", s.styles),
		cardField("active", fgBright, fitText(safeText(s.activeSkill), width-7), s.styles),
	}
}

func (s sidebarState) repositorySection(width int) []string {
	maxWD := min(25, max(1, width-7))
	lines := []string{
		"",
		cardLabel("repository", s.styles),
		cardField("workdir", s.styles.FgDim, fitTextMiddle(s.workdirSummary(), maxWD), s.styles),
		s.branchLine(width),
	}
	if s.ahead > 0 {
		lines = append(lines, cardField("ahead", s.styles.FgDim, fmt.Sprintf("%d commits", s.ahead), s.styles))
	}
	return lines
}
