package provider

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestModelsDevProviderID(t *testing.T) {
	tests := []struct {
		providerType config.ProviderType
		want         string
	}{
		{config.ProviderTypeOpenAI, "openai"},
		{config.ProviderTypeCodex, "openai"},
		{config.ProviderTypeAnthropic, "anthropic"},
		{config.ProviderTypeGemini, "google"},
		{config.ProviderTypeOpenRouter, "openrouter"},
		{config.ProviderTypeOpencodeGo, "opencode-go"},
		{config.ProviderTypeOpencodeZen, "opencode"},
		{config.ProviderTypeLMStudio, "lmstudio"},
		{config.ProviderTypeOpenAICompat, "my-alias"},
		{config.ProviderTypeOllama, "my-alias"},
		{config.ProviderTypeLiteLLM, "my-alias"},
		{"", "my-alias"},
	}
	for _, tt := range tests {
		t.Run(string(tt.providerType), func(t *testing.T) {
			if got := modelsDevProviderID(tt.providerType, "my-alias"); got != tt.want {
				t.Fatalf("modelsDevProviderID(%q) = %q, want %q", tt.providerType, got, tt.want)
			}
		})
	}
}

func TestLoadAndApplyMetadataMapsCodexAliasToOpenAI(t *testing.T) {
	data := []byte(`{
		"302ai":{"models":{"gpt-5.6-luna":{"limit":{"context":1050000,"output":128000}}}},
		"abacus":{"models":{"gpt-5.6-luna":{"limit":{"context":1000000,"output":128000}}}},
		"openai":{"models":{"gpt-5.6-luna":{"limit":{"context":1050000,"output":128000}}}}
	}`)
	for _, alias := range []string{"codex", "luna", "openai"} {
		t.Run(alias, func(t *testing.T) {
			rm := ResolvedModel{
				ProviderAlias:  alias,
				ProviderConfig: config.ProviderConfig{Type: config.ProviderTypeCodex},
				BackendModelID: "gpt-5.6-luna",
			}
			info := loadAndApplyModelsDevMetadataFromData(&rm, config.ModelConfig{}, data)
			if !info.Found || info.ContextWindow != 1050000 || info.MaxOutputTokens != 128000 {
				t.Fatalf("info = %+v, want openai gpt-5.6-luna limits", info)
			}
			resolveLimitsFromDiscovery(&rm, config.AdvancedLimitsConfig{}, info, nil, true, "luna")
			if rm.EffectiveLimits.ContextWindow != 1050000 {
				t.Fatalf("EffectiveLimits.ContextWindow = %d, want 1050000", rm.EffectiveLimits.ContextWindow)
			}
			if len(rm.Warnings) != 0 {
				t.Fatalf("Warnings = %v, want none", rm.Warnings)
			}
		})
	}
}
