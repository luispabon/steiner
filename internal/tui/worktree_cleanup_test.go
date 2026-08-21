package tui

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
)

func TestWorktreeCleanupPlan(t *testing.T) {
	listCalls := 0
	pruneCalls := 0
	wantErr := errors.New("prune failed")
	plan := NewWorktreeCleanupPlan(
		func(context.Context) (int, error) {
			listCalls++
			return 3, nil
		},
		func(context.Context) (int, error) {
			pruneCalls++
			return 2, wantErr
		},
	)

	count, err := plan.Count(context.Background())
	if err != nil || count != 3 || listCalls != 1 {
		t.Fatalf("Count() = (%d, %v), list calls = %d", count, err, listCalls)
	}
	if plan.ShouldPrune() {
		t.Fatal("ShouldPrune() true before Request")
	}
	plan.Request()
	if !plan.ShouldPrune() {
		t.Fatal("ShouldPrune() false after Request")
	}
	count, err = plan.Prune(context.Background())
	if !errors.Is(err, wantErr) || count != 2 || pruneCalls != 1 {
		t.Fatalf("Prune() = (%d, %v), prune calls = %d", count, err, pruneCalls)
	}
}

func TestWorktreeCleanupPlanNilReceiver(t *testing.T) {
	var plan *WorktreeCleanupPlan
	if count, err := plan.Count(context.Background()); count != 0 || err != nil {
		t.Fatalf("nil Count() = (%d, %v), want (0, nil)", count, err)
	}
	plan.Request()
	if plan.ShouldPrune() {
		t.Fatal("nil ShouldPrune() = true, want false")
	}
	if count, err := plan.Prune(context.Background()); count != 0 || err != nil {
		t.Fatalf("nil Prune() = (%d, %v), want (0, nil)", count, err)
	}
}

func TestWorktreeCleanupModalState(t *testing.T) {
	state := openWorktreeCleanupModal(80, 24, 4)
	if state.selectedAction != worktreeCleanupActionSkip {
		t.Fatalf("default action = %d, want skip", state.selectedAction)
	}
	if !state.IsOpen() {
		t.Fatal("modal is closed after open")
	}
	if got := state.moveSelection(-1).selectedAction; got != worktreeCleanupActionPrune {
		t.Fatalf("moveSelection(-1) = %d, want prune", got)
	}
	if got := state.moveSelection(1).moveSelection(1).selectedAction; got != worktreeCleanupActionSkip {
		t.Fatalf("moveSelection wrap = %d, want skip", got)
	}
	if state.closeWorktreeCleanupModal().IsOpen() {
		t.Fatal("modal is open after close")
	}
}

func TestExitFlowDecisionLogic(t *testing.T) {
	tests := []struct {
		name        string
		plan        *WorktreeCleanupPlan
		statusMode  string
		countMsg    worktreeCountMsg
		wantActions int
		wantModal   bool
		wantPhase   int
	}{
		{name: "no plan exits", wantActions: 1},
		{name: "running exits", plan: NewWorktreeCleanupPlan(func(context.Context) (int, error) { return 2, nil }, nil), statusMode: "running", wantActions: 1},
		{name: "empty count exits", plan: NewWorktreeCleanupPlan(func(context.Context) (int, error) { return 0, nil }, nil), countMsg: worktreeCountMsg{count: 0}, wantActions: 1},
		{name: "positive count offers cleanup", plan: NewWorktreeCleanupPlan(func(context.Context) (int, error) { return 2, nil }, nil), countMsg: worktreeCountMsg{count: 2}, wantModal: true, wantPhase: exitFlowPhaseCleanup},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controller := &testController{}
			m := newModel(Config{Controller: controller, WorktreeCleanup: tt.plan}, nil)
			m.status.mode = tt.statusMode
			_, cmd := m.beginExitFlow()
			if tt.plan != nil && tt.statusMode != "running" && tt.name != "empty count exits" && tt.name != "positive count offers cleanup" {
				t.Fatal("expected a count command")
			}
			if tt.plan != nil && tt.statusMode != "running" {
				m.Update(tt.countMsg)
			} else if cmd != nil {
				m.Update(cmd())
			}
			if got := controller.countRequestExit(); got != tt.wantActions {
				t.Fatalf("RequestExit count = %d, want %d", got, tt.wantActions)
			}
			if got := m.worktreeCleanupModal.IsOpen(); got != tt.wantModal {
				t.Fatalf("cleanup modal open = %v, want %v", got, tt.wantModal)
			}
			if tt.wantModal && m.exitFlowPhase != tt.wantPhase {
				t.Fatalf("exit phase = %d, want %d", m.exitFlowPhase, tt.wantPhase)
			}
		})
	}
}

func TestExitFlowCountingCtrlCIgnored(t *testing.T) {
	m := newModel(Config{Controller: &testController{}}, nil)
	m.exitFlowPhase = exitFlowPhaseCounting

	handled, _, cmd := m.handleNavigationKeyMsg(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("Ctrl-C was not handled")
	}
	if cmd != nil {
		t.Fatal("Ctrl-C during counting produced a command")
	}
	if m.exitModal.IsOpen() {
		t.Fatal("Ctrl-C during counting opened exit modal")
	}
	if m.exitFlowPhase != exitFlowPhaseCounting {
		t.Fatalf("exit phase = %d, want counting", m.exitFlowPhase)
	}
}

