package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/usagestats"
)

func TestTokenStatsHeadlineBanner(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	recordTokenStatsFixture(t, rec, tokenStatsNow)

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	if !strings.Contains(got, "█") {
		t.Fatalf("report = %q, want a block banner at a wide width", got)
	}
	if !strings.Contains(got, "*tokens* · last calendar month · Oct 18 to Nov 18 (UTC)") {
		t.Fatalf("report = %q, want the banner label and month range", got)
	}
	if !strings.Contains(got, "2 requests · 66.7% of input from cache") {
		t.Fatalf("report = %q, want the headline request and hit rate line", got)
	}
	if !strings.Contains(got, "cached 2.0k · uncached 1.0k · output 500") {
		t.Fatalf("report = %q, want the cached, uncached and output line", got)
	}
}

func TestTokenStatsHeadlineFallsBackWhenNarrow(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	recordTokenStatsFixture(t, rec, tokenStatsNow)

	// 3.5k needs a 26-cell banner plus margin, so this width cannot hold it.
	got := formatTokenStatsReport(rec, tokenStatsNow, 20)
	headline := tokenStatsPrefix(t, got, "### This week")
	if strings.Contains(headline, "█") {
		t.Fatalf("headline = %q, want no banner at a narrow width", headline)
	}
	if !strings.Contains(got, "**3.5k tokens** · last calendar month · Oct 18 to Nov 18 (UTC)") {
		t.Fatalf("report = %q, want the bold fallback line", got)
	}
}

func TestTokenStatsHeadlineClampsMonthEnd(t *testing.T) {
	at := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)
	rec := newTokenStatsRecorder(t, at)
	rec.Record(usagestats.Observation{
		ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1",
		PromptTokens: 100, At: at.Add(-time.Hour),
	})

	got := formatTokenStatsReport(rec, at, 120)
	if !strings.Contains(got, "Feb 28 to Mar 31 (UTC)") {
		t.Fatalf("report = %q, want the clamped month range Feb 28 to Mar 31", got)
	}
}

func TestTokenStatsWeekStrip(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	recordTokenStatsFixture(t, rec, tokenStatsNow)

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	if !strings.Contains(got, "### This week (Mon Nov 16 to Sun Nov 22, UTC)") {
		t.Fatalf("report = %q, want the current week heading", got)
	}

	fullBar := barString(1200, 1200, tokenStatsBarWidth)
	emptyBar := barString(0, 1200, tokenStatsBarWidth)
	for _, want := range []string{
		fmt.Sprintf("Mon 16%s%s%s%7s", tokenStatsSpacing, emptyBar, tokenStatsSpacing, "-"),
		fmt.Sprintf("Wed 18%s%s%s%7s%s(today, partial)", tokenStatsSpacing, fullBar, tokenStatsSpacing, "1.2k", tokenStatsSpacing),
		fmt.Sprintf("Sun 22%s·", tokenStatsSpacing),
		fmt.Sprintf("week: %s", formatTokens(1200)),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report = %q, want the week strip line %q", got, want)
		}
	}
	if strings.Contains(got, fmt.Sprintf("Sat 21%s%s", tokenStatsSpacing, emptyBar)) {
		t.Fatalf("report = %q, want future days to show only a dot", got)
	}
}

func TestTokenStatsWeekStartsOnMonday(t *testing.T) {
	monday := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	rec := newTokenStatsRecorder(t, monday)
	rec.Record(usagestats.Observation{
		ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1",
		PromptTokens: 100, At: monday,
	})

	got := formatTokenStatsReport(rec, monday, 120)
	if !strings.Contains(got, "### This week (Mon Nov 16 to Sun Nov 22, UTC)") {
		t.Fatalf("report = %q, want the week to start on the current Monday", got)
	}
	if !strings.Contains(got, "Mon 16") || !strings.Contains(got, "(today, partial)") {
		t.Fatalf("report = %q, want Monday marked as today", got)
	}
	if strings.Contains(got, "### Previous weeks") {
		t.Fatalf("report = %q, want no previous weeks when only today has data", got)
	}
}

func TestTokenStatsPreviousWeeks(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	recordTokenStatsFixture(t, rec, tokenStatsNow)

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	if !strings.Contains(got, "### Previous weeks") {
		t.Fatalf("report = %q, want a previous weeks section", got)
	}

	fullBar := barString(2300, 2300, tokenStatsBarWidth)
	bound := fmt.Sprintf("Nov 9 to Nov 15%s%7s%s%s", tokenStatsSpacing, formatTokens(2300), tokenStatsSpacing, fullBar)
	if !strings.Contains(got, bound) {
		t.Fatalf("report = %q, want the previous week row %q", got, bound)
	}

	newest := strings.Index(got, "Nov 9 to Nov 15")
	older := strings.Index(got, "Nov 2 to Nov 8")
	if newest < 0 || older < 0 || newest > older {
		t.Fatalf("report = %q, want weeks newest first", got)
	}

	oldest := "Oct 12 to Oct 18"
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, oldest) {
			if !strings.Contains(line, "(partial)") {
				t.Fatalf("oldest week line = %q, want a partial marker", line)
			}
			return
		}
	}
	t.Fatalf("report = %q, want the retention-clipped oldest week %q", got, oldest)
}

func TestTokenStatsHeadlineSkippedWithoutMonthData(t *testing.T) {
	// Two months back is outside both the calendar month and the store's
	// retention, so it reaches no window but the session report.
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	rec.Record(usagestats.Observation{
		ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1",
		PromptTokens: 100, At: tokenStatsNow.AddDate(0, -2, 0),
	})

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	headline := tokenStatsPrefix(t, got, "### This week")
	if strings.Contains(headline, "█") {
		t.Fatalf("headline = %q, want no banner without month data", headline)
	}
	if !strings.Contains(headline, tokenStatsEmptyWindow) {
		t.Fatalf("headline = %q, want the empty headline window message", headline)
	}
}
