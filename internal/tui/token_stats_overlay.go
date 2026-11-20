package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/usagestats"
)

const (
	// tokenStatsRetentionDays mirrors the usagestats store's unexported
	// retentionDays: the store keeps 33 days of hourly buckets, so the weekly
	// strip can reach no further back.
	tokenStatsRetentionDays = 33
	// tokenStatsBannerMargin is the free horizontal room the banner needs
	// beyond its own width before the plain-line fallback takes over.
	tokenStatsBannerMargin = 8
	// tokenStatsBarWidth is the bar width used by the daily and weekly strips.
	tokenStatsBarWidth = 20
	// tokenStatsEmptyReport replaces the whole report when nothing is recorded.
	tokenStatsEmptyReport = "No token usage recorded yet."
	// tokenStatsEmptyWindow labels a window with no calls.
	tokenStatsEmptyWindow = "_No calls in this window._"
)

// tokenStatsWindow pairs a section label with its aggregated report.
type tokenStatsWindow struct {
	label  string
	report usagestats.Report
}

// formatTokenStatsReport renders the token usage dashboard as markdown: a
// headline month total, daily and weekly strips, one table per fixed window,
// and the per-source split. now pins the calendar arithmetic and width is the
// overlay width available for the banner.
func formatTokenStatsReport(rec *usagestats.Recorder, now time.Time, width int) string {
	if rec == nil {
		return tokenStatsEmptyReport
	}
	now = now.UTC()
	monthFrom := usagestats.MonthAgo(now)
	month := rec.WindowSince(monthFrom)
	session := rec.SessionWindow()
	if month.Total.Requests == 0 && session.Total.Requests == 0 {
		return tokenStatsEmptyReport
	}

	var sb strings.Builder
	writeTokenHeadline(&sb, month, monthFrom, now, width)
	writeThisWeekSection(&sb, rec, now)
	writePreviousWeeksSection(&sb, rec, now)
	writeWindowSection(&sb, "This session", session)
	for _, window := range tokenStatsWindows(rec, monthFrom) {
		writeWindowSection(&sb, window.label, window.report)
	}
	writeBySourceSection(&sb, rec, monthFrom)
	return strings.TrimRight(sb.String(), "\n")
}

// tokenStatsWindows returns the fixed windows that follow the session section,
// in display order.
func tokenStatsWindows(rec *usagestats.Recorder, monthFrom time.Time) []tokenStatsWindow {
	return []tokenStatsWindow{
		{label: "Last hour", report: rec.Window(time.Hour)},
		{label: "Last 24 hours", report: rec.Window(24 * time.Hour)},
		{label: "Last 7 days", report: rec.Window(7 * 24 * time.Hour)},
		{label: "Last calendar month", report: rec.WindowSince(monthFrom)},
	}
}

// executeOpenTokenStatsAction opens the token usage dashboard overlay.
func (m *Model) executeOpenTokenStatsAction() (tea.Model, tea.Cmd) {
	content := formatTokenStatsReport(m.recorder, time.Now(), m.width)
	m.contextOverlay = openContextOverlay("Token usage", content, m.width, m.height, m.styles, m.content.glamourStyleSheet)
	m.input.Reset()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}
