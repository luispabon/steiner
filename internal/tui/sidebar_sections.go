package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

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
	if overflow && availForFiles > 0 {
		lines = append(lines, s.styledWithBg(s.styles.FgMute, fmt.Sprintf("↓ %d more", len(sorted)-displayCount)))
	}
	return lines
}

func (s sidebarState) staticLines(width int) []string {
	lines := append([]string{}, s.brandLines(width)...)
	lines = append(lines, lipgloss.NewStyle().Background(lipgloss.Color(theme.Black)).Render(""))
	lines = append(lines, s.separatorLine(width))
	lines = append(lines, s.modelSection(width)...)
	lines = append(lines, s.contextSection(width)...)
	lines = append(lines, s.statusSection(width)...)
	lines = append(lines, s.performanceSection(width)...)
	if s.oneshotPhase != "" {
		lines = append(lines, s.oneshotSection(width)...)
	}
	lines = append(lines, s.repositorySection(width)...)
	lines = append(lines, "", cardLabel(fmt.Sprintf("modified files · %d", len(s.modifiedFiles)), s.styles))
	return lines
}

func (s sidebarState) modelSection(width int) []string {
	fgBright := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Background(lipgloss.Color(theme.Black))
	fgDim := s.styles.FgDim.Background(lipgloss.Color(theme.Black))

	model := fitText(safeText(s.model), width)
	provider := strings.TrimSpace(s.providerName)
	if provider == "" {
		provider = stripProviderURL(s.provider)
	}

	var line1 string
	switch {
	case provider == "":
		line1 = fgBright.Render(model)
	case len(provider)+1+len(model) <= width:
		line1 = fgDim.Render(provider+"/") + fgBright.Render(model)
	default:
		remaining := width - len(model) - 1
		if remaining < 4 {
			line1 = fgBright.Render(model)
		} else {
			line1 = fgDim.Render(fitText(provider, remaining)+"/") + fgBright.Render(model)
		}
	}

	lines := []string{"", cardLabel("model", s.styles), line1}

	reasoning := strings.TrimSpace(s.reasoning)
	quant := strings.TrimSpace(s.quant)
	switch {
	case reasoning != "" && quant != "":
		lines = append(lines, s.styledWithBg(s.styles.FgDim, fitText(fmt.Sprintf("reasoning: %s · %s", reasoning, quant), width)))
	case reasoning != "":
		lines = append(lines, s.styledWithBg(s.styles.FgDim, fitText("reasoning: "+reasoning, width)))
	case quant != "":
		lines = append(lines, s.styledWithBg(s.styles.FgDim, fitText("quant: "+quant, width)))
	}

	return lines
}

// sandboxStatusStyle returns the colour for a sandbox status value: green
// for active, amber for unavailable, red for bypassed, dim otherwise.
func sandboxStatusStyle(status string, styles theme.Styles) lipgloss.Style {
	switch status {
	case "active":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Added))
	case "unavailable":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn))
	case "bypassed":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Removed))
	default:
		return styles.FgDim
	}
}

// statusSection renders the compact sandbox/skill/MCP status trio. Returns
// nil when all three are absent, so no stray blank line is emitted.
func (s sidebarState) statusSection(width int) []string {
	const keyW = 8
	fgBright := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))

	var rows []string
	if status := strings.TrimSpace(s.sandboxStatus); status != "" {
		rows = append(rows, cardFieldAccent("SANDBOX", sandboxStatusStyle(status, s.styles), fitText(status, width-keyW), s.styles))
	}
	if skill := strings.TrimSpace(s.activeSkill); skill != "" {
		rows = append(rows, cardFieldAccent("SKILL", fgBright, fitText(skill, width-keyW), s.styles))
	}
	if spinner, count := s.mcpRow(); count != "" {
		mcpStyle := fgBright
		if s.mcpFailed {
			mcpStyle = s.styles.ErrorStyle
		}
		if spinner != "" {
			row := cardFieldAccent("MCP", s.styles.FgMute, spinner+" ", s.styles) +
				s.styledWithBg(mcpStyle, count)
			rows = append(rows, row)
		} else {
			rows = append(rows, cardFieldAccent("MCP", mcpStyle, count, s.styles))
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return append([]string{""}, rows...)
}

// separatorLine renders the separator line with the mode label centered.
// When mode is "plan" or "build", renders a width-filling line of dashes
// with the mode label centered (e.g. "──── plan ────"). Dashes use a muted
// mode color; the label uses the full mode color. When no mode is set,
// returns all dashes in mute color.
func (s sidebarState) separatorLine(width int) string {
	mode := strings.TrimSpace(s.execMode)
	switch mode {
	case "plan":
		label := " plan "
		dashCount := max(0, width-len(label))
		left := dashCount / 2
		right := dashCount - left
		dashStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.DelegateThinkingLine)).
			Background(lipgloss.Color(theme.Black))
		labelStyle := s.styles.ModePlanStyle.Background(lipgloss.Color(theme.Black))
		return dashStyle.Render(strings.Repeat("─", left)) +
			labelStyle.Render(label) +
			dashStyle.Render(strings.Repeat("─", right))
	case "build":
		label := " build "
		dashCount := max(0, width-len(label))
		left := dashCount / 2
		right := dashCount - left
		dashStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.ToolGrnLine)).
			Background(lipgloss.Color(theme.Black))
		labelStyle := s.styles.ModeBuildStyle.Background(lipgloss.Color(theme.Black))
		return dashStyle.Render(strings.Repeat("─", left)) +
			labelStyle.Render(label) +
			dashStyle.Render(strings.Repeat("─", right))
	default:
		return s.styledWithBg(s.styles.FgMute, strings.Repeat("─", max(0, width)))
	}
}

func (s sidebarState) contextSection(width int) []string {
	lines := []string{
		"",
		cardLabel("context", s.styles),
		s.contextGaugeLine(width),
	}
	if dot := s.compactDotLine(); dot != "" {
		lines = append(lines, dot)
	}
	return lines
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

func (s sidebarState) performanceSection(width int) []string {
	w := width - 7
	const keyW = 10
	return []string{
		"",
		cardLabel("performance", s.styles),
		cardFieldN("duration", keyW, s.styles.FgDim, fitText(formatDuration(s.perfDurationMs), w-keyW+7), s.styles),
		cardFieldN("ttft", keyW, s.styles.FgDim, fitText(formatDuration(s.perfTTFTMs), w-keyW+7), s.styles),
		cardFieldN("tps", keyW, s.styles.FgDim, fitText(formatTPS(s.perfOutputTPS), w-keyW+7), s.styles),
		cardFieldN("cache hit", keyW, s.styles.FgDim, fitText(formatCacheHitRate(s.sessionCacheHitRate, s.sessionCacheHitRateOK), w-keyW+7), s.styles),
	}
}

// formatCacheHitRate renders the hit rate as "78.2%" or "—" when undefined.
func formatCacheHitRate(rate float64, ok bool) string {
	if !ok {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", rate*100)
}

// formatDuration formats ms as "1.2s" or "340ms"
func formatDuration(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	if ms >= 1000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
	}
	return fmt.Sprintf("%dms", ms)
}

// formatTPS formats tokens/sec as "42.1 t/s"
func formatTPS(tps float64) string {
	if tps <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f t/s", tps)

}

// oneshotSection renders the oneshot phase in the sidebar.
func (s sidebarState) oneshotSection(_ int) []string {
	title := fmt.Sprintf("Oneshot - %s", s.oneshotPhase)
	return []string{
		"",
		cardLabel(title, s.styles),
	}
}
