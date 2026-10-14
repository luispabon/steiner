package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/quota"
)

// usageTestNow is the fixed clock for render tests (Wednesday 14 Oct 2026, 12:00 UTC).
var usageTestNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func usageTestFrame(t *testing.T, views ...quota.AccountView) usageFrame {
	t.Helper()
	m := newModel(Config{}, nil)
	if m.styles == nil {
		t.Fatal("newModel left styles unset")
	}
	return usageFrame{views: views, now: usageTestNow, styles: m.styles, width: 80}
}

func usageStrippedLines(f usageFrame) []string {
	lines := usageBodyLines(f)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}
	return out
}

func usageContains(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func TestUsageLevelFor(t *testing.T) {
	tests := []struct {
		pct  float64
		want usageLevel
	}{
		{0, usageLevelNormal},
		{46, usageLevelNormal},
		{74, usageLevelNormal},
		{75, usageLevelWarn},
		{89.9, usageLevelWarn},
		{90, usageLevelError},
		{92, usageLevelError},
	}
	for _, tt := range tests {
		if got := usageLevelFor(tt.pct); got != tt.want {
			t.Errorf("usageLevelFor(%v) = %v, want %v", tt.pct, got, tt.want)
		}
	}
}

func TestUsageBarCells(t *testing.T) {
	tests := []struct {
		pct    float64
		filled int
	}{
		{0, 0}, {46, 11}, {74, 18}, {92, 22}, {100, 24}, {150, 24},
	}
	for _, tt := range tests {
		bar := usageBar(tt.pct)
		if got := strings.Count(bar, "█"); got != tt.filled {
			t.Errorf("usageBar(%v) filled = %d, want %d", tt.pct, got, tt.filled)
		}
		if got := strings.Count(bar, "░"); got != usageBarCells-tt.filled {
			t.Errorf("usageBar(%v) empty = %d, want %d", tt.pct, got, usageBarCells-tt.filled)
		}
	}
}

func TestUsageHeaderActiveMarkerAndRightAlignment(t *testing.T) {
	fetched := usageTestNow.Add(-3 * time.Minute)
	tests := []struct {
		name   string
		active bool
		prefix string
	}{
		{"active", true, "● claude_subscription (work)"},
		{"inactive", false, "  claude_subscription (work)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := usageTestFrame(t, quota.AccountView{
				ProviderType: "claude_subscription",
				DisplayAlias: "work",
				Active:       tt.active,
				Latest:       &quota.Snapshot{Plan: "Max", FetchedAt: fetched},
			})
			header := usageStrippedLines(f)[0]
			if !strings.HasPrefix(header, tt.prefix) {
				t.Errorf("header %q does not start with %q", header, tt.prefix)
			}
			if !strings.HasSuffix(header, "Max · updated 3m ago") {
				t.Errorf("header %q does not end with plan and freshness", header)
			}
			if w := lipgloss.Width(header); w != f.width {
				t.Errorf("header width = %d, want %d", w, f.width)
			}
		})
	}
}

