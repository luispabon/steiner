package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestReasoningPickerOpenBuildsOptions(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	caps := provider.ReasoningCapabilities{
		SupportedEfforts:      []string{"low", "medium", "high"},
		ProviderDefaultEffort: "medium",
	}

	p := newReasoningPickerOverlay(styles).Open("gpt-5", caps, provider.ReasoningOverride{}, "")

	if !p.IsOpen() {
		t.Fatal("expected picker to be open")
	}
	if p.modelName != "gpt-5" {
		t.Fatalf("expected modelName gpt-5, got %q", p.modelName)
	}
	// provider default + 3 concrete efforts
	if len(p.candidates) != 4 {
		t.Fatalf("expected 4 candidates, got %d", len(p.candidates))
	}
	if !p.candidates[0].IsProviderDefault {
		t.Fatalf("expected first candidate to be provider default, got %+v", p.candidates[0])
	}
	wantValues := []string{"", "low", "medium", "high"}
	for i, want := range wantValues {
		if p.candidates[i].Value != want {
			t.Errorf("candidate[%d].Value = %q, want %q", i, p.candidates[i].Value, want)
		}
	}
}

func TestReasoningPickerOmitsUnsupportedEfforts(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	caps := provider.ReasoningCapabilities{SupportedEfforts: []string{"medium", "high"}}

	p := newReasoningPickerOverlay(styles).Open("gpt-5", caps, provider.ReasoningOverride{}, "")

	for _, opt := range p.candidates {
		if opt.Value == "none" {
			t.Fatalf("did not expect unsupported effort 'none' in candidates: %+v", p.candidates)
		}
	}
}

func TestReasoningPickerMarksCurrentEffort(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	caps := provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "medium", "high"}}
	current := provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "high"}

	p := newReasoningPickerOverlay(styles).Open("gpt-5", caps, current, "")

	opt, ok := p.SelectedOption()
	if !ok {
		t.Fatal("expected a selected option")
	}
	if !opt.IsCurrent || opt.Value != "high" {
		t.Fatalf("expected selection to land on current effort 'high', got %+v", opt)
	}
}

func TestReasoningPickerMarksCurrentProviderDefault(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	caps := provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "medium"}}
	current := provider.ReasoningOverride{Kind: provider.ReasoningOverrideProviderDefault}

	p := newReasoningPickerOverlay(styles).Open("gpt-5", caps, current, "")

	opt, ok := p.SelectedOption()
	if !ok {
		t.Fatal("expected a selected option")
	}
	if !opt.IsCurrent || !opt.IsProviderDefault {
		t.Fatalf("expected selection to land on provider default, got %+v", opt)
	}
}

func TestReasoningPickerMarksConfiguredEffortCurrentWhenNoOverride(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	caps := provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "medium", "high"}}

	p := newReasoningPickerOverlay(styles).Open("gpt-5", caps, provider.ReasoningOverride{}, "medium")

	opt, ok := p.SelectedOption()
	if !ok {
		t.Fatal("expected a selected option")
	}
	if !opt.IsCurrent || opt.Value != "medium" {
		t.Fatalf("expected selection to land on configured effort 'medium', got %+v", opt)
	}
}

func TestReasoningPickerMarksProviderDefaultCurrentWhenNoOverrideAndNoConfiguredEffort(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	caps := provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "medium", "high"}}

	p := newReasoningPickerOverlay(styles).Open("gpt-5", caps, provider.ReasoningOverride{}, "")

	opt, ok := p.SelectedOption()
	if !ok {
		t.Fatal("expected a selected option")
	}
	if !opt.IsCurrent || !opt.IsProviderDefault {
		t.Fatalf("expected selection to land on provider default, got %+v", opt)
	}
}

func TestReasoningPickerOverrideWinsOverConfiguredEffort(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	caps := provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "medium", "high"}}
	current := provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "high"}

	p := newReasoningPickerOverlay(styles).Open("gpt-5", caps, current, "medium")

	opt, ok := p.SelectedOption()
	if !ok {
		t.Fatal("expected a selected option")
	}
	if !opt.IsCurrent || opt.Value != "high" {
		t.Fatalf("expected session override to win over configured effort, got %+v", opt)
	}
}

func TestReasoningPickerFilter(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	caps := provider.ReasoningCapabilities{SupportedEfforts: []string{"low", "medium", "high"}}

	p := newReasoningPickerOverlay(styles).Open("gpt-5", caps, provider.ReasoningOverride{}, "")

	msg := tea.KeyPressMsg{Code: 'h', Text: "h"}
	p, _ = p.Update(msg)

	if len(p.candidates) != 1 {
		t.Fatalf("expected 1 candidate after filtering 'h', got %d: %+v", len(p.candidates), p.candidates)
	}
	if p.candidates[0].Value != "high" {
		t.Fatalf("expected candidate 'high', got %+v", p.candidates[0])
	}
}

func TestReasoningPickerCloseOnEsc(t *testing.T) {
	t.Parallel()
	styles := theme.BuildStyles(theme.AccentPresets["amber"])
	caps := provider.ReasoningCapabilities{SupportedEfforts: []string{"low"}}

	p := newReasoningPickerOverlay(styles).Open("gpt-5", caps, provider.ReasoningOverride{}, "")
	p, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	if p.IsOpen() {
		t.Fatal("expected picker to be closed after Esc")
	}
}

func TestReasoningOverrideFromOption(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opt  reasoningEffortOption
		want provider.ReasoningOverride
	}{
		{
			name: "provider default",
			opt:  reasoningEffortOption{IsProviderDefault: true},
			want: provider.ReasoningOverride{Kind: provider.ReasoningOverrideProviderDefault},
		},
		{
			name: "explicit effort",
			opt:  reasoningEffortOption{Value: "medium"},
			want: provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "medium"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := reasoningOverrideFromOption(tt.opt)
			if got != tt.want {
				t.Errorf("reasoningOverrideFromOption(%+v) = %+v, want %+v", tt.opt, got, tt.want)
			}
		})
	}
}

func TestReasoningOverrideLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		o    provider.ReasoningOverride
		want string
	}{
		{"effort", provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "medium"}, "medium"},
		{"provider default", provider.ReasoningOverride{Kind: provider.ReasoningOverrideProviderDefault}, "provider default"},
		{"none", provider.ReasoningOverride{Kind: provider.ReasoningOverrideNone}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := reasoningOverrideLabel(tt.o); got != tt.want {
				t.Errorf("reasoningOverrideLabel(%+v) = %q, want %q", tt.o, got, tt.want)
			}
		})
	}
}
