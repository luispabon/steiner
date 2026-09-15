package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestBuiltinSourceResolve(t *testing.T) {
	tests := []struct {
		name           string
		providerType   config.ProviderType
		backendModelID string
		wantKnown      bool
		wantEfforts    []string
	}{
		{
			name:           "openai gpt-5 family gets fallback efforts",
			providerType:   config.ProviderTypeOpenAI,
			backendModelID: "gpt-5-turbo",
			wantKnown:      true,
			wantEfforts:    []string{"minimal", "low", "medium", "high"},
		},
		{
			name:           "codex o3 family gets fallback efforts",
			providerType:   config.ProviderTypeCodex,
			backendModelID: "o3-mini",
			wantKnown:      true,
			wantEfforts:    []string{"minimal", "low", "medium", "high"},
		},
		{
			name:           "non-openai provider gets no fallback",
			providerType:   config.ProviderTypeAnthropic,
			backendModelID: "claude-3",
			wantKnown:      false,
		},
		{
			name:           "openai unrecognized family gets no fallback",
			providerType:   config.ProviderTypeOpenAI,
			backendModelID: "gpt-3.5-turbo",
			wantKnown:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := modelRef{Provider: config.ProviderConfig{Type: tt.providerType}, BackendModelID: tt.backendModelID}
			res := builtinSource{}.resolve(context.Background(), ref, fieldSet(fieldEfforts))
			if res.facts.ReasoningEfforts.Known != tt.wantKnown {
				t.Fatalf("ReasoningEfforts.Known = %v, want %v", res.facts.ReasoningEfforts.Known, tt.wantKnown)
			}
			if tt.wantKnown {
				if !reflect.DeepEqual(res.facts.ReasoningEfforts.Value, tt.wantEfforts) {
					t.Errorf("ReasoningEfforts.Value = %v, want %v", res.facts.ReasoningEfforts.Value, tt.wantEfforts)
				}
				if res.facts.ReasoningEfforts.Source != FactSourceFallback || res.facts.ReasoningEfforts.Confidence != "low" {
					t.Errorf("ReasoningEfforts = %+v, want fallback/low", res.facts.ReasoningEfforts)
				}
			}
		})
	}
}
