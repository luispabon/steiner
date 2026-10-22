package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/output"
)

// fakeDevLogSink records every write. It reports the internal stream as
// enabled only when enabled is true.
type fakeDevLogSink struct {
	enabled bool
	records []diagnostics.Record
}

func (f *fakeDevLogSink) Enabled(kind diagnostics.Kind) bool {
	return f.enabled && kind == diagnostics.KindInternal
}

func (f *fakeDevLogSink) Write(rec diagnostics.Record) {
	f.records = append(f.records, rec)
}

func recordTestDevLog(m *Model, n int, severity devLogSeverity) {
	for i := range n {
		m.recordDevLog(devLogParams{Severity: severity, Message: fmt.Sprintf("entry-%d", i)})
	}
}

func TestDevLogRingDropsOldestEntry(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	for i := range devLogCapacity + 3 {
		m.recordDevLog(devLogParams{Severity: devLogError, Message: fmt.Sprintf("m%d", i)})
	}
	entries := m.devLog.entries
	if len(entries) != devLogCapacity {
		t.Fatalf("len = %d, want %d", len(entries), devLogCapacity)
	}
	if entries[0].Message != "m3" {
		t.Errorf("oldest kept = %q, want m3", entries[0].Message)
	}
	if last := entries[len(entries)-1].Message; last != "m502" {
		t.Errorf("newest = %q, want m502", last)
	}
}

func TestSanitizeDevLogMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "csi colour removed", in: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "osc title removed", in: "\x1b]0;title\x07text", want: "text"},
		{name: "newline and tab kept", in: "a\tb\nc", want: "a\tb\nc"},
		{name: "bell and carriage return dropped", in: "x\x07\ry", want: "xy"},
		{name: "del and c1 dropped", in: "a\x7f\u009bb", want: "ab"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeDevLogMessage(tc.in); got != tc.want {
				t.Errorf("sanitizeDevLogMessage(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCapDevLogMessage(t *testing.T) {
	t.Parallel()
	t.Run("short message unchanged", func(t *testing.T) {
		t.Parallel()
		if got := capDevLogMessage("short"); got != "short" {
			t.Errorf("capDevLogMessage = %q, want short", got)
		}
	})
	t.Run("long ascii capped including suffix", func(t *testing.T) {
		t.Parallel()
		got := capDevLogMessage(strings.Repeat("a", 9000))
		if len(got) != devLogMaxMessageBytes {
			t.Errorf("len = %d, want %d", len(got), devLogMaxMessageBytes)
		}
		if !strings.HasSuffix(got, devLogTruncateSuffix) {
			t.Errorf("missing suffix: %q", got[len(got)-20:])
		}
	})
	t.Run("multibyte rune at cut stays valid", func(t *testing.T) {
		t.Parallel()
		// Two-byte runes put a continuation byte at the naive cut point.
		got := capDevLogMessage(strings.Repeat("\u00e9", 5000))
		if !utf8.ValidString(got) {
			t.Errorf("capped message is not valid UTF-8")
		}
		if len(got) > devLogMaxMessageBytes {
			t.Errorf("len = %d, want <= %d", len(got), devLogMaxMessageBytes)
		}
		if !strings.HasSuffix(got, devLogTruncateSuffix) {
			t.Errorf("missing suffix")
		}
	})
}

func TestRecordDevLogUnseenCounting(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)

	m.recordDevLog(devLogParams{Severity: devLogWarning, Message: "w"})
	if m.status.devLogUnseen != 1 || m.status.devLogWorst != devLogWarning {
		t.Fatalf("after warning: unseen=%d worst=%v, want 1 warning", m.status.devLogUnseen, m.status.devLogWorst)
	}

	m.recordDevLog(devLogParams{Severity: devLogError, Message: "e"})
	if m.status.devLogUnseen != 2 || m.status.devLogWorst != devLogError {
		t.Errorf("after error: unseen=%d worst=%v, want 2 error", m.status.devLogUnseen, m.status.devLogWorst)
	}

	m.openDevLogOverlay()
	if m.devLog.unseen != 0 || m.status.devLogUnseen != 0 {
		t.Errorf("after open: unseen=%d status=%d, want 0", m.devLog.unseen, m.status.devLogUnseen)
	}

	m.recordDevLog(devLogParams{Severity: devLogError, Message: "while open"})
	if m.devLog.unseen != 0 {
		t.Errorf("entry recorded while open: unseen=%d, want 0", m.devLog.unseen)
	}
}

func TestRecordDevLogWorstUnseenIgnoresSeenEntries(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.recordDevLog(devLogParams{Severity: devLogError, Message: "seen later"})
	m.openDevLogOverlay()
	m.devLogOverlay = m.devLogOverlay.Close()
	m.recordDevLog(devLogParams{Severity: devLogWarning, Message: "new"})
	if m.status.devLogUnseen != 1 || m.status.devLogWorst != devLogWarning {
		t.Errorf("unseen=%d worst=%v, want 1 warning; the seen error must not raise the badge", m.status.devLogUnseen, m.status.devLogWorst)
	}
}

func TestRecordDevLogUnseenCappedAtRingSize(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	recordTestDevLog(m, devLogCapacity+10, devLogWarning)
	if m.devLog.unseen != devLogCapacity {
		t.Errorf("unseen = %d, want %d", m.devLog.unseen, devLogCapacity)
	}
}

func TestRecordDevLogDroppedDuringReplay(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.applyEvent(output.Event{Type: output.EventTypeReplayStarted})
	m.recordDevLog(devLogParams{Severity: devLogError, Message: "replayed"})
	if n := len(m.devLog.entries); n != 0 {
		t.Fatalf("entries during replay = %d, want 0", n)
	}

	m.applyEvent(output.Event{Type: output.EventTypeReplayFinished})
	m.recordDevLog(devLogParams{Severity: devLogError, Message: "live"})
	if n := len(m.devLog.entries); n != 1 {
		t.Errorf("entries after replay = %d, want 1", n)
	}
}

func TestRecordDevLogDiagnosticsSink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		sink    *fakeDevLogSink
		wantLen int
	}{
		{name: "enabled stream writes", sink: &fakeDevLogSink{enabled: true}, wantLen: 1},
		{name: "disabled stream skips", sink: &fakeDevLogSink{enabled: false}, wantLen: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{Diagnostics: tc.sink}, nil)
			m.recordDevLog(devLogParams{
				Severity:  devLogError,
				Source:    "provider",
				AgentID:   "agent-1",
				AgentType: "implement",
				CallID:    "call-9",
				EventType: "model_call",
				Message:   "boom \x1b[31mred\x1b[0m",
			})
			if len(tc.sink.records) != tc.wantLen {
				t.Fatalf("records = %d, want %d", len(tc.sink.records), tc.wantLen)
			}
			if tc.wantLen == 0 {
				return
			}
			rec := tc.sink.records[0]
			if rec.Kind != diagnostics.KindInternal || rec.AgentID != "agent-1" || rec.Timestamp.IsZero() {
				t.Errorf("record kind=%q agent=%q ts zero=%v, want internal agent-1 with timestamp", rec.Kind, rec.AgentID, rec.Timestamp.IsZero())
			}
			payload, ok := rec.Payload.(devLogPayload)
			if !ok {
				t.Fatalf("payload type = %T, want devLogPayload", rec.Payload)
			}
			want := devLogPayload{
				Severity:  "error",
				Source:    "provider",
				AgentType: "implement",
				CallID:    "call-9",
				EventType: "model_call",
				Message:   "boom red",
			}
			if payload != want {
				t.Errorf("payload = %+v, want %+v", payload, want)
			}
		})
	}
}

