package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/diagnostics"
)

const (
	// devLogCapacity bounds the ring. The oldest entry is dropped when it is full.
	devLogCapacity = 500
	// devLogMaxMessageBytes bounds one stored message, truncation suffix included.
	devLogMaxMessageBytes = 8 * 1024
	devLogTruncateSuffix  = "...[truncated]"
)

// devLogSeverity ranks an entry. Higher values need more attention. The zero
// value means no severity and is not a valid entry severity.
type devLogSeverity int

const (
	devLogWarning devLogSeverity = iota + 1
	devLogError
)

// String returns the severity label shown in the overlay and written to
// diagnostics.
func (s devLogSeverity) String() string {
	switch s {
	case devLogWarning:
		return "warning"
	case devLogError:
		return "error"
	default:
		return "unknown"
	}
}

// DiagnosticsSink receives developer error log entries for the internal
// diagnostics stream. *diagnostics.Writer satisfies it. A nil sink disables the
// stream.
type DiagnosticsSink interface {
	Enabled(kind diagnostics.Kind) bool
	Write(rec diagnostics.Record)
}

// devLogParams describes one developer error log entry.
type devLogParams struct {
	Severity  devLogSeverity
	Source    string
	AgentID   string
	AgentType string
	CallID    string
	EventType string
	Message   string
}

// devLogEntry is a recorded devLogParams with its capture time. Its Message is
// already sanitized and capped.
type devLogEntry struct {
	at time.Time
	devLogParams
}

// devLogPayload is the internal diagnostics payload for one entry.
type devLogPayload struct {
	Severity  string `json:"severity"`
	Source    string `json:"source,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	EventType string `json:"event_type,omitempty"`
	Message   string `json:"message"`
}

// devLogState holds the ring and badge bookkeeping behind the error log.
type devLogState struct {
	entries []devLogEntry
	// unseen counts entries recorded since the overlay last opened. It is
	// capped at len(entries).
	unseen int
	// replaying is true between replay started and replay finished events.
	// Entries recorded in that window are dropped.
	replaying bool
	sink      DiagnosticsSink
}

// recordDevLog appends an entry to the ring, updates the unseen badge state and
// writes the entry to the internal diagnostics stream when it is enabled. While
// the overlay is open, entries count as seen.
func (m *Model) recordDevLog(p devLogParams) {
	if m.devLog.replaying {
		return
	}
	e := devLogEntry{at: time.Now(), devLogParams: p}
	e.Message = capDevLogMessage(sanitizeDevLogMessage(p.Message))
	m.devLog.push(e)
	if m.devLogOverlay.IsOpen() {
		m.devLog.unseen = 0
	} else {
		m.devLog.unseen = min(m.devLog.unseen+1, len(m.devLog.entries))
	}
	m.syncDevLogStatus()
	m.writeDevLogDiagnostics(e)
}

// push appends e and drops the oldest entry when the ring is full.
func (s *devLogState) push(e devLogEntry) {
	if len(s.entries) == devLogCapacity {
		copy(s.entries, s.entries[1:])
		s.entries = s.entries[:devLogCapacity-1]
	}
	s.entries = append(s.entries, e)
}

// worstUnseen returns the highest severity among the unseen entries, or zero
// when none are unseen.
func (s devLogState) worstUnseen() devLogSeverity {
	var worst devLogSeverity
	for _, e := range s.entries[len(s.entries)-s.unseen:] {
		worst = max(worst, e.Severity)
	}
	return worst
}

// syncDevLogStatus copies the unseen count and worst severity into the status
// bar state.
func (m *Model) syncDevLogStatus() {
	m.status.devLogUnseen = m.devLog.unseen
	m.status.devLogWorst = m.devLog.worstUnseen()
}

// writeDevLogDiagnostics writes e to the internal stream when a sink is set and
// the stream is enabled. Writer failures are not reported: the diagnostics
// writer logs them itself.
func (m *Model) writeDevLogDiagnostics(e devLogEntry) {
	sink := m.devLog.sink
	if sink == nil || !sink.Enabled(diagnostics.KindInternal) {
		return
	}
	sink.Write(diagnostics.Record{
		Timestamp: e.at,
		Kind:      diagnostics.KindInternal,
		AgentID:   e.AgentID,
		Payload: devLogPayload{
			Severity:  e.Severity.String(),
			Source:    e.Source,
			AgentType: e.AgentType,
			CallID:    e.CallID,
			EventType: e.EventType,
			Message:   e.Message,
		},
	})
}

// devLogBadge renders the status bar badge for unseen entries: a warning or
// error icon with the unseen count, in the severity colour. It is empty when
// nothing is unseen.
func (s statusState) devLogBadge() string {
	if s.devLogUnseen == 0 {
		return ""
	}
	style, icon := s.styles.WarningStyle, "⚠"
	if s.devLogWorst == devLogError {
		style, icon = s.styles.ErrorStyle, "✗"
	}
	return style.Render(fmt.Sprintf("%s %d", icon, s.devLogUnseen))
}

// sanitizeDevLogMessage removes ANSI escape sequences and control characters
// other than newline and tab, so stored messages cannot drive the terminal.
func sanitizeDevLogMessage(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return -1
		}
		return r
	}, ansi.Strip(s))
}

// capDevLogMessage truncates s to devLogMaxMessageBytes, suffix included, on a
// rune boundary.
func capDevLogMessage(s string) string {
	if len(s) <= devLogMaxMessageBytes {
		return s
	}
	cut := devLogMaxMessageBytes - len(devLogTruncateSuffix)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + devLogTruncateSuffix
}
