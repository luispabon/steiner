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
	lines = append(lines, s.performanceSection(width)...)
	lines = append(lines, s.cacheSection(width)...)
	if s.oneshotPhase != "" {
		lines = append(lines, s.oneshotSection(width)...)
	}
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
	if row := s.modeRow(); row != "" {
		lines = append(lines, row)
	}
	if r := strings.TrimSpace(s.reasoning); r != "" {
		lines = append(lines, cardField("reasoning", s.styles.FgDim, fitText(r, width-7), s.styles))
	}
	if q := strings.TrimSpace(s.quant); q != "" {
		lines = append(lines, cardField("quant", s.styles.FgDim, fitText(q, width-7), s.styles))
	}
	providerDisplay := strings.TrimSpace(s.providerName)
	if providerDisplay == "" {
		providerDisplay = fitText(stripProviderURL(s.provider), width-7)
	} else {
		providerDisplay = fitText(providerDisplay, width-11)
	}
	if providerDisplay == "" {
		providerDisplay = "n/a"
	}
	return append(lines, cardField("provider", s.styles.FgDim, providerDisplay, s.styles))
}

// modeRow renders the "mode" field in the model section using the glyph +
// text badge, styled per the current execution mode. Returns "" when no mode
// is set (e.g. before startup seeding).
func (s sidebarState) modeRow() string {
	switch strings.TrimSpace(s.execMode) {
	case "plan":
		return cardField("mode", s.styles.ModePlanStyle, "⏸ plan", s.styles)
	case "build":
		return cardField("mode", s.styles.ModeBuildStyle, "⏵⏵ build", s.styles)
	default:
		return ""
	}
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

func (s sidebarState) performanceSection(width int) []string {
	w := width - 7
	const keyW = 10
	return []string{
		"",
		cardLabel("performance", s.styles),
		cardFieldN("duration", keyW, s.styles.FgDim, fitText(formatDuration(s.perfDurationMs), w-keyW+7), s.styles),
		cardFieldN("ttft", keyW, s.styles.FgDim, fitText(formatDuration(s.perfTTFTMs), w-keyW+7), s.styles),
		cardFieldN("tps", keyW, s.styles.FgDim, fitText(formatTPS(s.perfOutputTPS), w-keyW+7), s.styles),
	}
}

// cacheSection renders the current session's token-weighted cache hit rate.
func (s sidebarState) cacheSection(width int) []string {
	w := width - 7
	const keyW = 10
	return []string{
		"",
		cardLabel("cache", s.styles),
		cardFieldN("hit rate", keyW, s.styles.FgDim, fitText(formatCacheHitRate(s.sessionCacheHitRate, s.sessionCacheHitRateOK), w-keyW+7), s.styles),
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
