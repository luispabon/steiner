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

	got, indexes := fuzzyMatchModelEntries(entries, "alpha")
	if len(got) != 1 || got[0].Ref != "provider/alpha" || len(indexes) != 1 || len(indexes[0]) == 0 {
		t.Fatalf("alpha match = %+v, indexes = %+v, want Alpha model entry and indexes", got, indexes)
	}
	got, indexes = fuzzyMatchModelEntries(entries, "beta")
	if len(got) != 0 || len(indexes) != 0 {
		t.Fatalf("beta match = %+v, indexes = %+v, want no display match", got, indexes)
	}
	got, indexes = fuzzyMatchModelEntries(entries, "provider")
	if len(got) != 1 || got[0].Ref != "beta" || len(indexes) != 1 || len(indexes[0]) == 0 {
		t.Fatalf("provider match = %+v, indexes = %+v, want Provider model entry and indexes", got, indexes)
	}
}

func TestFuzzyMatchModelEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		entries []ModelEntry
		query   string
		want    []string
	}{
		{
			name: "subsequence",
			entries: []ModelEntry{
				{Ref: "foobar", Display: "foobar"},
				{Ref: "fuzzy", Display: "fuzzy foo"},
			},
			query: "fbr",
			want:  []string{"foobar"},
		},
		{
			name: "ranking",
			entries: []ModelEntry{
				{Ref: "later", Display: "mapple"},
				{Ref: "first", Display: "apple"},
			},
			query: "apple",
			want:  []string{"first", "later"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, indexes := fuzzyMatchModelEntries(tt.entries, tt.query)
			if len(got) != len(tt.want) || len(indexes) != len(tt.want) {
				t.Fatalf("matches = %+v, indexes = %+v, want refs %v", got, indexes, tt.want)
			}
			for i, wantRef := range tt.want {
				if got[i].Ref != wantRef {
					t.Errorf("match %d ref = %q, want %q", i, got[i].Ref, wantRef)
				}
				name := got[i].Display
				if name == "" {
					name = got[i].Ref
				}
				if len(indexes[i]) == 0 {
					t.Errorf("match %d indexes are empty", i)
				}
				for _, index := range indexes[i] {
					if index < 0 || index >= len(name) {
						t.Errorf("match %d index %d outside rendered name %q", i, index, name)
					}
				}
			}
		})
	}
}

func TestFuzzyMatchModelEntriesEmptyQuery(t *testing.T) {
	t.Parallel()
	entries := []ModelEntry{
		{Ref: "first", Display: "First"},
		{Ref: "second", Display: "Second"},
	}
	got, indexes := fuzzyMatchModelEntries(entries, "  ")
	if len(got) != len(entries) || len(indexes) != len(entries) {
		t.Fatalf("matches = %+v, indexes = %+v, want all entries and indexes", got, indexes)
	}
	for i := range entries {
		if got[i].Ref != entries[i].Ref {
			t.Errorf("match %d ref = %q, want %q", i, got[i].Ref, entries[i].Ref)
		}
		if len(indexes[i]) != 0 {
			t.Errorf("match %d indexes = %v, want empty", i, indexes[i])
		}
	}
}

func TestModelPickerRenderWithNilMatchIndexes(t *testing.T) {
	t.Parallel()
	s := testStyles("#ff0000")
	m := newModelPickerOverlay(s)
	m = m.OpenEntries([]ModelEntry{
		{Ref: "one", Display: "First"},
		{Ref: "two", Display: "Second"},
	}, "one")

	if m.matchIndexes != nil {
		t.Fatalf("matchIndexes = %v, want nil after open", m.matchIndexes)
	}
	if len(m.candidates) == 0 {
		t.Fatal("candidates are empty after open")
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "First") {
		t.Fatalf("initial view = %q, want First", got)
	}

	m, _ = m.Update(tea.KeyPressMsg{Text: "s"})
	if got := stripANSI(m.View()); !strings.Contains(got, "Second") {
		t.Fatalf("filtered view = %q, want Second", got)
	}
}

func TestFuzzyMatchModelEntriesIndexRowsIndependent(t *testing.T) {
	t.Parallel()
	entries := []ModelEntry{
		{Ref: "a", Display: "apple"},
		{Ref: "b", Display: "mapple"},
	}
	_, rows := fuzzyMatchModelEntries(entries, "apple")
	if len(rows) < 2 {
		t.Fatalf("index rows = %v, want at least two matches", rows)
	}
	if len(rows[0]) == 0 || len(rows[1]) == 0 {
		t.Fatalf("index rows = %v, want non-empty rows", rows)
	}

	second := rows[1][0]
	rows[0][0] = -1
	if rows[1][0] != second {
		t.Fatalf("mutating first row changed second row: got %d, want %d", rows[1][0], second)
	}
	rows[1][0] = -2
	if rows[0][0] != -1 {
		t.Fatalf("mutating second row changed first row: got %d, want -1", rows[0][0])
	}
}

func TestFuzzyMatchModelEntriesUsesDisplayOnly(t *testing.T) {
	t.Parallel()
	entry := ModelEntry{Ref: "provider/alpha", Display: "Alpha model"}
	got, indexes := fuzzyMatchModelEntries([]ModelEntry{entry}, "alpha")
	if len(got) != 1 || got[0].Ref != entry.Ref || got[0].Display != entry.Display || len(indexes) != 1 || len(indexes[0]) == 0 {
		t.Fatalf("alpha match = %+v, indexes = %+v, want display match", got, indexes)
	}
	got, indexes = fuzzyMatchModelEntries([]ModelEntry{entry}, "provider")
	if len(got) != 0 || len(indexes) != 0 {
		t.Fatalf("provider match = %+v, indexes = %+v, want no ref match", got, indexes)
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
	m.modelPicker.candidates, _ = fuzzyMatchModelEntries(m.modelPicker.allEntries, m.modelPicker.query)
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
