package prompt

import (
	"fmt"
	"time"
)

// SessionDate is the local calendar date captured once when a session starts.
type SessionDate struct{ t time.Time }

// NewSessionDate captures t as the session date. Callers pass time.Now() once
// per session identity; the value must not be re-read per run or turn.
func NewSessionDate(t time.Time) SessionDate {
	return SessionDate{t: t}
}

// IsZero reports whether no date was captured.
func (d SessionDate) IsZero() bool {
	return d.t.IsZero()
}

// render returns the user-facing date line, e.g.
// "Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started."
func (d SessionDate) render() string {
	return fmt.Sprintf(
		"Current date: %s (%s, UTC%s), recorded when this session started.",
		d.t.Format("2006-01-02"),
		d.t.Format("MST"),
		d.t.Format("-07:00"),
	)
}
