package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// errorSurfacingNotices returns the notice segments in the transcript.
func errorSurfacingNotices(m *Model) []contentSegment {
	var out []contentSegment
	for _, seg := range m.content.segments {
		if seg.kind == segmentNotice {
			out = append(out, seg)
		}
	}
	return out
}

func TestRetryLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                 string
		attempt, maxAttempts int
		want                 string
	}{
		{name: "in range", attempt: 2, maxAttempts: 5, want: "retrying 2/5"},
		{name: "missing counts", attempt: 0, maxAttempts: 0, want: "retrying 1/1"},
		{name: "attempt above max is clamped", attempt: 7, maxAttempts: 3, want: "retrying 3/3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := retryLabel(tc.attempt, tc.maxAttempts); got != tc.want {
				t.Errorf("retryLabel(%d, %d) = %q, want %q", tc.attempt, tc.maxAttempts, got, tc.want)
			}
		})
	}
}

func TestRetryDiagnosticUsesActivityLabel(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	before := len(m.content.segments)

	m.applyEvent(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Kind: output.DiagnosticKindRetry, Severity: "warning", Message: "retrying", Attempt: 2, MaxAttempts: 5}))
	if got := len(m.content.segments); got != before {
		t.Errorf("transcript segments after retry = %d, want %d", got, before)
	}
	if m.activity.retry != "retrying 2/5" {
		t.Errorf("activity retry label = %q, want %q", m.activity.retry, "retrying 2/5")
	}

	m.applyEvent(output.NewAssistantChunkEventWithSource(1, "hi", ""))
	if m.activity.retry != "" {
		t.Errorf("activity retry label after model output = %q, want empty", m.activity.retry)
	}
}

func TestToolPanicNoticeAndDevLogStack(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.applyEvent(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Kind:     output.DiagnosticKindToolPanic,
		Severity: "error",
		Message:  "tool \"bash\" panicked: boom\ngoroutine 1 [running]:\nmain.run()",
	}))

	notices := errorSurfacingNotices(m)
	if len(notices) != 1 {
		t.Fatalf("notice count = %d, want 1", len(notices))
	}
	if !strings.Contains(notices[0].text, "tool panicked") {
		t.Errorf("notice text = %q, want tool panic notice", notices[0].text)
	}
	if strings.Contains(notices[0].text, "goroutine") {
		t.Errorf("notice text = %q, want no stack in the transcript", notices[0].text)
	}
	if len(m.devLog.entries) != 1 {
		t.Fatalf("dev log entries = %d, want 1", len(m.devLog.entries))
	}
	entry := m.devLog.entries[0]
	if entry.Severity != devLogError || entry.Source != "agent" || !strings.Contains(entry.Message, "goroutine 1") {
		t.Errorf("dev log entry = %+v, want agent error with stack", entry.devLogParams)
	}
}

func TestSessionHealthSaveNotice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		scope      string
		severity   string
		wantNotice bool
		wantLog    bool
		wantLevel  devLogSeverity
	}{
		{name: "save warning shows notice and logs", scope: output.SessionHealthScopeSave, severity: "warning", wantNotice: true, wantLog: true, wantLevel: devLogWarning},
		{name: "save critical shows notice and logs error", scope: output.SessionHealthScopeSave, severity: "critical", wantNotice: true, wantLog: true, wantLevel: devLogError},
		{name: "other scope logs only", scope: "", severity: "warning", wantLog: true, wantLevel: devLogWarning},
		{name: "save info is silent", scope: output.SessionHealthScopeSave, severity: "info"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m.applyEvent(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Scope:    tc.scope,
				Severity: tc.severity,
				Notes:    []string{"session state degraded"},
			}))

			notices := errorSurfacingNotices(m)
			if got := len(notices) == 1; got != tc.wantNotice {
				t.Errorf("notice shown = %v, want %v", got, tc.wantNotice)
			}
			if tc.wantNotice && !strings.Contains(notices[0].text, "session state degraded") {
				t.Errorf("notice text = %q, want session note", notices[0].text)
			}
			if got := len(m.devLog.entries) == 1; got != tc.wantLog {
				t.Fatalf("dev log entry recorded = %v, want %v", got, tc.wantLog)
			}
			if tc.wantLog && m.devLog.entries[0].Severity != tc.wantLevel {
				t.Errorf("dev log severity = %v, want %v", m.devLog.entries[0].Severity, tc.wantLevel)
			}
		})
	}
}

