package tui

import (
	"testing"
	"time"
)

// TestSessionStartDisplayRelativeLocalDates pins the sidebar's relative start
// label: bare clock time today, clock time plus "yesterday" on the preceding
// local calendar day, and the full date before that.
func TestSessionStartDisplayRelativeLocalDates(t *testing.T) {
	t.Parallel()
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, 11, day, hour, minute, 0, 0, time.Local)
	}

	cases := []struct {
		name    string
		started time.Time
		now     time.Time
		want    string
	}{
		{"not started", time.Time{}, at(4, 9, 30), ""},
		{"start today", at(4, 9, 30), at(4, 9, 31), "09:30"},
		{"start today, under 24h elapsed", at(4, 0, 10), at(4, 23, 50), "00:10"},
		{"start yesterday", at(3, 9, 30), at(4, 9, 30), "09:30 yesterday"},
		{"start yesterday, over 24h elapsed", at(3, 22, 0), at(4, 23, 30), "22:00 yesterday"},
		{"start at midnight today", at(4, 0, 0), at(4, 13, 5), "00:00"},
		{"start just before midnight yesterday", at(3, 23, 59), at(4, 0, 0), "23:59 yesterday"},
		{"start two days back", at(2, 9, 30), at(4, 9, 30), "2026-11-02 09:30"},
		{"yesterday across the year boundary",
			time.Date(2025, 12, 31, 23, 50, 0, 0, time.Local),
			time.Date(2026, 1, 1, 0, 5, 0, 0, time.Local), "23:50 yesterday"},
		{"older across the year boundary",
			time.Date(2025, 12, 30, 9, 30, 0, 0, time.Local),
			time.Date(2026, 1, 1, 0, 5, 0, 0, time.Local), "2025-12-30 09:30"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sessionStartDisplay(tc.started, tc.now); got != tc.want {
				t.Errorf("sessionStartDisplay(%v, %v) = %q, want %q", tc.started, tc.now, got, tc.want)
			}
		})
	}
}

// TestSessionStartDisplayAcrossDST covers local calendar days whose length is
// not 24 hours: the fall-back day makes yesterday 25 hours before now and the
// spring-forward day makes it 23, so elapsed-time arithmetic would mislabel
// them.
func TestSessionStartDisplayAcrossDST(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata for America/New_York is unavailable: %v", err)
	}

	cases := []struct {
		name    string
		started time.Time
		now     time.Time
		want    string
	}{
		{"fall back, 25h elapsed is yesterday",
			time.Date(2026, 10, 31, 22, 30, 0, 0, loc),
			time.Date(2026, 11, 1, 22, 30, 0, 0, loc), "22:30 yesterday"},
		{"fall back, same day across the repeated hour",
			time.Date(2026, 11, 1, 1, 30, 0, 0, loc),
			time.Date(2026, 11, 1, 23, 0, 0, 0, loc), "01:30"},
		{"spring forward, 23h elapsed is yesterday",
			time.Date(2026, 3, 7, 23, 30, 0, 0, loc),
			time.Date(2026, 3, 8, 23, 30, 0, 0, loc), "23:30 yesterday"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if elapsed := tc.now.Sub(tc.started); elapsed == 24*time.Hour {
				t.Fatalf("fixture elapsed time = %v, want a non-24h day", elapsed)
			}
			if got := sessionStartDisplay(tc.started, tc.now); got != tc.want {
				t.Errorf("sessionStartDisplay(%v, %v) = %q, want %q", tc.started, tc.now, got, tc.want)
			}
		})
	}
}

// TestSyncSidebarRefreshesStartLabelAcrossMidnight proves the idle session
// ticker relabels yesterday's start without a new prompt: syncSidebarAt reruns
// the formatter against the current instant.
func TestSyncSidebarRefreshesStartLabelAcrossMidnight(t *testing.T) {
	t.Parallel()
	m := &Model{}
	m.startSessionClock(time.Date(2026, 11, 3, 23, 55, 0, 0, time.Local))

	m.syncSidebarAt(time.Date(2026, 11, 3, 23, 59, 30, 0, time.Local))
	if got := m.sidebar.startedLocal; got != "23:55" {
		t.Fatalf("sidebar.startedLocal before midnight = %q, want %q", got, "23:55")
	}

	m.syncSidebarAt(time.Date(2026, 11, 4, 0, 0, 30, 0, time.Local))
	if got := m.sidebar.startedLocal; got != "23:55 yesterday" {
		t.Errorf("sidebar.startedLocal after midnight = %q, want %q", got, "23:55 yesterday")
	}
	if !m.sidebar.runtimeActive {
		t.Error("sidebar.runtimeActive = false after midnight, want the session still active")
	}
}

// TestSessionTickRefreshesStartLabelWhileIdle proves a session tick recomputes
// the start label even when nothing else changed, so the label rolls over at
// local midnight while the session sits idle.
func TestSessionTickRefreshesStartLabelWhileIdle(t *testing.T) {
	t.Parallel()
	m := &Model{}
	// 48 hours back always lands at least two local calendar days earlier, so
	// the label takes the absolute date form regardless of when the test runs.
	start := time.Now().Add(-48 * time.Hour)
	m.startSessionClock(start)
	m.sidebar.startedLocal = "stale"

	if _, cmd := m.handleSessionTickMsg(sessionTickMsg{gen: m.sessionTickGen}); cmd == nil {
		t.Fatal("the session timer did not re-arm on an idle tick")
	}
	want := start.Local().Format(sessionStartLayout)
	if got := m.sidebar.startedLocal; got != want {
		t.Errorf("sidebar.startedLocal after idle tick = %q, want %q", got, want)
	}
}
