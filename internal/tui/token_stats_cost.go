package tui

import (
	"fmt"

	"github.com/luispabon/steiner/internal/usagestats"
)

// formatCostCell renders one group's cost. A leading "~" marks an estimate from
// models.dev rates rather than a provider-reported amount, "+" marks groups
// where some requests had no price at all, and "-" means nothing in the group
// was priced.
func formatCostCell(t usagestats.Totals) string {
	priced := t.Requests - t.UnpricedRequests
	if priced <= 0 {
		return "-"
	}
	cell := formatCostAmount(t.CostMicroUSD, t.ReportedCostRequests < priced)
	if t.UnpricedRequests > 0 {
		cell += "+"
	}
	return cell
}

// formatCostAmount renders micro-USD as dollars. A positive amount below one
// cent shows "<$0.01" without an estimate prefix, because rounding hides the
// difference between the estimate and a reported amount.
func formatCostAmount(micro int64, estimated bool) string {
	prefix := ""
	if estimated {
		prefix = "~"
	}
	switch {
	case micro <= 0:
		return prefix + "$0.00"
	case micro < 10_000:
		return "<$0.01"
	default:
		return fmt.Sprintf("%s$%.2f", prefix, float64(micro)/1e6)
	}
}
