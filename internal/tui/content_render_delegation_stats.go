package tui

import (
	"fmt"
	"math"
	"strings"
)

func (b *contentBuffer) renderDelegationFooterSeparator(width int) string {
	if width < 1 {
		width = 1
	}
	return b.styles.FgDim.Render(strings.Repeat("─", width))
}

func (b *contentBuffer) renderDelegationStatsRow(dd *delegationDisplayState) string {
	parts := delegationStatsParts(b, dd)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "    ")
}

func delegationStatsParts(b *contentBuffer, dd *delegationDisplayState) []string {
	parts := make([]string, 0, 7)
	if badge := renderModelBadge(b.styles, dd.modelName, dd.reasoning); badge != "" {
		parts = append(parts, badge)
	}
	if dd.isAdvisor && dd.advisorUse > 0 && dd.advisorMaxUses > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Use: %d/%d", dd.advisorUse, dd.advisorMaxUses)))
	}
	if dd.turnCount > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Turns: %d", dd.turnCount)))
	}
	if dd.toolCallCount > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Tool Calls: %d", dd.toolCallCount)))
	}
	if dd.tokenCount > 0 {
		parts = append(parts, b.styles.FgDim.Render("Tokens: "+formatTokenPair(dd.cacheReadTokens+dd.inputTokens+dd.cacheCreateTokens, dd.tokenCount)))
	}
	if duration := delegationStatsDuration(dd); duration != "" {
		parts = append(parts, b.styles.FgDim.Render("Duration: "+duration))
	}
	if status := delegationStatsStatus(dd); status != "" {
		parts = append(parts, b.renderDelegationStatsStatus(status))
	}
	if ctx := delegationStatsContext(dd); ctx != "" {
		parts = append(parts, b.styles.FgDim.Render(ctx))
	}
	if dd.status == "active" && dd.latestCacheHitOK {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Cache: %s latest req · %s session", formatCacheHitRate(dd.latestCacheHitRate, dd.latestCacheHitOK), formatCacheHitRate(dd.cacheHitRate, dd.cacheHitOK))))
	} else if dd.cacheHitOK {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Cache: %s", formatCacheHitRate(dd.cacheHitRate, dd.cacheHitOK))))
	}
	if advStats := b.renderDelegationStatsAdvisor(dd); advStats != "" {
		parts = append(parts, advStats)
	}
	return parts
}

func (b *contentBuffer) renderDelegationStatsAdvisor(dd *delegationDisplayState) string {
	if dd.isAdvisor || dd.advisorBudget <= 0 {
		return ""
	}
	if dd.status != "complete" && dd.status != "failed" && dd.status != "budget_exhausted" {
		return ""
	}
	text := fmt.Sprintf("Advisor: %d/%d", dd.advisorUses, dd.advisorBudget)
	if dd.advisorDenied > 0 {
		return b.styles.FgDim.Render(text) + b.styles.Warn.Render(fmt.Sprintf(" +%d denied", dd.advisorDenied))
	}
	return b.styles.FgDim.Render(text)
}

func delegationStatsDuration(dd *delegationDisplayState) string {
	duration := strings.TrimSpace(dd.elapsed)
	if duration == "" && dd.status == "active" && dd.startTime > 0 {
		duration = formatElapsed(dd.startTime, nanoNow())
	}
	return duration
}

func delegationStatsStatus(dd *delegationDisplayState) string {
	status := strings.TrimSpace(dd.resultStatus)
	if status == "" {
		status = dd.status
	}
	return status
}

func delegationStatsContext(dd *delegationDisplayState) string {
	if dd.contextFillPct <= 0 {
		return ""
	}
	ctx := fmt.Sprintf("Ctx: %d%%", int(math.Round(dd.contextFillPct)))
	if dd.promptTokens > 0 && dd.contextWindow > 0 {
		ctx += fmt.Sprintf(" (%s / %s)", formatCompactCount(dd.promptTokens), formatCompactCount(dd.contextWindow))
	}
	return ctx
}

func (b *contentBuffer) renderDelegationStatsStatus(status string) string {
	label := b.styles.FgDim.Render("Status: ")
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "complete":
		return label + b.styles.SuccessStyle.Render(status)
	case "budget exhausted":
		return label + b.styles.Warn.Render(status)
	case "failed", "error":
		return label + b.styles.ErrorStyle.Render(status)
	default:
		return label + status
	}
}

func formatCompactCount(value int) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	case value >= 10_000:
		return fmt.Sprintf("%dk", int(math.Round(float64(value)/1_000)))
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func formatTokenPair(input, output int) string {
	return fmt.Sprintf("%s in / %s out", formatCompactCount(input), formatCompactCount(output))
}
