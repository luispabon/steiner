package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

func TestPrintModelInspect(t *testing.T) {
	tests := []struct {
		name      string
		rm        provider.ResolvedModel
		wantLines []string
	}{
		{
			name: "configured transport no override",
			rm: provider.ResolvedModel{
				Alias:                   "mymodel",
				ProviderAlias:           "local",
				ProviderConfig:          config.ProviderConfig{Type: config.ProviderTypeOpenAICompat},
				BackendModelID:          "llama3",
				EffectiveProviderType:   config.ProviderTypeOpenAICompat,
				EffectiveTransport:      provider.TransportConfigured,
				MetadataSource:          "config",
				Confidence:              "high",
				TransportOverrideReason: "none",
				EffectiveLimits: provider.EffectiveLimits{
					ContextWindow:             32768,
					MaxOutputTokens:           4096,
					CompactionThreshold:       0.70,
					EstimatorPadTokens:        327,
					NormalSummaryMaxTokens:    4096,
					EmergencySummaryMaxTokens: 2048,
				},
				Params:              map[string]any{},
				ExtraParams:         map[string]any{},
				PromptSuffix:        "",
				TokenizerStrategy:   provider.TokenizerStrategyHeuristic,
				TokenizerConfidence: "low",
			},
			wantLines: []string{
				"alias: mymodel",
				"provider: local",
				"backend_id: llama3",
				"confidence: high",
				"configured_provider_type: openai_compat",
				"effective_provider_type: openai_compat",
				"effective_transport: configured",
				"metadata_source: config",
				"transport_override_reason: none",
				"limits:",
				"  source: config",
				"  confidence: high",
				"  context_window: 32768",
				"  max_output_tokens: 4096",
				"derived_policy:",
				"  compaction_threshold: 0.70",
				"  estimator_pad_tokens: 327",
				"  normal_summary_token_budget: 4096",
				"  emergency_summary_token_budget: 2048",
				"params: {}",
				"extra_params: {}",
				"prompt_suffix: \"\"",
				"tokenizer:",
				"  strategy: heuristic",
				"  confidence: low",
			},
		},
		{
			name: "models dev override",
			rm: provider.ResolvedModel{
				Alias:                   "minimax",
				ProviderAlias:           "opencode-go",
				ProviderConfig:          config.ProviderConfig{Type: config.ProviderTypeOpenAICompat},
				BackendModelID:          "minimax-m3",
				EffectiveProviderType:   config.ProviderTypeAnthropic,
				EffectiveTransport:      provider.TransportAnthropic,
				MetadataSource:          "models.dev",
				Confidence:              "medium",
				TransportOverrideReason: "models.dev provider override for minimax-m3",
				EffectiveLimits: provider.EffectiveLimits{
					ContextWindow:             256000,
					MaxOutputTokens:           8192,
					CompactionThreshold:       0.70,
					EstimatorPadTokens:        2560,
					NormalSummaryMaxTokens:    8192,
					EmergencySummaryMaxTokens: 4096,
				},
				Params:              map[string]any{},
				ExtraParams:         map[string]any{},
				PromptSuffix:        "",
				TokenizerStrategy:   provider.TokenizerStrategyTiktoken,
				TokenizerConfidence: "high",
				Warnings:            []string{"some warning"},
			},
			wantLines: []string{
				"alias: minimax",
				"provider: opencode-go",
				"backend_id: minimax-m3",
				"confidence: medium",
				"configured_provider_type: openai_compat",
				"effective_provider_type: anthropic",
				"effective_transport: anthropic",
				"metadata_source: models.dev",
				"transport_override_reason: models.dev provider override for minimax-m3",
				"limits:",
				"  source: models.dev",
				"  confidence: medium",
				"  context_window: 256000",
				"  max_output_tokens: 8192",
				"warnings:",
				"  - some warning",
			},
		},
		{
			name: "explicit config override",
			rm: provider.ResolvedModel{
				Alias:                   "kimi",
				ProviderAlias:           "opencode-go",
				ProviderConfig:          config.ProviderConfig{Type: config.ProviderTypeOpenAICompat},
				BackendModelID:          "kimi-k2.6",
				EffectiveProviderType:   config.ProviderTypeAnthropic,
				EffectiveTransport:      provider.TransportAnthropic,
				MetadataSource:          "config",
				Confidence:              "high",
				TransportOverrideReason: "explicit config override",
				EffectiveLimits: provider.EffectiveLimits{
					ContextWindow:             128000,
					MaxOutputTokens:           8192,
					CompactionThreshold:       0.70,
					EstimatorPadTokens:        1280,
					NormalSummaryMaxTokens:    8192,
					EmergencySummaryMaxTokens: 4096,
				},
				Params:              map[string]any{},
				ExtraParams:         map[string]any{},
				PromptSuffix:        "",
				TokenizerStrategy:   provider.TokenizerStrategyTiktoken,
				TokenizerConfidence: "high",
			},
			wantLines: []string{
				"configured_provider_type: openai_compat",
				"effective_provider_type: anthropic",
				"effective_transport: anthropic",
				"transport_override_reason: explicit config override",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := printModelInspect(&buf, tt.rm); err != nil {
				t.Fatalf("printModelInspect() error = %v", err)
			}
			got := buf.String()
			for _, want := range tt.wantLines {
				if !strings.Contains(got, want) {
					t.Errorf("output missing expected line: %q\nfull output:\n%s", want, got)
				}
			}
		})
	}
}
