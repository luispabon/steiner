package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/usagestats"
)

func TestTokenStatsWindowTable(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	recordTokenStatsFixture(t, rec, tokenStatsNow)

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	section := tokenStatsSection(t, got, "### Last 24 hours")

	if !strings.Contains(section, "1 models · 1 providers") {
		t.Fatalf("section = %q, want the model and provider counts", section)
	}
	if !strings.Contains(section, "1 requests · 50.0% of input from cache · 200 output/request") {
		t.Fatalf("section = %q, want the summary line", section)
	}
	for _, want := range []string{
		"| Provider | Model | Reqs | Uncached | Read | Write | Output | Reason | Total | Hit | Cost |",
		"|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
		"| alpha | m1 | 1 | 400 | 500 | 100 | 200 | - | 1.2k | 50.0% | - |",
		"| **Total** |  | 1 | 400 | 500 | 100 | 200 | - | 1.2k | 50.0% | - |",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("section = %q, want %q", section, want)
		}
	}
}

func TestTokenStatsNestedSourceRowsOnlyWhenMultiple(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	at := tokenStatsNow.Add(-2 * time.Hour)
	for _, observation := range []usagestats.Observation{
		{ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "split", PromptTokens: 100, At: at},
		{ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "split", PromptTokens: 200, At: at, Source: usagestats.SourceAdvisor},
		{ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "solo", PromptTokens: 300, At: at},
	} {
		rec.Record(observation)
	}

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	section := tokenStatsSection(t, got, "### Last 24 hours")

	if count := strings.Count(section, "└ parent"); count != 1 {
		t.Fatalf("section = %q, want one parent row, got %d", section, count)
	}
	if !strings.Contains(section, "|  | └ advisor |") {
		t.Fatalf("section = %q, want a blank provider cell and an advisor row", section)
	}
	if count := strings.Count(section, "└ "); count != 2 {
		t.Fatalf("section = %q, want nested rows only for the split model, got %d", section, count)
	}
}

func TestTokenStatsBySourceShares(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	at := tokenStatsNow.Add(-2 * time.Hour)
	for _, observation := range []usagestats.Observation{
		{ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1", PromptTokens: 1000, CompletionTokens: 200, At: at},
		{ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1", PromptTokens: 300, At: at, Source: usagestats.SourceAdvisor},
		{ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1", At: at, Source: usagestats.SourceSubAgent},
	} {
		rec.Record(observation)
	}

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	section := tokenStatsSection(t, got, "### By source (last calendar month)")

	for _, want := range []string{
		"| Source | Reqs | Uncached in | Cache read | Output | Reason | Cost | Share of total |",
		"| parent | 1 | 1.0k | - | 200 | - | - | 80% |",
		"| advisor | 1 | 300 | - | - | - | - | 20% |",
		"| sub_agent | 1 | - | - | - | - | - | - |",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("section = %q, want %q", section, want)
		}
	}
}

func TestTokenStatsEmptyWindowMessage(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	recordTokenStatsFixture(t, rec, tokenStatsNow)

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	want := "### Last hour\n\n" + tokenStatsEmptyWindow
	if !strings.Contains(got, want) {
		t.Fatalf("report = %q, want the empty last-hour window %q", got, want)
	}
}

func TestTokenStatsWindowOrder(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	recordTokenStatsFixture(t, rec, tokenStatsNow)

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	order := []string{"### This session", "### Last hour", "### Last 24 hours", "### Last 7 days", "### Last calendar month", "### By source"}
	position := -1
	for _, heading := range order {
		next := strings.Index(got, heading)
		if next < 0 {
			t.Fatalf("report = %q, want section %q", got, heading)
		}
		if next < position {
			t.Fatalf("report = %q, want %q after the previous section", got, heading)
		}
		position = next
	}
}