func TestConversationWarningNotices(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		events     []output.Event
		wantNotice int
		wantLast   string
		wantAbsent string
	}{
		{
			name:       "save warning",
			events:     []output.Event{output.NewConversationWarningEvent(output.ConversationWarningKindSave, "save failed")},
			wantNotice: 1,
			wantLast:   "save failed",
		},
		{
			name:       "compact warning",
			events:     []output.Event{output.NewConversationWarningEvent(output.ConversationWarningKindCompact, "compaction failed")},
			wantNotice: 1,
			wantLast:   "compaction failed",
		},
		{
			name:       "run failure with no preceding stop error",
			events:     []output.Event{output.NewConversationWarningEvent(output.ConversationWarningKindRun, "run failed")},
			wantNotice: 1,
			wantLast:   "run failed",
		},
		{
			name: "run failure after stop reason error is suppressed",
			events: []output.Event{
				output.NewStopReasonEvent(1, "error", errors.New("boom")),
				output.NewConversationWarningEvent(output.ConversationWarningKindRun, "run failed"),
			},
			wantNotice: 1,
			wantLast:   "boom",
			wantAbsent: "run failed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			for _, ev := range tc.events {
				m.applyEvent(ev)
			}
			notices := errorSurfacingNotices(m)
			if len(notices) != tc.wantNotice {
				t.Fatalf("notice count = %d, want %d", len(notices), tc.wantNotice)
			}
			if last := notices[len(notices)-1].text; !strings.Contains(last, tc.wantLast) {
				t.Errorf("last notice = %q, want it to contain %q", last, tc.wantLast)
			}
			for _, seg := range notices {
				if tc.wantAbsent != "" && strings.Contains(seg.text, tc.wantAbsent) {
					t.Errorf("notice %q contains %q, want it suppressed", seg.text, tc.wantAbsent)
				}
			}
		})
	}
}

func TestScopedChildRetryRecordsAgentDevLog(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	before := len(m.content.segments)
	ev := output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Kind: output.DiagnosticKindRetry, Severity: "warning", Message: "retrying", Attempt: 1, MaxAttempts: 3})
	ev.Scope = output.EventScope{AgentID: "child-1", AgentType: "explore"}
	m.applyEvent(ev)

	if got := len(m.content.segments); got != before {
		t.Errorf("transcript segments after scoped retry = %d, want %d", got, before)
	}
	if len(m.devLog.entries) != 1 {
		t.Fatalf("dev log entries = %d, want 1", len(m.devLog.entries))
	}
	if got := m.devLog.entries[0].AgentID; got != "child-1" {
		t.Errorf("dev log agent id = %q, want child-1", got)
	}
}

func TestReplayDoesNotRecordDevLogOrBadge(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	failed := output.NewToolCallFinishedEvent(1, "bash", "call-1", "", errors.New("exit 1"))

	m.applyEvent(output.Event{Type: output.EventTypeReplayStarted})
	m.applyEvent(failed)
	m.applyEvent(output.Event{Type: output.EventTypeReplayFinished})
	if len(m.devLog.entries) != 0 || m.devLog.unseen != 0 {
		t.Fatalf("after replay: entries = %d, unseen = %d, want 0 and 0", len(m.devLog.entries), m.devLog.unseen)
	}

	m.applyEvent(failed)
	if m.devLog.unseen != 1 {
		t.Errorf("unseen after live failure = %d, want 1", m.devLog.unseen)
	}
}

func TestClipboardFailureNoticeAndDevLog(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, clipboardFailedMsg{err: errors.New("status: wl-copy: not found")})

	notices := errorSurfacingNotices(m)
	if len(notices) != 1 || !strings.Contains(notices[0].text, "wl-copy: not found") {
		t.Fatalf("notices = %+v, want one error notice naming wl-copy", notices)
	}
	if len(m.devLog.entries) != 1 {
		t.Fatalf("dev log entries = %d, want 1", len(m.devLog.entries))
	}
	if e := m.devLog.entries[0]; e.Source != "tui" || e.Severity != devLogError {
		t.Errorf("dev log entry = %+v, want tui error", e.devLogParams)
	}
}
