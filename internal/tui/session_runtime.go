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

// sessionStartLayout is how the sidebar shows the session start time.
const sessionStartLayout = "2006-01-02 15:04"

// sessionStartDisplay formats a session start time in local time for the
// sidebar, returning "" when the session has not started.
func sessionStartDisplay(started time.Time) string {
	if started.IsZero() {
		return ""
	}
	return started.Local().Format(sessionStartLayout)
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
