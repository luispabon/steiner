package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/usagestats"
)

// writeWindowSection renders one window's heading, summary line and per-model
// table, with nested source rows for models split across call surfaces.
func writeWindowSection(sb *strings.Builder, label string, report usagestats.Report) {
	if len(report.Rows) == 0 {
		fmt.Fprintf(sb, "### %s\n\n%s\n\n", label, tokenStatsEmptyWindow)
		return
	}
	fmt.Fprintf(sb, "### %s · %d models · %d providers\n\n", label, report.Models, report.Providers)
	sb.WriteString(windowSummaryLine(report.Total))
	sb.WriteString("\n\n| Provider | Model | Reqs | Uncached | Read | Write | Output | Reason | Total | Hit | Cost |\n")
	sb.WriteString("|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, row := range report.Rows {
		writeTotalsRow(sb, row.ProviderAlias, row.BackendModelID, row.Totals)
		if len(row.Sources) > 1 {
			for _, source := range row.Sources {
				writeTotalsRow(sb, "", "└ "+tokenStatsSourceLabel(source.Source), source.Totals)
			}
		}
	}
	writeTotalsRow(sb, "**Total**", "", report.Total)
	sb.WriteString("\n")
}

// windowSummaryLine renders "42 requests · 93.7% of input from cache · 907
// output/request", dropping the hit rate when it is undefined.
func windowSummaryLine(t usagestats.Totals) string {
	line := fmt.Sprintf("%d requests", t.Requests)
	if rate, ok := t.HitRate(); ok {
		line += " · " + formatPercent(rate, ok) + " of input from cache"
	}
	return fmt.Sprintf("%s · %.0f output/request", line, outputPerRequest(t))
}

// outputPerRequest returns the average completion tokens per request.
func outputPerRequest(t usagestats.Totals) float64 {
	if t.Requests == 0 {
		return 0
	}
	return float64(t.CompletionTokens) / float64(t.Requests)
}

// writeTotalsRow writes one table row of token counts and cost.
func writeTotalsRow(sb *strings.Builder, provider, model string, t usagestats.Totals) {
	rate, ok := t.HitRate()
	fmt.Fprintf(sb, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
		provider, model,
		formatTokens(t.Requests),
		formatTokens(t.InputTokens),
		formatTokens(t.CacheReadTokens),
		formatTokens(t.CacheCreateTokens),
		formatTokens(t.CompletionTokens),
		formatTokens(t.ReasoningTokens),
		formatTokens(t.Total()),
		formatPercent(rate, ok),
		formatCostCell(t),
	)
}

// tokenStatsSourceLabel renders a call surface the way the store names it.
func tokenStatsSourceLabel(source usagestats.Source) string {
	switch source {
	case usagestats.SourceSubAgent:
		return "sub_agent"
	case usagestats.SourceAdvisor:
		return "advisor"
	case usagestats.SourceParent:
		return "parent"
	default:
		return "unknown"
	}
}

// writeBySourceSection renders the per-source split of the last calendar month.
// It is omitted when no call surface has traffic in the window.
func writeBySourceSection(sb *strings.Builder, rec *usagestats.Recorder, from time.Time) {
	sources := rec.BySource(from)
	if len(sources) == 0 {
		return
	}
	total := 0
	for _, source := range sources {
		total += source.Total()
	}

	sb.WriteString("### By source (last calendar month)\n\n")
	sb.WriteString("| Source | Reqs | Uncached in | Cache read | Output | Reason | Cost | Share of total |\n")
	sb.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, source := range sources {
		fmt.Fprintf(sb, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			tokenStatsSourceLabel(source.Source),
			formatTokens(source.Requests),
			formatTokens(source.InputTokens),
			formatTokens(source.CacheReadTokens),
			formatTokens(source.CompletionTokens),
			formatTokens(source.ReasoningTokens),
			formatCostCell(source.Totals),
			formatShare(source.Total(), total))
	}
	sb.WriteString("\n")
}

// formatShare renders value as a whole percent of total, or "-" when either is
// zero.
func formatShare(value, total int) string {
	if total <= 0 || value <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(value)/float64(total))
}
