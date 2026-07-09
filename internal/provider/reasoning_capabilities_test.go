package provider

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestResolveReasoningCapabilities(t *testing.T) {
	tests := []struct {
		name           string
		reasoningCfg   config.ReasoningConfig
		providerType   config.ProviderType
		backendModelID string
		wantEfforts    []string
		wantSource     string
		wantConfidence string
		wantEffective  string
	}{
		{
			name:           "config supported_efforts wins over fallback",
			reasoningCfg:   config.ReasoningConfig{SupportedEfforts: []string{"low", "high"}},
			providerType:   config.ProviderTypeOpenAI,
			backendModelID: "gpt-5",
			wantEfforts:    []string{"low", "high"},
			wantSource:     "config",
			wantConfidence: "high",
		},
		{
			name:           "openai gpt-5 family gets fallback efforts",
			providerType:   config.ProviderTypeOpenAI,
			backendModelID: "gpt-5-turbo",
			wantEfforts:    []string{"minimal", "low", "medium", "high"},
			wantSource:     "fallback",
			wantConfidence: "low",
		},
		{
			name:           "codex o3 family gets fallback efforts",
			providerType:   config.ProviderTypeCodex,
			backendModelID: "o3-mini",
			wantEfforts:    []string{"minimal", "low", "medium", "high"},
			wantSource:     "fallback",
			wantConfidence: "low",
		},
		{
			name:           "non-openai provider gets no fallback",
			providerType:   config.ProviderTypeAnthropic,
			backendModelID: "claude-3",
			wantEfforts:    nil,
			wantSource:     "unknown",
			wantConfidence: "unknown",
		},
		{
			name:           "openai unrecognized family gets no fallback",
			providerType:   config.ProviderTypeOpenAI,
			backendModelID: "gpt-3.5-turbo",
			wantEfforts:    nil,
			wantSource:     "unknown",
			wantConfidence: "unknown",
		},
		{
			name:           "configured effort becomes effective effort",
			reasoningCfg:   config.ReasoningConfig{Effort: "high"},
			providerType:   config.ProviderTypeOpenAI,
			backendModelID: "gpt-5",
			wantEfforts:    []string{"minimal", "low", "medium", "high"},
			wantSource:     "fallback",
			wantConfidence: "low",
			wantEffective:  "high",
		},
		{
			name:           "no configured effort leaves effective effort empty",
			providerType:   config.ProviderTypeOpenAI,
			backendModelID: "gpt-5",
			wantEfforts:    []string{"minimal", "low", "medium", "high"},
			wantSource:     "fallback",
			wantConfidence: "low",
			wantEffective:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps, effective := resolveReasoningCapabilities(tt.reasoningCfg, tt.providerType, tt.backendModelID)
			if !equalStrings(caps.SupportedEfforts, tt.wantEfforts) {
				t.Errorf("SupportedEfforts=%v, want %v", caps.SupportedEfforts, tt.wantEfforts)
			}
			if caps.Source != tt.wantSource {
				t.Errorf("Source=%q, want %q", caps.Source, tt.wantSource)
			}
			if caps.Confidence != tt.wantConfidence {
				t.Errorf("Confidence=%q, want %q", caps.Confidence, tt.wantConfidence)
			}
			if effective != tt.wantEffective {
				t.Errorf("effective effort=%q, want %q", effective, tt.wantEffective)
			}
		})
	}
}

func TestApplyReasoningOverride(t *testing.T) {
	base := ResolvedModel{
		Alias: "mymodel",
		Reasoning: ReasoningCapabilities{
			SupportedEfforts: []string{"low", "medium", "high"},
		},
		ReasoningEffectiveEffort: "medium",
	}

	unknownCaps := ResolvedModel{
		Alias:                    "othermodel",
		Reasoning:                ReasoningCapabilities{},
		ReasoningEffectiveEffort: "",
	}

	tests := []struct {
		name       string
		rm         ResolvedModel
		override   ReasoningOverride
		wantErr    bool
		wantEffort string
	}{
		{
			name:       "no override leaves effective effort unchanged",
			rm:         base,
			override:   ReasoningOverride{Kind: ReasoningOverrideNone},
			wantEffort: "medium",
		},
		{
			name:       "zero value override leaves effective effort unchanged",
			rm:         base,
			override:   ReasoningOverride{},
			wantEffort: "medium",
		},
		{
			name:       "provider default override clears configured effort",
			rm:         base,
			override:   ReasoningOverride{Kind: ReasoningOverrideProviderDefault},
			wantEffort: "",
		},
		{
			name:       "effort override to supported value",
			rm:         base,
			override:   ReasoningOverride{Kind: ReasoningOverrideEffort, Effort: "high"},
			wantEffort: "high",
		},
		{
			name:     "effort override to unsupported value errors",
			rm:       base,
			override: ReasoningOverride{Kind: ReasoningOverrideEffort, Effort: "xhigh"},
			wantErr:  true,
		},
		{
			name:       "effort override allowed when supported list unknown",
			rm:         unknownCaps,
			override:   ReasoningOverride{Kind: ReasoningOverrideEffort, Effort: "xhigh"},
			wantEffort: "xhigh",
		},
		{
			name:     "unknown override kind errors",
			rm:       base,
			override: ReasoningOverride{Kind: "bogus"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyReasoningOverride(tt.rm, tt.override)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ReasoningEffectiveEffort != tt.wantEffort {
				t.Errorf("ReasoningEffectiveEffort=%q, want %q", got.ReasoningEffectiveEffort, tt.wantEffort)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
