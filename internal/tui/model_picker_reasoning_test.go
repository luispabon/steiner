package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/provider"
)

func TestModelPickerEnterOnReasoningCapableModelOpensReasoningStep(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Model:      "small",
		ModelNames: []string{"small", "large"},
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"large": {SupportedEfforts: []string{"low", "medium", "high"}},
		},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // select "large"
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = true, want false once reasoning step opens")
	}
	if !m.reasoningPicker.IsOpen() {
		t.Fatal("reasoningPicker.IsOpen() = false, want true for a reasoning-capable model")
	}
	if got, want := m.primaryModel, "small"; got != want {
		t.Fatalf("primaryModel = %q, want %q — model switch must not commit before reasoning step", got, want)
	}
	if len(ctrl.actions) != 0 {
		t.Fatalf("controller received %d actions, want 0 before reasoning is chosen", len(ctrl.actions))
	}
}

func TestModelPickerEnterOnNonReasoningModelCommitsImmediately(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Model:      "small",
		ModelNames: []string{"small", "large"},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.modelPicker.IsOpen() || m.reasoningPicker.IsOpen() {
		t.Fatal("expected both pickers closed after immediate commit")
	}
	if got, want := m.primaryModel, "large"; got != want {
		t.Fatalf("primaryModel = %q, want %q", got, want)
	}

	sw, ok := lastSwitchModelAction(t, ctrl)
	if !ok {
		t.Fatal("expected a SwitchModel action")
	}
	if sw.Reasoning != nil {
		t.Fatalf("Reasoning = %+v, want nil for a non-reasoning model", sw.Reasoning)
	}
}

func TestReasoningPickerEnterCommitsModelAndReasoning(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Model:      "small",
		ModelNames: []string{"small", "large"},
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"large": {SupportedEfforts: []string{"low", "medium", "high"}},
		},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // select "large"
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	// reasoningPicker: index 0 is "provider default", move down twice for "medium"
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.reasoningPicker.IsOpen() {
		t.Fatal("reasoningPicker.IsOpen() = true, want false after commit")
	}
	if got, want := m.primaryModel, "large"; got != want {
		t.Fatalf("primaryModel = %q, want %q", got, want)
	}

	sw, ok := lastSwitchModelAction(t, ctrl)
	if !ok {
		t.Fatal("expected a SwitchModel action")
	}
	if sw.Reasoning == nil || sw.Reasoning.Kind != provider.ReasoningOverrideEffort || sw.Reasoning.Effort != "medium" {
		t.Fatalf("Reasoning = %+v, want effort override 'medium'", sw.Reasoning)
	}
	if got, want := m.sidebar.reasoning, "medium"; got != want {
		t.Fatalf("sidebar.reasoning = %q, want %q", got, want)
	}
}

func TestReasoningPickerProviderDefaultOption(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Model:      "small",
		ModelNames: []string{"small", "large"},
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"large": {SupportedEfforts: []string{"low", "medium"}},
		},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	// First candidate is already "provider default"; commit without moving.
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	sw, ok := lastSwitchModelAction(t, ctrl)
	if !ok {
		t.Fatal("expected a SwitchModel action")
	}
	if sw.Reasoning == nil || sw.Reasoning.Kind != provider.ReasoningOverrideProviderDefault {
		t.Fatalf("Reasoning = %+v, want provider default override", sw.Reasoning)
	}
	if got, want := m.sidebar.reasoning, "provider default"; got != want {
		t.Fatalf("sidebar.reasoning = %q, want %q", got, want)
	}
}

