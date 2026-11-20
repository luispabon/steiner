package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/usagestats"
)

// windowTableColumns are the columns of a per-window model table.
var windowTableColumns = []tokenStatsColumn{
	{header: "Provider"},
	{header: "Model", flex: true},
	{header: "Reqs", right: true},
	{header: "Uncached", right: true},
	{header: "Read", right: true},
	{header: "Write", right: true, optional: tokenStatsOptionalWrite},
	{header: "Output", right: true},
	{header: "Reason", right: true, optional: tokenStatsOptionalReason},
	{header: "Total", right: true},
	{header: "Hit", right: true, optional: tokenStatsOptionalHit},
	{header: "Cost", right: true},
}

// sourceTableColumns are the columns of the per-source table.
var sourceTableColumns = []tokenStatsColumn{
	{header: "Source"},
	{header: "Reqs", right: true},
	{header: "Uncached in", right: true},
	{header: "Cache read", right: true},
	{header: "Output", right: true},
	{header: "Reason", right: true, optional: tokenStatsOptionalReason},
	{header: "Cost", right: true},
	{header: "Share of total", right: true},
}

// writeWindowSection renders one window's heading, summary line and per-model
// table, with nested source rows for models split across call surfaces.
func writeWindowSection(sb *strings.Builder, label string, report usagestats.Report, table tokenStatsTable, plan tokenStatsPlan) {
	if len(report.Rows) == 0 {
		fmt.Fprintf(sb, "### %s\n\n%s\n\n", label, tokenStatsEmptyWindow)
		return
	}
	fmt.Fprintf(sb, "### %s · %d models · %d providers\n\n", label, report.Models, report.Providers)
	sb.WriteString(windowSummaryLine(report.Total))
	sb.WriteString("\n\n")
	writeTokenStatsTable(sb, table, plan)
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

// tokenStatsWindowTable returns one window's model table: a row per model,
// nested source rows for models split across call surfaces, and the window
// total.
func tokenStatsWindowTable(report usagestats.Report) tokenStatsTable {
	rows := make([]tokenStatsRow, 0, 2*len(report.Rows)+1)
	for _, row := range report.Rows {
		rows = append(rows, tokenStatsRow{cells: windowRowCells(row.ProviderAlias, row.BackendModelID, row.Totals)})
		if len(row.Sources) > 1 {
			for _, source := range row.Sources {
				rows = append(rows, tokenStatsRow{cells: windowRowCells("", "└ "+tokenStatsSourceLabel(source.Source), source.Totals)})
			}
		}
	}
	rows = append(rows, tokenStatsRow{cells: windowRowCells("Total", "", report.Total), bold: true})
	return tokenStatsTable{columns: windowTableColumns, rows: rows}
}

// windowRowCells renders one window table row in windowTableColumns order.
func windowRowCells(provider, model string, t usagestats.Totals) []string {
	rate, ok := t.HitRate()
	return []string{
		provider,
		model,
		formatTokens(t.Requests),
		formatTokens(t.InputTokens),
		formatTokens(t.CacheReadTokens),
		formatTokens(t.CacheCreateTokens),
		formatTokens(t.CompletionTokens),
		formatTokens(t.ReasoningTokens),
		formatTokens(t.Total()),
		formatPercent(rate, ok),
		formatCostCell(t),
	}
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

// tokenStatsSourceTable returns the per-source split of the last calendar
// month, and false when no call surface has traffic in the window.
func tokenStatsSourceTable(rec *usagestats.Recorder, from time.Time) (tokenStatsTable, bool) {
	sources := rec.BySource(from)
	if len(sources) == 0 {
		return tokenStatsTable{}, false
	}
	total := 0
	for _, source := range sources {
		total += source.Total()
	}
	rows := make([]tokenStatsRow, 0, len(sources))
	for _, source := range sources {
		rows = append(rows, tokenStatsRow{cells: []string{
			tokenStatsSourceLabel(source.Source),
			formatTokens(source.Requests),
			formatTokens(source.InputTokens),
			formatTokens(source.CacheReadTokens),
			formatTokens(source.CompletionTokens),
			formatTokens(source.ReasoningTokens),
			formatCostCell(source.Totals),
			formatShare(source.Total(), total),
		}})
	}
	return tokenStatsTable{columns: sourceTableColumns, rows: rows}, true
}

// writeBySourceSection renders the per-source split table.
func writeBySourceSection(sb *strings.Builder, table tokenStatsTable, plan tokenStatsPlan) {
	sb.WriteString("### By source (last calendar month)\n\n")
	writeTokenStatsTable(sb, table, plan)
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
