package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/provider"
)

func TestModelPickerEntriesFilterDisplayAndRef(t *testing.T) {
	t.Parallel()
	entries := []ModelEntry{
		{Ref: "provider/alpha", Display: "Alpha model"},
		{Ref: "beta", Display: "Provider model"},
	}
	if got := filterModelEntries(entries, "alpha"); len(got) != 1 || got[0].Ref != "provider/alpha" {
		t.Fatalf("display filter = %+v, want alpha entry", got)
	}
	if got := filterModelEntries(entries, "beta"); len(got) != 1 || got[0].Ref != "beta" {
		t.Fatalf("ref filter = %+v, want beta entry", got)
	}
}

func TestModelPickerEntriesSeedAndAsyncUpdates(t *testing.T) {
	t.Parallel()
	updates := make(chan []ModelEntry, 2)
	m := newModel(Config{
		Model:               "one",
		Entries:             []ModelEntry{{Ref: "one", Display: "Initial model", Current: true}},
		ModelEntriesUpdates: updates,
	}, nil)
	m.modelPicker = m.modelPicker.OpenEntries(m.modelPickerEntries(), m.primaryModel)
	if got := stripANSI(m.modelPicker.View()); !strings.Contains(got, "Initial model") {
		t.Fatalf("seed view = %q, want initial display", got)
	}

	updates <- []ModelEntry{{Ref: "one", Display: "First completion"}}
	updates <- []ModelEntry{{Ref: "two", Display: "Second completion"}}
	cmd := waitForModelEntries(updates)
	msg := cmd()
	m = updateModel(t, m, msg)
	if got := m.modelEntries[0].Display; got != "First completion" {
		t.Fatalf("first update = %q, want first completion", got)
	}
	cmd = func() tea.Msg {
		return waitForModelEntries(updates)()
	}
	m = updateModel(t, m, cmd())
	if got := m.modelEntries[0].Display; got != "Second completion" {
		t.Fatalf("second update = %q, want second completion", got)
	}
	close(updates)
	_, cmd = m.Update(modelEntriesUpdatedMsg{entries: nil, ok: false})
	if cmd != nil {
		t.Fatal("closed update returned command, want no re-arm")
	}
}

func TestModelPickerAsyncUpdatePreservesQueryAndSelection(t *testing.T) {
	t.Parallel()
	updates := make(chan []ModelEntry, 1)
	m := newModel(Config{
		Model: "one",
		Entries: []ModelEntry{
			{Ref: "one", Display: "First"},
			{Ref: "two", Display: "Second"},
		},
		ModelEntriesUpdates: updates,
	}, nil)
	m.modelPicker = m.modelPicker.OpenEntries(m.modelPickerEntries(), "one")
	m.modelPicker, _ = m.modelPicker.Update(tea.KeyPressMsg{Text: "e"})
	m.modelPicker, _ = m.modelPicker.Update(tea.KeyPressMsg{Text: "e"})
	if got := m.modelPicker.query; got != "ee" {
		t.Fatalf("query before update = %q, want ee", got)
	}
	// Replace data while keeping selected ref available under the same query.
	m.modelPicker.query = "e"
	m.modelPicker.candidates = filterModelEntries(m.modelPicker.allEntries, m.modelPicker.query)
	m.modelPicker.selection = 1
	updates <- []ModelEntry{
		{Ref: "two", Display: "Updated second"},
		{Ref: "three", Display: "Third"},
	}
	m = updateModel(t, m, waitForModelEntries(updates)())
	if got := m.modelPicker.query; got != "e" {
		t.Fatalf("query after update = %q, want e", got)
	}
	if got := m.modelPicker.SelectedRef(); got != "two" {
		t.Fatalf("selection after update = %q, want two", got)
	}
	if got := stripANSI(m.modelPicker.View()); !strings.Contains(got, "Updated second") {
		t.Fatalf("updated view = %q, want updated row", got)
	}
}

func TestModelPickerEnterOnReasoningCapableModelOpensReasoningStep(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	// reasoningPicker: index 0 is "default", move down twice for "medium"
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
	t.Parallel()
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
	// First candidate is already "default"; commit without moving.
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	sw, ok := lastSwitchModelAction(t, ctrl)
	if !ok {
		t.Fatal("expected a SwitchModel action")
	}
	if sw.Reasoning == nil || sw.Reasoning.Kind != provider.ReasoningOverrideProviderDefault {
		t.Fatalf("Reasoning = %+v, want default override", sw.Reasoning)
	}
	if got, want := m.sidebar.reasoning, "default"; got != want {
		t.Fatalf("sidebar.reasoning = %q, want %q", got, want)
	}
}

func TestReasoningPickerEscReturnsToModelList(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
