package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

func delegateCancelTestRows() []delegateActiveRow {
	return []delegateActiveRow{
		{agentID: "explore-1", agentType: "explore", taskPreview: "inspect the repository"},
		{agentID: "code-1", agentType: "code", taskPreview: "make the requested change", isCode: true},
	}
}

func TestDelegateCancelModalSelectorRouting(t *testing.T) {
	m := newModel(Config{}, nil)
	m.delegateCancelModal = openDelegateCancelModal(80, 24, delegateCancelTestRows())
	m.status.mode = "running"

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if got, want := m.delegateCancelModal.selected, 4; got != want {
		t.Fatalf("wrapped selection = %d, want %d", got, want)
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.delegateCancelModal.selected; got != 0 {
		t.Fatalf("wrapped selection after down = %d, want 0", got)
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.delegateCancelModal.screen; got != delegateCancelScreenConfirmTarget {
		t.Fatalf("non-code screen = %d, want %d", got, delegateCancelScreenConfirmTarget)
	}

	m.delegateCancelModal = openDelegateCancelModal(80, 24, delegateCancelTestRows())
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.delegateCancelModal.screen; got != delegateCancelScreenConfirmTargetCode {
		t.Fatalf("code screen = %d, want %d", got, delegateCancelScreenConfirmTargetCode)
	}

	ctrl := &testController{}
	m.controller = ctrl
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.delegateCancelModal.IsOpen() {
		t.Fatal("Esc from confirmation did not return to selector")
	}
	if got := ctrl.countInterruptActiveRun(); got != 0 {
		t.Fatalf("Esc cascaded to interrupt, got %d actions", got)
	}
}

func TestDelegateCancelModalDispatchesActions(t *testing.T) {
	tests := []struct {
		name       string
		screen     delegateCancelScreen
		selected   int
		target     int
		wantAction interactive.Action
	}{
		{
			name:       "non-code stop",
			screen:     delegateCancelScreenConfirmTarget,
			wantAction: interactive.CancelDelegate{AgentID: "explore-1", Discard: false},
		},
		{
			name:       "code keep worktree",
			screen:     delegateCancelScreenConfirmTargetCode,
			target:     1,
			wantAction: interactive.CancelDelegate{AgentID: "code-1", Discard: false},
		},
		{
			name:       "code discard worktree",
			screen:     delegateCancelScreenConfirmTargetCode,
			selected:   1,
			target:     1,
			wantAction: interactive.CancelDelegate{AgentID: "code-1", Discard: true},
		},
		{
			name:       "stop all",
			screen:     delegateCancelScreenConfirmAll,
			wantAction: interactive.CancelAllDelegates{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := &testController{}
			m := newModel(Config{}, nil)
			m.controller = ctrl
			m.input.SetValue("keep this draft")
			m.status.mode = "running"
			for _, row := range delegateCancelTestRows() {
				m.content.AppendEvent(output.NewDelegationStartedEventWithType(row.agentID, row.taskPreview, "", "", row.agentType))
			}
			m.delegateCancelModal = openDelegateCancelModal(80, 24, delegateCancelTestRows())
			m.delegateCancelModal.screen = tt.screen
			m.delegateCancelModal.selected = tt.selected
			m.delegateCancelModal.target = tt.target

			m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.delegateCancelModal.IsOpen() {
				t.Fatal("confirmed action left modal open")
			}
			ctrl.mu.Lock()
			if len(ctrl.actions) != 1 {
				ctrl.mu.Unlock()
				t.Fatalf("got %d actions, want 1", len(ctrl.actions))
			}
			got := ctrl.actions[0]
			ctrl.mu.Unlock()
			if got != tt.wantAction {
				t.Fatalf("action = %#v, want %#v", got, tt.wantAction)
			}
			if got := m.input.Value(); got != "keep this draft" {
				t.Fatalf("input = %q, want draft preserved", got)
			}
			if got := m.status.mode; got != "running" {
				t.Fatalf("status mode = %q, want running", got)
			}
		})
	}
}

func TestDelegateCancelModalStopRunDispatchesInterrupt(t *testing.T) {
	ctrl := &testController{}
	m := newModel(Config{}, nil)
	m.controller = ctrl
	m.status.mode = "running"
	m.delegateCancelModal = openDelegateCancelModal(80, 24, delegateCancelTestRows())
	m.delegateCancelModal.screen = delegateCancelScreenConfirmRun

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.delegateCancelModal.IsOpen() {
		t.Fatal("stop run left modal open")
	}
	if got := ctrl.countInterruptActiveRun(); got != 1 {
		t.Fatalf("interrupt actions = %d, want 1", got)
	}
	if got := m.status.mode; got != "" {
		t.Fatalf("status mode = %q, want empty after whole-run interrupt", got)
	}
}

func TestDelegateCancelModalKeepWorkingRefreshesRows(t *testing.T) {
	m := newModel(Config{}, nil)
	m.content.AppendEvent(output.NewDelegationStartedEventWithType("explore-1", "inspect", "", "", "explore"))
	m.content.AppendEvent(output.NewDelegationStartedEventWithType("code-1", "change", "", "", "code"))
	m.delegateCancelModal = openDelegateCancelModal(80, 24, m.content.ActiveDelegateRows())
	m.delegateCancelModal.screen = delegateCancelScreenConfirmTarget
	m.delegateCancelModal.target = 0

	m.content.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "explore-1", Status: "complete"}))
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.delegateCancelModal.IsOpen() || m.delegateCancelModal.screen != delegateCancelScreenSelector {
		t.Fatal("keep working did not return to selector")
	}
	if len(m.delegateCancelModal.rows) != 1 || m.delegateCancelModal.rows[0].agentID != "code-1" {
		t.Fatalf("refreshed rows = %#v, want only code-1", m.delegateCancelModal.rows)
	}
}

