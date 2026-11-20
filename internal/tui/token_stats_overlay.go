package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/usagestats"
)

const (
	// tokenStatsRetentionDays is the store's retention: the weekly strip can
	// reach no further back.
	tokenStatsRetentionDays = usagestats.RetentionDays
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
// and the per-source split. now pins the calendar arithmetic. width is the
// markdown width the overlay renders the report at: it bounds the banner and
// decides which table columns fit.
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

	sessionTable := tokenStatsWindowTable(session)
	windows := tokenStatsWindows(rec, monthFrom)
	tables := make([]tokenStatsTable, 0, len(windows)+2)
	if len(session.Rows) > 0 {
		tables = append(tables, sessionTable)
	}
	windowTables := make([]tokenStatsTable, len(windows))
	for i, window := range windows {
		windowTables[i] = tokenStatsWindowTable(window.report)
		if len(window.report.Rows) > 0 {
			tables = append(tables, windowTables[i])
		}
	}
	sourceTable, hasSource := tokenStatsSourceTable(rec, monthFrom)
	if hasSource {
		tables = append(tables, sourceTable)
	}
	plan := planTokenStatsTables(tables, tokenStatsTableLimit(width))

	var sb strings.Builder
	writeTokenHeadline(&sb, month, monthFrom, now, width)
	writeThisWeekSection(&sb, rec, now)
	writePreviousWeeksSection(&sb, rec, now)
	writeWindowSection(&sb, "This session", session, sessionTable, plan)
	for i, window := range windows {
		writeWindowSection(&sb, window.label, window.report, windowTables[i], plan)
	}
	if hasSource {
		writeBySourceSection(&sb, sourceTable, plan)
	}
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
	m.contextOverlay = openContextOverlay("Token usage", tokenStatsEmptyReport, m.width, m.height, m.styles, m.content.glamourStyleSheet)
	// The table columns depend on the report width, so the overlay rebuilds the
	// report on every reflow, including a terminal resize.
	m.contextOverlay.contentForWidth = func(width int) string {
		return formatTokenStatsReport(m.recorder, time.Now(), width)
	}
	m.contextOverlay = m.contextOverlay.reflow()
	m.input.Reset()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}
