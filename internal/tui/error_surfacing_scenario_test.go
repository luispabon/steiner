package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

// TestErrorSurfacingScenario feeds one run's error-bearing events through
// Update and checks the transcript notices, the activity label and the status
// bar badge.
func TestErrorSurfacingScenario(t *testing.T) {
	t.Parallel()
	m := updateModel(t, newModel(Config{}, nil), tea.WindowSizeMsg{Width: 160, Height: 40})
	feed := func(ev output.Event) {
		m = updateModel(t, m, runtimeEventMsg{Event: ev})
	}

	feed(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Kind:        output.DiagnosticKindRetry,
		Severity:    "warning",
		Message:     "provider returned transient error, retrying turn in 5s",
		Attempt:     1,
		MaxAttempts: 3,
	}))
	if m.activity.retry != "retrying 1/3" {
		t.Errorf("activity retry label = %q, want %q", m.activity.retry, "retrying 1/3")
	}
	if notices := errorSurfacingNotices(m); len(notices) != 0 {
		t.Fatalf("notices after retry = %+v, want none in the transcript", notices)
	}

	feed(output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Kind:     output.DiagnosticKindToolPanic,
		Severity: "error",
		Message:  "tool \"bash\" panicked: boom\ngoroutine 1 [running]:\nmain.run()",
	}))
	for range 6 {
		feed(output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call-sub", "", errors.New("quota exceeded"), output.ToolPreview{}, &output.DelegationAdmission{Status: "rejected", AgentID: "agent-1"}))
	}
	feed(output.NewStopReasonEvent(1, "error", errors.New("provider failed")))
	// A failed run after a stop reason error is suppressed: the stop error is the notice.
	feed(output.NewConversationWarningEvent(output.ConversationWarningKindRun, "run failed"))

	want := []struct {
		text  string
		count int
	}{
		{"internal error: a tool panicked; full stack in the dev log", 1},
		{"sub_agent rejected: quota exceeded", 6},
		{"provider failed", 1},
	}
	notices := errorSurfacingNotices(m)
	if len(notices) != len(want) {
		t.Fatalf("notice count = %d, want %d: %+v", len(notices), len(want), notices)
	}
	for i, w := range want {
		if got := notices[i]; got.text != w.text || got.noticeCount != w.count || got.noticeSeverity != noticeError {
			t.Errorf("notice %d = {%q, count %d, severity %v}, want {%q, count %d, noticeError}", i, got.text, got.noticeCount, got.noticeSeverity, w.text, w.count)
		}
	}
	if strings.Contains(stripANSI(m.content.String(160)), "run failed") {
		t.Error("run failed notice rendered after stop reason error, want it suppressed")
	}

	if m.status.devLogUnseen != len(m.devLog.entries) || len(m.devLog.entries) == 0 {
		t.Fatalf("devLogUnseen = %d, dev log entries = %d, want equal and non-zero", m.status.devLogUnseen, len(m.devLog.entries))
	}
	if badge := stripANSI(m.renderStatus(160)); !strings.Contains(badge, "✗ ") {
		t.Errorf("status bar = %q, want error badge", badge)
	}
}
