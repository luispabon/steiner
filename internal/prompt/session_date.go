package prompt

import (
	"fmt"
	"time"
)

// SessionDate is the local calendar date captured once when a session starts.
// It keeps the rendered components rather than the instant, so a resumed
// session reproduces the same line, including the zone abbreviation, even on a
// later day or in another zone.
type SessionDate struct {
	date   string // YYYY-MM-DD
	zone   string // zone abbreviation as rendered, e.g. BST
	offset string // numeric offset as rendered, e.g. +01:00
}

// NewSessionDate captures t as the session date. Callers pass time.Now() once
// per session identity; the value must not be re-read per run or turn.
func NewSessionDate(t time.Time) SessionDate {
	return SessionDate{
		date:   t.Format("2006-01-02"),
		zone:   t.Format("MST"),
		offset: t.Format("-07:00"),
	}
}

// NewSessionDateFromParts restores a date from the parts returned by Parts. It
// reports false when they are not a valid date, zone, and offset, so a corrupt
// stored record falls back to capturing a new date.
func NewSessionDateFromParts(date, zone, offset string) (SessionDate, bool) {
	d := SessionDate{date: date, zone: zone, offset: offset}
	if !d.valid() {
		return SessionDate{}, false
	}
	return d, true
}

// Parts returns the date, zone abbreviation, and numeric offset that make up
// the rendered line. They round-trip through NewSessionDateFromParts.
func (d SessionDate) Parts() (date, zone, offset string) {
	return d.date, d.zone, d.offset
}

// IsZero reports whether no date was captured.
func (d SessionDate) IsZero() bool {
	return d.date == ""
}

func (d SessionDate) valid() bool {
	if d.zone == "" {
		return false
	}
	if _, err := time.Parse("2006-01-02", d.date); err != nil {
		return false
	}
	_, err := time.Parse("-07:00", d.offset)
	return err == nil
}

// render returns the user-facing date line, e.g.
// "Current date: 2026-09-13 (BST, UTC+01:00), recorded when this session started."
func (d SessionDate) render() string {
	return fmt.Sprintf(
		"Current date: %s (%s, UTC%s), recorded when this session started.",
		d.date,
		d.zone,
		d.offset,
	)
}