func TestReasoningPickerEscReturnsToModelList(t *testing.T) {
	m := newModel(Config{
		Model:      "small",
		ModelNames: []string{"small", "large"},
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"large": {SupportedEfforts: []string{"low", "medium"}},
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if !m.reasoningPicker.IsOpen() {
		t.Fatal("expected reasoningPicker to be open before Esc")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.reasoningPicker.IsOpen() {
		t.Fatal("reasoningPicker.IsOpen() = true, want false after Esc")
	}
	if !m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = false, want true after Esc returns to model list")
	}
	if got, want := m.primaryModel, "small"; got != want {
		t.Fatalf("primaryModel = %q, want %q — Esc must not commit a switch", got, want)
	}
}

func TestModelPickerWorkflowHandoffSkipsReasoningStep(t *testing.T) {
	m := newModel(Config{
		Model:      "small",
		ModelNames: []string{"small", "large"},
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"large": {SupportedEfforts: []string{"low", "medium"}},
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m.modelPicker = m.modelPicker.OpenForWorkflowHandoff("Select model", []string{"small", "large"}, "small")
	m.modelPicker.width = 100
	m.modelPicker.height = 20

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.reasoningPicker.IsOpen() {
		t.Fatal("reasoningPicker.IsOpen() = true, want false for workflow handoff selection")
	}
	if m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = true, want false after workflow handoff selection")
	}
	if got, want := m.workflowHandoff.modelAlias, "large"; got != want {
		t.Fatalf("workflowHandoff.modelAlias = %q, want %q", got, want)
	}
}

func TestModelPickerEnterFallsBackToOnDemandResolveWhenBatchPending(t *testing.T) {
	ctrl := &testController{}
	resolveCalls := 0

	m := newModel(Config{
		Model:      "small",
		ModelNames: []string{"small", "large"},
		Controller: ctrl,
		// ResolveReasoningFunc is set (batch resolution pending) but hasn't
		// completed yet, so modelReasoningCapabilities starts empty — this
		// mirrors the real startup window before modelReasoningResolvedMsg
		// arrives.
		ResolveReasoningFunc: func() (map[string]provider.ReasoningCapabilities, map[string]string) {
			return nil, nil
		},
		ResolveReasoningForAliasFunc: func(alias string) (provider.ReasoningCapabilities, string) {
			resolveCalls++
			if alias == "large" {
				return provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "medium", "high"}}, ""
			}
			return provider.ReasoningCapabilities{}, ""
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	if m.reasoningBatchResolved {
		t.Fatal("reasoningBatchResolved = true, want false before modelReasoningResolvedMsg arrives")
	}

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // select "large"
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if resolveCalls != 1 {
		t.Fatalf("ResolveReasoningForAliasFunc called %d times, want 1", resolveCalls)
	}
	if m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = true, want false once reasoning step opens")
	}
	if !m.reasoningPicker.IsOpen() {
		t.Fatal("reasoningPicker.IsOpen() = false, want true — on-demand resolve should have found reasoning support")
	}
}

func TestModelPickerEnterSkipsOnDemandResolveOnceBatchResolved(t *testing.T) {
	ctrl := &testController{}
	resolveCalls := 0

	m := newModel(Config{
		Model:      "small",
		ModelNames: []string{"small", "large"},
		Controller: ctrl,
		ResolveReasoningFunc: func() (map[string]provider.ReasoningCapabilities, map[string]string) {
			return nil, nil
		},
		ResolveReasoningForAliasFunc: func(_ string) (provider.ReasoningCapabilities, string) {
			resolveCalls++
			return provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "high"}}, ""
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	// Batch resolution completes with no reasoning support for "large" before
	// the user makes a selection.
	m = updateModel(t, m, modelReasoningResolvedMsg{
		capabilities: map[string]provider.ReasoningCapabilities{"large": {}},
		efforts:      map[string]string{},
	})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // select "large"
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if resolveCalls != 0 {
		t.Fatalf("ResolveReasoningForAliasFunc called %d times, want 0 once batch has resolved", resolveCalls)
	}
	if m.reasoningPicker.IsOpen() {
		t.Fatal("reasoningPicker.IsOpen() = true, want false — batch resolution already confirmed no reasoning support")
	}
}

func lastSwitchModelAction(t *testing.T, ctrl *testController) (interactive.SwitchModel, bool) {
	t.Helper()
	for i := len(ctrl.actions) - 1; i >= 0; i-- {
		if sw, ok := ctrl.actions[i].(interactive.SwitchModel); ok {
			return sw, true
		}
	}
	return interactive.SwitchModel{}, false
}
