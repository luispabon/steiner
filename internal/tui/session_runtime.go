package tui

import "time"

// sessionWorkActive reports whether the sidebar runtime should advance. Four
// independent signals are OR-ed:
//
//   - rootTurnActive, the root turn latch. A submit or a run start sets it and a
//     run finish, a root stop reason or a non-generating driver state clears it.
//     It is a first-class source, not just a fallback: it starts the runtime at
//     a prompt and covers a compaction run while the driver stays idle. It is
//     also the only source when no driver state has been seen.
//   - compactionRunActive, the compaction run latch. A root active compaction
//     diagnostic sets it and a run finish, a root stop reason or a reset clears
//     it. It outlives the compaction display state, which a terminal diagnostic
//     can clear before the run's finish event arrives.
//   - the driver's generating state, authoritative while state is known. It
//     spans chained runs, where a stop reason or run finished event can arrive
//     between runs without an intervening state change.
//   - queued or running roster entries, fed by direct delegation lifecycle
//     events, so grouped and held completions clear them even while the driver
//     still reports a wait. Entries recreated from saved history do not count.
func (m *Model) sessionWorkActive() bool {
	return m.rootTurnActive || m.compactionRunActive || m.driverGenerating() || m.roster.activeCount() > 0
}

// evaluateSessionWork advances the runtime accounting at the current time. It
// runs after roster observations, conversation state transitions and root turn
// transitions, so a span opens or closes with the event that caused it.
func (m *Model) evaluateSessionWork() {
	m.syncSessionClockAt(time.Now())
}

// sessionStartLayout is how the sidebar shows a session start from an earlier
// local calendar day.
const sessionStartLayout = "2006-01-02 15:04"

// sessionStartClockLayout is how the sidebar shows a session start from today.
const sessionStartClockLayout = "15:04"

// sessionStartDisplay formats a session start time in local time for the
// sidebar, relative to now: "HH:MM today" on the same local calendar day,
// "HH:MM yesterday" on the preceding one, and "YYYY-MM-DD HH:MM" before that.
// It returns "" when the session has not started. now carries the reference
// zone; callers pass time.Now(), so that is the machine's local zone. The day
// comparison uses calendar dates via AddDate, not elapsed time, so it stays
// correct across DST shifts and year boundaries.
func sessionStartDisplay(started, now time.Time) string {
	if started.IsZero() {
		return ""
	}
	local := started.In(now.Location())
	if sameLocalDate(local, now) {
		return local.Format(sessionStartClockLayout) + " today"
	}
	if sameLocalDate(local, now.AddDate(0, 0, -1)) {
		return local.Format(sessionStartClockLayout) + " yesterday"
	}
	return local.Format(sessionStartLayout)
}

// sameLocalDate reports whether a and b fall on the same local calendar day.
func sameLocalDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// startSessionClock starts the session clock at the current session's first
// prompt. It records the prompt's wall clock time for the start row and opens
// an active runtime span, so runtime counts from the prompt onward.
func (m *Model) startSessionClock(now time.Time) {
	m.sessionStartedAt = &now
	m.sessionRuntime = 0
	since := now
	m.sessionActiveSince = &since
}

// resetSessionClock clears the session clock for a clear, fork or resume. The
// generation bump makes any armed session timer chain stop instead of
// re-arming against the next session.
func (m *Model) resetSessionClock() {
	m.sessionStartedAt = nil
	m.sessionRuntime = 0
	m.sessionActiveSince = nil
	m.rootTurnActive = false
	m.compactionRunActive = false
	m.sessionTickGen++
}

// syncSessionClockAt advances the runtime accounting at now. It opens an
// active span when work starts and folds an open span into the accumulated
// runtime when work stops. Overlapping parent and sub-agent work shares one
// span, so the union counts once.
func (m *Model) syncSessionClockAt(now time.Time) {
	if m.sessionStartedAt == nil {
		return
	}
	if m.sessionWorkActive() {
		if m.sessionActiveSince == nil {
			since := now
			m.sessionActiveSince = &since
		}
		return
	}
	if m.sessionActiveSince != nil {
		if elapsed := now.Sub(*m.sessionActiveSince); elapsed > 0 {
			m.sessionRuntime += elapsed
		}
		m.sessionActiveSince = nil
	}
}

// sessionRuntimeAt returns the runtime accumulated through now, including an
// open active span.
func (m *Model) sessionRuntimeAt(now time.Time) time.Duration {
	total := m.sessionRuntime
	if m.sessionActiveSince != nil {
		if elapsed := now.Sub(*m.sessionActiveSince); elapsed > 0 {
			total += elapsed
		}
	}
	return total
}
