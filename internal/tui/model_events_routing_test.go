package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

// approvalHeadController reports a fixed approval head request ID. The embedded
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
		name        string
		coordinated bool
		callID      string
		head        string
		wantActive  bool
	}{
		{name: "no coordinator falls back to event stream", coordinated: false, callID: "call-1", wantActive: true},
		{name: "head matches request", coordinated: true, callID: "call-1", head: "call-1", wantActive: true},
		{name: "another call is head", coordinated: true, callID: "call-1", head: "call-0", wantActive: false},
		{name: "cached grant leaves no coordinator head", coordinated: true, callID: "call-cached", wantActive: false},
		{name: "empty head and empty request call ID", coordinated: true, wantActive: false},
		{name: "empty request ID with coordinator", coordinated: true, head: "call-1", wantActive: false},
		{name: "empty request ID without coordinator", coordinated: false, wantActive: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			if tc.coordinated {
				m.controller = approvalHeadController{head: tc.head}
			} else {
				m.controller = &testController{}
			}
			requested := output.Event{Type: output.EventTypeApprovalRequested, Payload: output.ApprovalEvent{CallID: tc.callID, RequestID: tc.callID, Tool: "bash"}}
			m = updateModel(t, m, runtimeEventMsg{Event: requested})
			if m.approval.active != tc.wantActive {
				t.Errorf("approval active = %t, want %t", m.approval.active, tc.wantActive)
			}
			if got := m.approvalRequestPromptable(tc.callID); got != tc.wantActive {
				t.Errorf("approvalRequestPromptable(%q) = %t, want %t", tc.callID, got, tc.wantActive)
			}
		})
	}
}

// Cached session grants emit requested events without registering with the
// coordinator. They must not open the tray, reset the composer or notify.
func TestUpdateCachedApprovalRequestLeavesComposerAndTrayAlone(t *testing.T) {
	t.Parallel()
	fn := &fakeNotifier{avail: true, notified: make(chan struct{}, 1)}
	m := newModel(Config{WorkingDir: "/home/user/myproject", Notifier: fn}, nil)
	m.controller = approvalHeadController{head: ""}
	m.status.mode = "build"
	m.input.SetValue("draft instruction")
	m.input.Focus()

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-cached", RequestID: "call-cached", Mode: "session grant", Preview: `{"path":"x"}`, Kind: "path"})})

	if m.approval.active {
		t.Fatal("cached grant activated the approval tray")
	}
	if m.approvalRequestPromptable("call-cached") {
		t.Error("approvalRequestPromptable(call-cached) = true, want false for a cached grant")
	}
	if got, want := m.input.Value(), "draft instruction"; got != want {
		t.Errorf("input value = %q, want %q", got, want)
	}
	if !m.input.Focused() {
		t.Error("input.Focused() = false, want the cached grant to leave the composer focused")
	}
	if got, want := m.status.mode, "build"; got != want {
		t.Errorf("status mode = %q, want %q", got, want)
	}
	fn.expectNoCall(t)
}

func TestUpdateApprovalRequestNotifiesOnlyForCoordinatorHead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		head       string
		callID     string
		wantNotify bool
	}{
		{name: "cached grant with empty head", callID: "call-cached"},
		{name: "empty head and empty call ID", callID: ""},
		{name: "mismatching head", head: "call-other", callID: "call-cached"},
		{name: "matching head", head: "call-1", callID: "call-1", wantNotify: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fn := &fakeNotifier{avail: true, notified: make(chan struct{}, 1)}
			m := newModel(Config{WorkingDir: "/home/user/myproject", Notifier: fn}, nil)
			m.controller = approvalHeadController{head: tc.head}

			m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "bash", CallID: tc.callID, RequestID: tc.callID, Mode: "prompt", Preview: "{}", Kind: "path"})})

			if tc.wantNotify {
				if !m.approval.active {
					t.Fatalf("approval.active = false for coordinator head %q request", tc.callID)
				}
				calls := fn.waitForCall(t)
				if len(calls) != 1 {
					t.Fatalf("got %d Notify calls, want 1", len(calls))
				}
				if !strings.Contains(calls[0].Reason, "bash") {
					t.Errorf("Reason = %q, want it to contain %q", calls[0].Reason, "bash")
				}
				return
			}
			if m.approval.active {
				t.Fatal("approval.active = true, want the tray to stay closed")
			}
			fn.expectNoCall(t)
		})
	}
}