func TestDelegateCancelModalStopDoesNotDispatchStaleTarget(t *testing.T) {
	ctrl := &testController{}
	m := newModel(Config{}, nil)
	m.controller = ctrl
	m.status.mode = "running"
	m.content.AppendEvent(output.NewDelegationStartedEventWithType("child-1", "inspect", "", "", "explore"))
	m.delegateCancelModal = openDelegateCancelModal(80, 24, m.content.ActiveDelegateRows())

	m.content.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{AgentID: "child-1", Status: "complete"}))
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.delegateCancelModal.IsOpen() || m.delegateCancelModal.screen != delegateCancelScreenConfirmTarget {
		t.Fatal("stale target did not reach stop confirmation")
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if len(ctrl.actions) != 0 {
		t.Fatalf("stale target dispatched %d actions, want none", len(ctrl.actions))
	}
	if m.delegateCancelModal.IsOpen() {
		t.Fatal("empty refreshed selector remained open")
	}
	var found bool
	for _, segment := range m.content.segments {
		if strings.Contains(segment.text, "delegate child-1 already finished; worktree retained") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("stale target did not report retained worktree")
	}
}

func TestDelegateCancelModalRenderSelectorRow(t *testing.T) {
	m := newModel(Config{}, nil)
	m.delegateCancelModal = openDelegateCancelModal(80, 24, []delegateActiveRow{{
		agentID:     "explore-1",
		agentType:   "explore",
		taskPreview: strings.Repeat("long preview ", 20),
	}})
	rendered := stripANSI(m.renderDelegateCancelModal())
	if !strings.Contains(rendered, "explore · explore-1 ·") {
		t.Fatalf("selector row = %q, want type, ID, and separators", rendered)
	}
	if !strings.Contains(rendered, "…") {
		t.Fatalf("selector row = %q, want truncated preview", rendered)
	}
	if !strings.Contains(m.renderDelegateCancelModal(), "explore") {
		t.Fatal("styled selector does not contain type label")
	}
}

func TestDelegateCancelModalShortcutOpensOnlyWithActiveDelegate(t *testing.T) {
	keys := []tea.KeyPressMsg{
		{Code: tea.KeyEsc},
		{Code: 'c', Mod: tea.ModCtrl},
		{Code: 'd', Mod: tea.ModCtrl},
	}
	for _, key := range keys {
		t.Run(key.String(), func(t *testing.T) {
			ctrl := &testController{}
			m := newModel(Config{}, nil)
			m.controller = ctrl
			m.status.mode = "running"
			m.content.AppendEvent(output.NewDelegationStartedEventWithType("child-1", "inspect", "", "", "explore"))
			m = updateModel(t, m, key)
			if !m.delegateCancelModal.IsOpen() {
				t.Fatal("active delegate did not open stop modal")
			}
			if got := ctrl.countInterruptActiveRun(); got != 0 {
				t.Fatalf("shortcut dispatched interrupt, got %d", got)
			}
		})
	}

	ctrl := &testController{}
	m := newModel(Config{}, nil)
	m.controller = ctrl
	m.status.mode = "running"
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.delegateCancelModal.IsOpen() {
		t.Fatal("Esc without delegate opened stop modal")
	}
	if got := ctrl.countInterruptActiveRun(); got != 1 {
		t.Fatalf("Esc without delegate interrupts = %d, want 1", got)
	}
}

var _ interactive.Controller = (*testController)(nil)
