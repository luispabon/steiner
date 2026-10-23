package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

// approvalHeadController reports a fixed approval head identity. The embedded
// Controller is nil and is never called on these paths.
type approvalHeadController struct {
	interactive.Controller
	head string
}

func (c approvalHeadController) ApprovalHeadIdentity() string { return c.head }

func TestUpdateRoutesPhaseIndicatorEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		state    string
		wantMode string
	}{
		{name: "running shows phase only", state: "running", wantMode: "build"},
		{name: "other state is shown in mode", state: "failed", wantMode: "build (failed)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewPhaseIndicatorEvent("run-1", "build", tc.state, "")})
			if m.status.mode != tc.wantMode {
				t.Errorf("status mode = %q, want %q", m.status.mode, tc.wantMode)
			}
			if m.oneshotPhase != "build" {
				t.Errorf("oneshotPhase = %q, want build", m.oneshotPhase)
			}
		})
	}
}

func TestUpdateRoutesSandboxStatusEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		message         string
		wantStatusLines int
	}{
		// The raw sandbox_status event line is always appended and renders as a
		// status line, so a non-empty message adds one more.
		{name: "message appends status line", message: "docker ready", wantStatusLines: 2},
		{name: "empty message only updates status", message: "", wantStatusLines: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewSandboxStatusEvent("active", tc.message)})
			if m.sidebar.sandboxStatus != "active" || m.status.sandboxStatus != "active" {
				t.Errorf("sandbox status sidebar=%q status=%q, want active", m.sidebar.sandboxStatus, m.status.sandboxStatus)
			}
			transcript := stripANSI(m.content.String(m.viewport.Width()))
			if got := strings.Count(transcript, "status · "); got != tc.wantStatusLines {
				t.Errorf("status lines = %d, want %d: %q", got, tc.wantStatusLines, transcript)
			}
		})
	}
}

func TestUpdateRoutesWorkflowHandoffEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		decision string
	}{
		{name: "accepted closes modal", decision: output.EventTypeWorkflowHandoffAccepted},
		{name: "declined closes modal", decision: output.EventTypeWorkflowHandoffDeclined},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			requested := output.Event{Type: output.EventTypeWorkflowHandoffRequested, Payload: output.WorkflowHandoffEvent{Next: "build", Target: "build"}}
			m = updateModel(t, m, runtimeEventMsg{Event: requested})
			if !m.workflowHandoff.open {
				t.Fatal("requested handoff did not open modal")
			}
			decided := output.Event{Type: tc.decision, Payload: output.WorkflowHandoffEvent{Next: "build", Decision: "accepted"}}
			m = updateModel(t, m, runtimeEventMsg{Event: decided})
			if m.workflowHandoff.open {
				t.Error("decided handoff left modal open")
			}
		})
	}
}

func TestUpdateApprovalRequestedRespectsHeadIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		head       string
		wantActive bool
	}{
		{name: "no coordinator head", head: "", wantActive: true},
		{name: "head matches request", head: "call-1", wantActive: true},
		{name: "another call is head", head: "call-0", wantActive: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m.controller = approvalHeadController{head: tc.head}
			requested := output.Event{Type: output.EventTypeApprovalRequested, Payload: output.ApprovalEvent{CallID: "call-1", Tool: "bash"}}
			m = updateModel(t, m, runtimeEventMsg{Event: requested})
			if m.approval.active != tc.wantActive {
				t.Errorf("approval active = %t, want %t", m.approval.active, tc.wantActive)
			}
		})
	}
}

func TestUpdateRoutesInlineFailureMessages(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	cases := []struct {
		name     string
		msg      tea.Msg
		wantLine string
	}{
		{name: "controller handle failure", msg: controllerHandleFailedMsg{err: boom}, wantLine: "✗ boom"},
		{name: "clipboard failure", msg: clipboardFailedMsg{err: boom}, wantLine: "✗ boom"},
		{name: "phase transition failure", msg: phaseTransitionFailedMsg{err: boom}, wantLine: "phase transition failed: boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tc.msg)
			if transcript := stripANSI(m.content.String(m.viewport.Width())); !strings.Contains(transcript, tc.wantLine) {
				t.Errorf("transcript missing %q: %q", tc.wantLine, transcript)
			}
		})
	}
}
