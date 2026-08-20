package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/provider"
)

func TestNewModelSeedsReasoningSidebarFromConfiguredEffort(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		Model:             "gpt-5-id",
		CurrentModelAlias: "large",
		ModelNames:        []string{"large"},
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"large": {SupportedEfforts: []string{"low", "medium", "high"}},
		},
		ModelReasoningEfforts: map[string]string{"large": "high"},
	}, nil)

	if got, want := m.sidebar.reasoning, "high"; got != want {
		t.Fatalf("sidebar.reasoning = %q, want %q", got, want)
	}
}

func TestNewModelSeedsReasoningSidebarProviderDefaultWhenNoConfiguredEffort(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		Model:             "gpt-5-id",
		CurrentModelAlias: "large",
		ModelNames:        []string{"large"},
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"large": {SupportedEfforts: []string{"low", "medium", "high"}},
		},
	}, nil)

	if got, want := m.sidebar.reasoning, "default"; got != want {
		t.Fatalf("sidebar.reasoning = %q, want %q", got, want)
	}
}

func TestNewModelLeavesReasoningSidebarAbsentForNonReasoningModel(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		Model:             "small-id",
		CurrentModelAlias: "small",
		ModelNames:        []string{"small"},
	}, nil)

	if got, want := m.sidebar.reasoning, ""; got != want {
		t.Fatalf("sidebar.reasoning = %q, want %q (absent for non-reasoning model)", got, want)
	}
}

func TestReasoningSidebarLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		effort string
		caps   provider.ReasoningCapabilities
		want   string
	}{
		{
			name:   "configured effort wins",
			effort: "high",
			caps:   provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "high"}},
			want:   "high",
		},
		{
			name:   "no effort but reasoning capable",
			effort: "",
			caps:   provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "high"}},
			want:   "default",
		},
		{
			name:   "no reasoning capability",
			effort: "",
			caps:   provider.ReasoningCapabilities{},
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := reasoningSidebarLabel(tt.effort, tt.caps); got != tt.want {
				t.Errorf("reasoningSidebarLabel(%q, %+v) = %q, want %q", tt.effort, tt.caps, got, tt.want)
			}
		})
	}
}

func TestReasoningPickerOpenMarksConfiguredEffortCurrentFromStartup(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		Model:             "small-id",
		CurrentModelAlias: "small",
		ModelNames:        []string{"small", "large"},
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"large": {SupportedEfforts: []string{"low", "medium", "high"}},
		},
		ModelReasoningEfforts: map[string]string{"large": "high"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown}) // select "large"
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	opt, ok := m.reasoningPicker.SelectedOption()
	if !ok {
		t.Fatal("expected a selected option")
	}
	if !opt.IsCurrent || opt.Value != "high" {
		t.Fatalf("expected picker to mark configured effort 'high' current, got %+v", opt)
	}
}

func TestCurrentReasoningOverrideForUsesModelAliasNotBackendID(t *testing.T) {
	t.Parallel()
	ctrl := &testController{reasoningOverride: provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "high"}}

	m := newModel(Config{
		Model:             "backend-model-id",
		CurrentModelAlias: "large",
		ModelNames:        []string{"large"},
		Controller:        ctrl,
	}, nil)

	if got := m.currentReasoningOverrideFor("backend-model-id"); got.Kind != "" {
		t.Fatalf("currentReasoningOverrideFor(backend id) = %+v, want zero value", got)
	}
	got := m.currentReasoningOverrideFor("large")
	if got.Kind != provider.ReasoningOverrideEffort || got.Effort != "high" {
		t.Fatalf("currentReasoningOverrideFor(alias) = %+v, want effort override 'high'", got)
	}
}
