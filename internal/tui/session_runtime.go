package tui

import "time"

// sessionWorkActive reports whether the sidebar runtime should advance: a root
// turn is in progress, or the roster holds queued or running sub-agents. The
// root turn is tracked by its actual lifecycle (submit plus driver generating
// state, ended by a non-generating state or a root stop), and roster status
// comes from direct delegation lifecycle events, so grouped and held completions
// clear it even while the driver still reports a wait.
func (m *Model) sessionWorkActive() bool {
	if m.rootTurnActive {
		return true
	}
	return m.roster.activeCount() > 0
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
