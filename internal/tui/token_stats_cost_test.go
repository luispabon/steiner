package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/usagestats"
)

func TestFormatCostCell(t *testing.T) {
	tests := []struct {
		name   string
		totals usagestats.Totals
		want   string
	}{
		{
			name:   "no requests",
			totals: usagestats.Totals{},
			want:   "-",
		},
		{
			name:   "nothing priced",
			totals: usagestats.Totals{Requests: 3, UnpricedRequests: 3},
			want:   "-",
		},
		{
			name:   "estimated amount",
			totals: usagestats.Totals{Requests: 1, CostMicroUSD: 3_120_000},
			want:   "~$3.12",
		},
		{
			name:   "provider reported amount",
			totals: usagestats.Totals{Requests: 1, CostMicroUSD: 3_120_000, ReportedCostRequests: 1},
			want:   "$3.12",
		},
		{
			name:   "estimate plus partially priced requests",
			totals: usagestats.Totals{Requests: 2, CostMicroUSD: 5_130_000, UnpricedRequests: 1},
			want:   "~$5.13+",
		},
		{
			name:   "reported amount plus partially priced requests",
			totals: usagestats.Totals{Requests: 3, CostMicroUSD: 3_120_000, ReportedCostRequests: 2, UnpricedRequests: 1},
			want:   "$3.12+",
		},
		{
			name:   "mixed reported and estimated is an estimate",
			totals: usagestats.Totals{Requests: 2, CostMicroUSD: 5_130_000, ReportedCostRequests: 1},
			want:   "~$5.13",
		},
		{
			name:   "estimated free model",
			totals: usagestats.Totals{Requests: 1},
			want:   "~$0.00",
		},
		{
			name:   "reported free model",
			totals: usagestats.Totals{Requests: 1, ReportedCostRequests: 1},
			want:   "$0.00",
		},
		{
			name:   "sub-cent estimate below the boundary",
			totals: usagestats.Totals{Requests: 1, CostMicroUSD: 9_999},
			want:   "<$0.01",
		},
		{
			name:   "sub-cent reported amount below the boundary",
			totals: usagestats.Totals{Requests: 1, CostMicroUSD: 1, ReportedCostRequests: 1},
			want:   "<$0.01",
		},
		{
			name:   "one cent is the first rendered amount",
			totals: usagestats.Totals{Requests: 1, CostMicroUSD: 10_000},
			want:   "~$0.01",
		},
		{
			name:   "sub-cent estimate with unpriced requests keeps the suffix",
			totals: usagestats.Totals{Requests: 2, CostMicroUSD: 5_000, UnpricedRequests: 1},
			want:   "<$0.01+",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCostCell(tt.totals); got != tt.want {
				t.Fatalf("formatCostCell(%+v) = %q, want %q", tt.totals, got, tt.want)
			}
		})
	}
}

func TestTokenStatsReasonColumn(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	at := tokenStatsNow.Add(-2 * time.Hour)
	for _, observation := range []usagestats.Observation{
		{
			ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "with-reasoning",
			PromptTokens: 1000, CompletionTokens: 200, ReasoningTokens: 80, At: at,
		},
		{
			ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "no-reasoning",
			PromptTokens: 1000, CompletionTokens: 200, At: at,
		},
	} {
		rec.Record(observation)
	}

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	section := tokenStatsSection(t, got, "### Last 24 hours")
	for _, want := range []string{
		"| alpha | with-reasoning | 1 | 1.0k | - | - | 200 | 80 | 1.2k | - | - |",
		"| alpha | no-reasoning | 1 | 1.0k | - | - | 200 | - | 1.2k | - | - |",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("section = %q, want row %q", section, want)
		}
	}
}

func TestTokenStatsTotalRowSumsCostAndFlagsUnpriced(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	at := tokenStatsNow.Add(-2 * time.Hour)
	one := 1.0
	two := 2.12
	for _, observation := range []usagestats.Observation{
		{ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "paid", PromptTokens: 10, ReportedCostUSD: &one, At: at},
		{ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "paid", PromptTokens: 10, ReportedCostUSD: &two, At: at},
		{ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "free", PromptTokens: 10, At: at},
	} {
		rec.Record(observation)
	}

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	section := tokenStatsSection(t, got, "### Last 24 hours")
	for _, want := range []string{
		"| alpha | free | 1 | 10 | - | - | - | - | 10 | - | - |",
		"| alpha | paid | 2 | 20 | - | - | - | - | 20 | - | $3.12 |",
		"| **Total** |  | 3 | 30 | - | - | - | - | 30 | - | $3.12+ |",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("section = %q, want row %q", section, want)
		}
	}
}
