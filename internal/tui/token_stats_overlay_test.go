package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/usagestats"
)

// tokenStatsNow is the clock shared by the token stats tests: Wednesday 18
// November 2026, mid-week and mid-month so week and month boundaries differ.
var tokenStatsNow = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// newUsageRecorder returns a recorder whose async store writer is drained and
// stopped before the test's temp state home is removed.
func newUsageRecorder(t *testing.T, now func() time.Time) *usagestats.Recorder {
	t.Helper()
	rec := usagestats.New(now)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rec.Drain(ctx); err != nil {
			t.Errorf("drain usage stats recorder: %v", err)
		}
		rec.Close()
	})
	return rec
}

// newTokenStatsRecorder returns a recorder isolated to a temp state home and
// pinned to at. Not parallel-safe: t.Setenv forbids t.Parallel.
func newTokenStatsRecorder(t *testing.T, at time.Time) *usagestats.Recorder {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return newUsageRecorder(t, func() time.Time { return at })
}

// recordTokenStatsFixture records two parent observations: one earlier today
// and one eight days ago, inside the same calendar month.
func recordTokenStatsFixture(t *testing.T, rec *usagestats.Recorder, now time.Time) {
	t.Helper()
	observations := []usagestats.Observation{
		{
			ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1",
			PromptTokens: 1000, CompletionTokens: 200, CacheReadTokens: 500, CacheCreateTokens: 100,
			At: now.Add(-2 * time.Hour),
		},
		{
			ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1",
			PromptTokens: 2000, CompletionTokens: 300, CacheReadTokens: 1500,
			At: now.AddDate(0, 0, -8),
		},
	}
	for _, observation := range observations {
		rec.Record(observation)
	}
}

func TestFormatTokenStatsReportNilRecorder(t *testing.T) {
	t.Parallel()
	if got := formatTokenStatsReport(nil, tokenStatsNow, 120); got != tokenStatsEmptyReport {
		t.Fatalf("nil recorder report = %q, want %q", got, tokenStatsEmptyReport)
	}
}

func TestFormatTokenStatsReportEmptyRecorder(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	if got := formatTokenStatsReport(rec, tokenStatsNow, 120); got != tokenStatsEmptyReport {
		t.Fatalf("empty recorder report = %q, want %q", got, tokenStatsEmptyReport)
	}
}

func TestFormatTokenStatsReportSessionRows(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	recordTokenStatsFixture(t, rec, tokenStatsNow)

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	if !strings.Contains(got, "### This session · 1 models · 1 providers") {
		t.Fatalf("report = %q, want a session heading with one model and one provider", got)
	}

	section := tokenStatsSection(t, got, "### This session")
	// Two requests, 900 uncached input, 2000 cache read, 100 cache write,
	// 500 output, 3500 total, hit rate 2000/3000.
	wantRow := "| alpha | m1 | 2 | 900 | 2.0k | 100 | 500 | - | 3.5k | 66.7% | - |"
	if !strings.Contains(section, wantRow) {
		t.Fatalf("session section = %q, want row %q", section, wantRow)
	}
}

func TestFormatTokenStatsReportOmitsUndefinedHitRate(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	rec.Record(usagestats.Observation{
		ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1",
		CompletionTokens: 50, At: tokenStatsNow.Add(-2 * time.Hour),
	})

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	if strings.Contains(got, "of input from cache") {
		t.Fatalf("report = %q, want the hit rate clause omitted when undefined", got)
	}
	if !strings.Contains(got, "1 requests · 50 output/request") {
		t.Fatalf("report = %q, want a summary line without a hit rate", got)
	}
}

func TestFormatTokenStatsReportHasNoDashCharacters(t *testing.T) {
	rec := newTokenStatsRecorder(t, tokenStatsNow)
	recordTokenStatsFixture(t, rec, tokenStatsNow)

	got := formatTokenStatsReport(rec, tokenStatsNow, 120)
	if strings.ContainsAny(got, "\u2014\u2013") {
		t.Fatalf("report contains an em or en dash: %q", got)
	}
}

func TestExecuteOpenTokenStatsActionSetsTitle(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m.executeOpenTokenStatsAction()

	if got := m.contextOverlay.title; got != "Token usage" {
		t.Fatalf("overlay title = %q, want %q", got, "Token usage")
	}
	if got := m.contextOverlay.content; got != tokenStatsEmptyReport {
		t.Fatalf("overlay content = %q, want %q", got, tokenStatsEmptyReport)
	}
}

func TestTokenStatsOverlayRendersBannerAndTables(t *testing.T) {
	now := time.Now()
	rec := newTokenStatsRecorder(t, now)
	for _, at := range []time.Time{now.Add(-2 * time.Hour), now.AddDate(0, 0, -8)} {
		rec.Record(usagestats.Observation{
			ProviderAlias: "alpha", ProviderType: "openai", BackendModelID: "m1",
			PromptTokens: 1000, CompletionTokens: 200, CacheReadTokens: 500,
			At: at,
		})
	}

	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 140, Height: 120})
	m.recorder = rec
	m.executeOpenTokenStatsAction()

	view := stripANSI(m.renderContextOverlay())
	for _, want := range []string{"Token usage", "tokens", "This week (Mon", "Previous weeks"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overlay view = %q, want %q", view, want)
		}
	}

	m.contextOverlay = m.contextOverlay.scrollToEnd()
	end := stripANSI(m.renderContextOverlay())
	for _, want := range []string{"Last calendar month", "By source (last calendar month)", "alpha"} {
		if !strings.Contains(end, want) {
			t.Fatalf("overlay tail = %q, want %q", end, want)
		}
	}
}

// tokenStatsPrefix returns the part of report before heading.
func tokenStatsPrefix(t *testing.T, report, heading string) string {
	t.Helper()
	end := strings.Index(report, heading)
	if end < 0 {
		t.Fatalf("report has no %q section: %q", heading, report)
	}
	return report[:end]
}

// tokenStatsSection returns the part of report that starts at heading and runs
// to the next heading of the same or a higher level.
func tokenStatsSection(t *testing.T, report, heading string) string {
	t.Helper()
	start := strings.Index(report, heading)
	if start < 0 {
		t.Fatalf("report has no %q section: %q", heading, report)
	}
	rest := report[start+len(heading):]
	if end := strings.Index(rest, "\n### "); end >= 0 {
		return rest[:end]
	}
	return rest
}