func TestUpdateApprovalPromotionDoesNotNotify(t *testing.T) {
	t.Parallel()
	coord := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coord}
	fn := &fakeNotifier{avail: true, notified: make(chan struct{}, 1)}
	m := newModel(Config{WorkingDir: "/home/user/myproject", Controller: ctrl, Notifier: fn}, nil)

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-B", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", "call-A", nil)})
	bID, bCh := coord.Begin("write", "", "path", "")
	aID, aCh := coord.Begin("read", "", "path", "")
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "read", CallID: "call-A", RequestID: aID, Mode: "prompt", Preview: "{}", Kind: "path"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-B", RequestID: bID, Mode: "prompt", Preview: "{}", Kind: "path"})})
	if !m.approval.active {
		t.Fatal("approval.active = false for coordinator head call-B request")
	}
	if got, want := m.approval.identity, bID; got != want {
		t.Fatalf("approval.identity = %q, want %q", got, want)
	}
	// Only opening the tray notifies; the queued call-A request stayed silent.
	calls := fn.waitForCall(t)
	if len(calls) != 1 {
		t.Fatalf("got %d Notify calls before promotion, want 1", len(calls))
	}
	if !strings.Contains(calls[0].Reason, "write") {
		t.Fatalf("first notification Reason = %q, want the call-B write request", calls[0].Reason)
	}
	// Let a delayed queued-request notification surface before resetting.
	fn.expectQuiet(t)
	if calls := fn.snapshot(); len(calls) != 1 {
		t.Fatalf("got %d Notify calls after the quiet window, want 1", len(calls))
	}

	fn.reset()
	coord.Submit(interactive.SubmitApproval{Identity: bID, Tool: "write", Mode: "prompt", Decision: "allow_once"})
	coord.Finish(bCh)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-B", RequestID: bID, Mode: "prompt", Preview: "{}", Message: "ok", Kind: "path"})})
	if !m.approval.active {
		t.Fatal("approval.active = false after call-B promotion, want call-A")
	}
	if got, want := m.approval.identity, aID; got != want {
		t.Fatalf("approval.identity after promotion = %q, want %q", got, want)
	}
	fn.expectNoCall(t)
	coord.Finish(aCh)
}

func TestDecisionPreservesActiveApproval(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		coordinated bool
		head        string
		active      bool
		activeID    string
		callID      string
		want        bool
	}{
		{name: "unrelated decision while head matches active", coordinated: true, head: "call-A", active: true, activeID: "call-A", callID: "call-cached", want: true},
		{name: "decision for the active call", coordinated: true, head: "call-A", active: true, activeID: "call-A", callID: "call-A"},
		{name: "head moved on", coordinated: true, head: "call-B", active: true, activeID: "call-A", callID: "call-cached"},
		{name: "no coordinator", coordinated: false, active: true, activeID: "call-A", callID: "call-cached"},
		{name: "no active tray", coordinated: true, head: "call-A", callID: "call-cached"},
		{name: "empty active identity", coordinated: true, head: "call-A", active: true, callID: "call-cached"},
		{name: "empty decision call ID", coordinated: true, head: "call-A", active: true, activeID: "call-A", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			if tc.coordinated {
				m.controller = approvalHeadController{head: tc.head}
			} else {
				m.controller = &testController{}
			}
			m.approval = approvalState{active: tc.active, identity: tc.activeID}
			if got := m.decisionPreservesActiveApproval(tc.callID); got != tc.want {
				t.Errorf("decisionPreservesActiveApproval(%q) = %t, want %t", tc.callID, got, tc.want)
			}
		})
	}
}

func TestUpdateUnrelatedApprovalDecisionPreservesTrayAndSelection(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.controller = approvalHeadController{head: "call-A"}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-A", RequestID: "call-A", Mode: "prompt", Preview: `{"path":"a"}`, Kind: "path"})})
	if !m.approval.active {
		t.Fatal("approval.active = false for coordinator head call-A request")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := m.approval.selectedAction, 1; got != want {
		t.Fatalf("selectedAction = %d, want %d", got, want)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Turn: 1, Tool: "read", CallID: "call-cached", RequestID: "call-cached", Mode: "always allowed", Preview: "{}", Message: "always allowed", Kind: "path"})})
	if !m.approval.active {
		t.Fatal("unrelated decision cleared the active tray")
	}
	if got, want := m.approval.identity, "call-A"; got != want {
		t.Errorf("approval.identity = %q, want %q", got, want)
	}
	if got, want := m.approval.selectedAction, 1; got != want {
		t.Errorf("selectedAction = %d, want %d after unrelated decision", got, want)
	}
	if got, want := m.status.mode, "approval"; got != want {
		t.Errorf("status mode = %q, want %q", got, want)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(output.ApprovalEventParams{Turn: 1, Tool: "write", CallID: "call-A", RequestID: "call-A", Mode: "prompt", Preview: `{"path":"a"}`, Message: "ok", Kind: "path"})})
	if m.approval.active {
		t.Fatal("approval.active = true after the active call-A decision")
	}
	if got, want := m.status.mode, "running"; got != want {
		t.Errorf("status mode = %q, want %q", got, want)
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
