package interactive

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

func testReasoningSession(t *testing.T) *Session {
	return testNewSession(t, Dependencies{
		Config: config.Config{
			Models: config.ModelsConfig{
				Effective: config.EffectiveModelAssignments{DefaultModel: "current"},
				Definitions: map[string]config.ModelConfig{
					"current": {Provider: "local", ID: "current-id"},
					"other":   {Provider: "local", ID: "other-id"},
				},
			},
			Providers: map[string]config.ProviderConfig{"local": {}},
		},
	})
}

func TestHandleSwitchModelReasoningOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		seed           *provider.ReasoningOverride
		reasoning      *provider.ReasoningOverride
		wantOverride   provider.ReasoningOverride
		wantOverrideOK bool
	}{
		{
			name:           "nil reasoning leaves no prior override untouched",
			reasoning:      nil,
			wantOverride:   provider.ReasoningOverride{},
			wantOverrideOK: false,
		},
		{
			name: "nil reasoning preserves an existing override",
			seed: &provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "low"},
			wantOverride: provider.ReasoningOverride{
				Kind:   provider.ReasoningOverrideEffort,
				Effort: "low",
			},
			wantOverrideOK: true,
			reasoning:      nil,
		},
		{
			name:      "provider default override is stored",
			reasoning: &provider.ReasoningOverride{Kind: provider.ReasoningOverrideProviderDefault},
			wantOverride: provider.ReasoningOverride{
				Kind: provider.ReasoningOverrideProviderDefault,
			},
			wantOverrideOK: true,
		},
		{
			name:      "explicit effort override is stored",
			reasoning: &provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "high"},
			wantOverride: provider.ReasoningOverride{
				Kind:   provider.ReasoningOverrideEffort,
				Effort: "high",
			},
			wantOverrideOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := testReasoningSession(t)

			if tt.seed != nil {
				if err := s.handleSwitchModel("current", tt.seed); err != nil {
					t.Fatalf("seed handleSwitchModel = %v, want nil", err)
				}
			}

			if err := s.handleSwitchModel("current", tt.reasoning); err != nil {
				t.Fatalf("handleSwitchModel = %v, want nil", err)
			}

			s.mu.RLock()
			got, ok := s.reasoningOverrides["current"]
			s.mu.RUnlock()

			if ok != tt.wantOverrideOK {
				t.Fatalf("reasoningOverrides[current] present = %v, want %v", ok, tt.wantOverrideOK)
			}
			if got != tt.wantOverride {
				t.Fatalf("reasoningOverrides[current] = %+v, want %+v", got, tt.wantOverride)
			}
		})
	}
}

func TestCurrentReasoningOverrideZeroValue(t *testing.T) {
	t.Parallel()
	s := testReasoningSession(t)

	got := s.CurrentReasoningOverride()
	want := provider.ReasoningOverride{}
	if got != want {
		t.Fatalf("CurrentReasoningOverride() = %+v, want zero value %+v", got, want)
	}
}

func TestCurrentReasoningOverridePreservedAcrossSwitches(t *testing.T) {
	t.Parallel()
	s := testReasoningSession(t)
	ctx := context.Background()

	want := provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "high"}
	if err := s.Handle(ctx, SwitchModel{Name: "current", Reasoning: &want}); err != nil {
		t.Fatalf("Handle(SwitchModel current) = %v, want nil", err)
	}

	if got := s.CurrentReasoningOverride(); got != want {
		t.Fatalf("CurrentReasoningOverride() after set = %+v, want %+v", got, want)
	}

	if err := s.Handle(ctx, SwitchModel{Name: "other"}); err != nil {
		t.Fatalf("Handle(SwitchModel other) = %v, want nil", err)
	}
	if got := (provider.ReasoningOverride{}); s.CurrentReasoningOverride() != got {
		t.Fatalf("CurrentReasoningOverride() for other = %+v, want zero value", s.CurrentReasoningOverride())
	}

	if err := s.Handle(ctx, SwitchModel{Name: "current"}); err != nil {
		t.Fatalf("Handle(SwitchModel back to current) = %v, want nil", err)
	}

	if got := s.CurrentReasoningOverride(); got != want {
		t.Fatalf("CurrentReasoningOverride() after switching back = %+v, want preserved override %+v", got, want)
	}
}
