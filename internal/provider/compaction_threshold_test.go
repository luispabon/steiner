package provider

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestResolveCompactionThresholdTokens(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"configured": {
				Provider: "local",
				ID:       "model",
				Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{
					ContextWindow:             128000,
					CompactionThresholdTokens: 12000,
				}},
			},
		}},
	}

	rm, err := resolveReference(&cfg, "configured", false, nil)
	if err != nil {
		t.Fatalf("resolveReference() error = %v", err)
	}
	if got, want := rm.EffectiveLimits.CompactionThresholdTokens, 12000; got != want {
		t.Fatalf("CompactionThresholdTokens = %d, want %d", got, want)
	}
}

func TestResolveRejectsCompactionThresholdAtContextWindow(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"invalid": {
				Provider: "local",
				ID:       "model",
				Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{
					ContextWindow:             128000,
					CompactionThresholdTokens: 128000,
				}},
			},
		}},
	}

	_, err := resolveReference(&cfg, "invalid", false, nil)
	if err == nil || !strings.Contains(err.Error(), "compaction_threshold_tokens 128000 must be less than effective context window 128000") {
		t.Fatalf("resolveReference() error = %v, want descriptive threshold error", err)
	}
}