func TestRecordDevLogNilSinkDoesNotPanic(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.recordDevLog(devLogParams{Severity: devLogWarning, Message: "no sink"})
	if n := len(m.devLog.entries); n != 1 {
		t.Errorf("entries = %d, want 1", n)
	}
}

func TestDevLogBadgeTextAndColour(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	tests := []struct {
		name   string
		unseen int
		worst  devLogSeverity
		want   string
	}{
		{name: "nothing unseen hides badge", unseen: 0, worst: 0, want: ""},
		{name: "warning uses warning style", unseen: 2, worst: devLogWarning, want: m.styles.WarningStyle.Render("\u26a0 2")},
		{name: "error uses error style", unseen: 1, worst: devLogError, want: m.styles.ErrorStyle.Render("\u2717 1")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := m.status
			s.devLogUnseen = tc.unseen
			s.devLogWorst = tc.worst
			if got := s.devLogBadge(); got != tc.want {
				t.Errorf("devLogBadge() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusBarShowsAndClearsDevLogBadge(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})

	m.recordDevLog(devLogParams{Severity: devLogError, Message: "fail"})
	if got := stripANSI(m.renderStatus(120)); !strings.Contains(got, "\u2717 1") {
		t.Fatalf("status bar missing error badge: %q", got)
	}

	m.openDevLogOverlay()
	if got := stripANSI(m.renderStatus(120)); strings.Contains(got, "\u2717") {
		t.Errorf("status bar still shows badge after open: %q", got)
	}
}