func TestUsageFreshness(t *testing.T) {
	fetched := usageTestNow.Add(-5 * time.Minute)
	tests := []struct {
		name       string
		view       quota.AccountView
		refreshing bool
		want       string
	}{
		{"updated", quota.AccountView{Latest: &quota.Snapshot{FetchedAt: fetched}}, false, "updated 5m ago"},
		{"refreshing overlay", quota.AccountView{Latest: &quota.Snapshot{FetchedAt: fetched}}, true, "refreshing…"},
		{"refreshing account", quota.AccountView{Refreshing: true}, false, "refreshing…"},
		{"error with age", quota.AccountView{Latest: &quota.Snapshot{FetchedAt: fetched}, Err: "401"}, false, "updated 5m ago · unavailable: 401"},
		{"error without snapshot", quota.AccountView{Err: "401"}, false, "unavailable: 401"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := usageTestFrame(t)
			f.refreshing = tt.refreshing
			if got := ansi.Strip(usageFreshness(f, tt.view)); got != tt.want {
				t.Errorf("usageFreshness() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUsageResetTime(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"today", usageTestNow.Add(3*time.Hour + 30*time.Minute), "15:30"},
		{"within seven days", time.Date(2026, 10, 16, 9, 15, 0, 0, time.UTC), "Fri 09:15"},
		{"beyond seven days", time.Date(2026, 10, 30, 9, 15, 0, 0, time.UTC), "30 Oct"},
	}
	for _, tt := range tests {
		if got := usageResetTime(tt.at, usageTestNow); got != tt.want {
			t.Errorf("%s: usageResetTime() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestUsageRelative(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{2*time.Hour + 5*time.Minute, "2h05m"},
		{time.Hour, "1h00m"},
		{96 * time.Hour, "4d"},
		{15 * time.Minute, "15m"},
		{30 * time.Second, "<1m"},
	}
	for _, tt := range tests {
		if got := usageRelative(tt.d); got != tt.want {
			t.Errorf("usageRelative(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestUsageWindowLineMeterAndReset(t *testing.T) {
	f := usageTestFrame(t, quota.AccountView{
		ProviderType: "claude_subscription",
		DisplayAlias: "work",
		Latest: &quota.Snapshot{Windows: []quota.Window{
			{Label: "5h", UsedPercent: 74, ResetsAt: usageTestNow.Add(2*time.Hour + 5*time.Minute)},
		}},
	})
	lines := usageStrippedLines(f)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lines)
	}
	line := lines[1]
	if !strings.Contains(line, strings.Repeat("█", 18)+strings.Repeat("░", 6)) {
		t.Errorf("window bar wrong in %q", line)
	}
	for _, want := range []string{"5h", " 74%", "resets 14:05 (2h05m)"} {
		if !strings.Contains(line, want) {
			t.Errorf("window line %q missing %q", line, want)
		}
	}
}

func TestUsageTrendLines(t *testing.T) {
	emptyAt := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		p    quota.Projection
		want string
	}{
		{"empty before reset", quota.Projection{OK: true, BeforeReset: true, EmptyAt: emptyAt}, "    trend: empty by 15:00 (⚠ before reset)"},
		{"lasts past reset", quota.Projection{OK: true}, "    trend: lasts past reset ✓"},
		{"no projection", quota.Projection{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := usageTestFrame(t, quota.AccountView{
				DisplayAlias:      "work",
				Latest:            &quota.Snapshot{Windows: []quota.Window{{Label: "5h", UsedPercent: 46, ResetsAt: usageTestNow.Add(2 * time.Hour)}}},
				WindowProjections: []quota.Projection{tt.p},
			})
			lines := usageStrippedLines(f)
			if tt.want == "" {
				if usageContains(lines, "trend:") {
					t.Errorf("unexpected trend line in %q", lines)
				}
				return
			}
			if !usageContains(lines, tt.want) {
				t.Errorf("lines %q missing %q", lines, tt.want)
			}
		})
	}
}

func TestUsageBudgetLine(t *testing.T) {
	limit := 50.0
	f := usageTestFrame(t, quota.AccountView{
		DisplayAlias: "work",
		Latest: &quota.Snapshot{Budgets: []quota.Budget{
			{Label: "month", Used: 25, Limit: &limit, Period: "monthly"},
		}},
	})
	lines := usageStrippedLines(f)
	line := lines[1]
	for _, want := range []string{strings.Repeat("█", 12) + strings.Repeat("░", 12), " 50%", "monthly"} {
		if !strings.Contains(line, want) {
			t.Errorf("budget line %q missing %q", line, want)
		}
	}
}

func TestUsageSpendAndCreditsRows(t *testing.T) {
	f := usageTestFrame(t, quota.AccountView{
		DisplayAlias: "work",
		Latest: &quota.Snapshot{
			Budgets: []quota.Budget{
				{Label: "today", Used: 0.4},
				{Label: "week", Used: 3.1},
				{Label: "month", Used: 12.5},
			},
			Credits: "$9.99",
		},
	})
	lines := usageStrippedLines(f)
	want := []string{
		"  spend        today $0.40 · week $3.10 · month $12.50",
		"  credits      $9.99",
	}
	if len(lines) != 3 || lines[1] != want[0] || lines[2] != want[1] {
		t.Errorf("got %q, want spend and credits rows %q", lines, want)
	}
}

func TestUsageStaticUnavailableRow(t *testing.T) {
	const reason = "API key mode: no subscription usage"
	f := usageTestFrame(t, quota.AccountView{
		ProviderType: "openai",
		DisplayAlias: "api",
		Unavailable:  reason,
	})
	lines := usageStrippedLines(f)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
	}
	if !strings.HasSuffix(lines[0], reason) {
		t.Errorf("row %q does not end with %q", lines[0], reason)
	}
}

func TestUsageErrorRows(t *testing.T) {
	f := usageTestFrame(t, quota.AccountView{
		ProviderType: "claude_subscription",
		DisplayAlias: "work",
		Err:          "401 unauthorized",
	})
	lines := usageStrippedLines(f)
	if len(lines) != 1 || !strings.HasSuffix(lines[0], "unavailable: 401 unauthorized") {
		t.Errorf("error row = %q, want one line ending with unavailable error", lines)
	}
}

func TestUsageEmptyState(t *testing.T) {
	f := usageTestFrame(t)
	lines := usageStrippedLines(f)
	if len(lines) != 1 || lines[0] != "No configured providers report subscription usage." {
		t.Errorf("empty state = %q", lines)
	}
}

func TestUsageRenderIsPure(t *testing.T) {
	limit := 10.0
	f := usageTestFrame(t, quota.AccountView{
		ProviderType: "claude_subscription",
		DisplayAlias: "work",
		Active:       true,
		Latest: &quota.Snapshot{
			Plan:      "Max",
			FetchedAt: usageTestNow.Add(-time.Minute),
			Windows:   []quota.Window{{Label: "daily", UsedPercent: 92, ResetsAt: usageTestNow.Add(time.Hour)}},
			Budgets:   []quota.Budget{{Label: "month", Used: 1, Limit: &limit}},
		},
	})
	first := usageBodyLines(f)
	second := usageBodyLines(f)
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Error("two renders of the same frame differ")
	}
}