func TestExitFlowCountErrorExits(t *testing.T) {
	controller := &testController{}
	m := newModel(Config{Controller: controller}, nil)
	m.exitFlowPhase = exitFlowPhaseCounting

	m.handleWorktreeCountMsg(worktreeCountMsg{err: errors.New("count failed")})
	if m.exitFlowPhase != exitFlowPhaseNone {
		t.Fatalf("exit phase = %d, want none", m.exitFlowPhase)
	}
	if m.worktreeCleanupModal.IsOpen() {
		t.Fatal("count error opened cleanup modal")
	}
	if got := controller.countRequestExit(); got != 1 {
		t.Fatalf("RequestExit count = %d, want 1", got)
	}
}

func TestExitFlowStaleCountIgnored(t *testing.T) {
	controller := &testController{}
	m := newModel(Config{Controller: controller}, nil)
	m.exitFlowPhase = exitFlowPhaseCleanup

	m.handleWorktreeCountMsg(worktreeCountMsg{count: 2})
	if m.exitFlowPhase != exitFlowPhaseCleanup {
		t.Fatalf("exit phase = %d, want cleanup", m.exitFlowPhase)
	}
	if m.worktreeCleanupModal.IsOpen() {
		t.Fatal("stale count opened cleanup modal")
	}
	if got := controller.countRequestExit(); got != 0 {
		t.Fatalf("RequestExit count = %d, want 0", got)
	}
}

func TestExitFlowCountThenEscape(t *testing.T) {
	m := newModel(Config{Controller: &testController{}}, nil)
	m.exitFlowPhase = exitFlowPhaseCounting
	m.handleWorktreeCountMsg(worktreeCountMsg{count: 2})
	if !m.worktreeCleanupModal.IsOpen() || m.exitFlowPhase != exitFlowPhaseCleanup {
		t.Fatal("positive count did not open cleanup modal")
	}

	m.handleWorktreeCleanupModalKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.worktreeCleanupModal.IsOpen() {
		t.Fatal("escape did not close cleanup modal")
	}
	if m.exitFlowPhase != exitFlowPhaseNone {
		t.Fatalf("exit phase = %d, want none", m.exitFlowPhase)
	}
}

func TestWorktreeCleanupPlanCountCancelled(t *testing.T) {
	plan := NewWorktreeCleanupPlan(func(ctx context.Context) (int, error) {
		return 0, ctx.Err()
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := plan.Count(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Count() error = %v, want context.Canceled", err)
	}

	controller := &testController{}
	m := newModel(Config{Controller: controller}, nil)
	m.exitFlowPhase = exitFlowPhaseCounting
	m.handleWorktreeCountMsg(worktreeCountMsg{err: err})
	if got := controller.countRequestExit(); got != 1 {
		t.Fatalf("RequestExit count = %d, want 1", got)
	}
	if m.exitFlowPhase != exitFlowPhaseNone {
		t.Fatalf("exit phase = %d, want none", m.exitFlowPhase)
	}
}

func TestExitFlowReentryGuard(t *testing.T) {
	calls := 0
	plan := NewWorktreeCleanupPlan(func(context.Context) (int, error) {
		calls++
		return 1, nil
	}, nil)
	m := newModel(Config{Controller: &testController{}, WorktreeCleanup: plan}, nil)
	_, first := m.beginExitFlow()
	_, second := m.beginExitFlow()
	if first == nil || second != nil {
		t.Fatal("beginExitFlow did not guard re-entry")
	}
	if calls != 0 {
		t.Fatalf("Count called before commands ran: %d", calls)
	}
	first()
	if calls != 1 {
		t.Fatalf("Count calls = %d, want 1 command call", calls)
	}
}

func TestWorktreeCleanupModalKeys(t *testing.T) {
	t.Run("escape cancels exit", func(t *testing.T) {
		m := newModel(Config{Controller: &testController{}}, nil)
		m.exitFlowPhase = exitFlowPhaseCleanup
		m.openWorktreeCleanupModal(80, 24, 2)
		m.handleWorktreeCleanupModalKey(tea.KeyPressMsg{Code: tea.KeyEsc})
		if m.worktreeCleanupModal.IsOpen() || m.exitFlowPhase != exitFlowPhaseNone {
			t.Fatal("escape did not close modal and clear exit phase")
		}
	})

	for _, tc := range []struct {
		name      string
		selection int
		wantPrune bool
	}{
		{name: "skip", selection: worktreeCleanupActionSkip, wantPrune: false},
		{name: "prune", selection: worktreeCleanupActionPrune, wantPrune: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := &testController{}
			plan := NewWorktreeCleanupPlan(nil, nil)
			m := newModel(Config{Controller: controller, WorktreeCleanup: plan}, nil)
			m.exitFlowPhase = exitFlowPhaseCleanup
			m.openWorktreeCleanupModal(80, 24, 2)
			m.worktreeCleanupModal.selectedAction = tc.selection
			m.handleWorktreeCleanupModalKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			if plan.ShouldPrune() != tc.wantPrune {
				t.Fatalf("ShouldPrune() = %v, want %v", plan.ShouldPrune(), tc.wantPrune)
			}
			if controller.countRequestExit() != 1 {
				t.Fatal("enter did not dispatch one exit request")
			}
		})
	}
}

func TestDoExitWithoutControllerReturnsQuit(t *testing.T) {
	m := newModel(Config{}, nil)
	_, cmd := m.doExit()
	if cmd == nil {
		t.Fatal("doExit returned nil command without controller")
	}
}

var _ interactive.Controller = (*testController)(nil)
