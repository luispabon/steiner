package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestPromptCacheTTLDefaultsByProviderType(t *testing.T) {
	tests := []struct {
		name         string
		providerType ProviderType
		want         time.Duration
	}{
		{"anthropic", ProviderTypeAnthropic, 5 * time.Minute},
		{"claude_subscription", ProviderTypeClaudeSubscription, 60 * time.Minute},
		{"openai", ProviderTypeOpenAI, 30 * time.Minute},
		{"codex", ProviderTypeCodex, 30 * time.Minute},
		{"openai_compat", ProviderTypeOpenAICompat, 0},
		{"ollama", ProviderTypeOllama, 0},
		{"lmstudio", ProviderTypeLMStudio, 0},
		{"openrouter", ProviderTypeOpenRouter, 0},
		{"gemini", ProviderTypeGemini, 0},
		{"litellm", ProviderTypeLiteLLM, 0},
		{"opencode_go", ProviderTypeOpencodeGo, 0},
		{"opencode_zen", ProviderTypeOpencodeZen, 0},
		{"empty type", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Providers: map[string]ProviderConfig{"p": {Type: tt.providerType}}}
			if got := cfg.CompactionFallbackAfter("p"); got != tt.want {
				t.Fatalf("CompactionFallbackAfter() = %v, want %v", got, tt.want)
			}
			if got := cfg.PromptCacheTTL("p"); got != tt.want {
				t.Fatalf("PromptCacheTTL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPromptCacheTTLAndCompactionFallbackExplicitOverrides(t *testing.T) {
	tests := []struct {
		name     string
		provider ProviderConfig
		want     time.Duration
		wantTTL  time.Duration
	}{
		{"anthropic override", ProviderConfig{Type: ProviderTypeAnthropic, CompactionFallbackAfter: MustDuration("2m")}, 2 * time.Minute, 5 * time.Minute},
		{"anthropic explicit zero beats default", ProviderConfig{Type: ProviderTypeAnthropic, CompactionFallbackAfter: MustDuration("0s")}, 0, 5 * time.Minute},
		{"claude_subscription explicit zero beats default", ProviderConfig{Type: ProviderTypeClaudeSubscription, CompactionFallbackAfter: MustDuration("0s")}, 0, 60 * time.Minute},
		{"openai_compat override from zero default", ProviderConfig{Type: ProviderTypeOpenAICompat, CompactionFallbackAfter: MustDuration("10m")}, 10 * time.Minute, 0},
		{"unset fallback inherits prompt_cache_ttl", ProviderConfig{Type: ProviderTypeAnthropic, PromptCacheTTL: MustDuration("7m")}, 7 * time.Minute, 7 * time.Minute},
		{"explicit fallback override is independent", ProviderConfig{Type: ProviderTypeAnthropic, PromptCacheTTL: MustDuration("7m"), CompactionFallbackAfter: MustDuration("2m")}, 2 * time.Minute, 7 * time.Minute},
		{"prompt_cache_ttl explicit zero beats default", ProviderConfig{Type: ProviderTypeAnthropic, PromptCacheTTL: MustDuration("0s")}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Providers: map[string]ProviderConfig{"p": tt.provider}}
			if got := cfg.CompactionFallbackAfter("p"); got != tt.want {
				t.Fatalf("CompactionFallbackAfter() = %v, want %v", got, tt.want)
			}
			if got := cfg.PromptCacheTTL("p"); got != tt.wantTTL {
				t.Fatalf("PromptCacheTTL() = %v, want %v", got, tt.wantTTL)
			}
		})
	}
}

func TestCompactionFallbackAfterNilConfigAndUnknownAlias(t *testing.T) {
	var nilCfg *Config
	if got := nilCfg.CompactionFallbackAfter("anthropic"); got != 0 {
		t.Fatalf("nil config CompactionFallbackAfter() = %v, want 0", got)
	}
	if got := nilCfg.PromptCacheTTL("anthropic"); got != 0 {
		t.Fatalf("nil config PromptCacheTTL() = %v, want 0", got)
	}
	cfg := &Config{Providers: map[string]ProviderConfig{"claude": {Type: ProviderTypeAnthropic}}}
	if got := cfg.CompactionFallbackAfter("missing"); got != 0 {
		t.Fatalf("unknown alias CompactionFallbackAfter() = %v, want 0", got)
	}
	if got := cfg.PromptCacheTTL("missing"); got != 0 {
		t.Fatalf("unknown alias PromptCacheTTL() = %v, want 0", got)
	}
}

func TestCompactionFallbackAndPromptCacheTTLYAMLMalformedAndNegative(t *testing.T) {
	for _, doc := range []string{
		"type: anthropic\ncompaction_fallback_after: soon\n",
		"type: anthropic\nprompt_cache_ttl: soon\n",
	} {
		var malformed ProviderConfig
		if err := yaml.Unmarshal([]byte(doc), &malformed); err == nil {
			t.Fatalf("yaml.Unmarshal(%q) error = nil, want error", doc)
		}
	}

	tests := []struct {
		name     string
		key      string
		value    string
		wantProb string
	}{
		{"fallback zero is valid", "compaction_fallback_after", "0s", ""},
		{"fallback positive is valid", "compaction_fallback_after", "5m", ""},
		{"fallback negative is rejected", "compaction_fallback_after", "-1m", `providers["claude"].compaction_fallback_after must not be negative`},
		{"ttl zero is valid", "prompt_cache_ttl", "0s", ""},
		{"ttl positive is valid", "prompt_cache_ttl", "5m", ""},
		{"ttl negative is rejected", "prompt_cache_ttl", "-1m", `providers["claude"].prompt_cache_ttl must not be negative`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var provider ProviderConfig
			doc := "type: anthropic\napi_key_env: ANTHROPIC_API_KEY\n" + tt.key + ": " + tt.value + "\n"
			if err := yaml.Unmarshal([]byte(doc), &provider); err != nil {
				t.Fatalf("yaml.Unmarshal() error = %v", err)
			}
			var problems []string
			validateProvidersConfig(&problems, map[string]ProviderConfig{"claude": provider})
			joined := strings.Join(problems, "; ")
			if joined != tt.wantProb {
				t.Fatalf("problems = %q, want %q", joined, tt.wantProb)
			}
		})
	}
}

func TestCompactionFallbackLayeredPatches(t *testing.T) {
	cfg := Config{}
	layers := []struct {
		name         string
		doc          string
		wantAfter    time.Duration
		wantTTL      time.Duration
		wantFallback string
	}{
		{
			name:         "base layer sets value and profile",
			doc:          "providers:\n  claude:\n    type: anthropic\n    compaction_fallback_after: 2m\n    prompt_cache_ttl: 3m\nmodels:\n  profiles:\n    default:\n      compaction_fallback: fast\n",
			wantAfter:    2 * time.Minute,
			wantTTL:      3 * time.Minute,
			wantFallback: "fast",
		},
		{
			name:         "later layer sets explicit zero and leaves profile",
			doc:          "providers:\n  claude:\n    compaction_fallback_after: 0s\n",
			wantAfter:    0,
			wantTTL:      3 * time.Minute,
			wantFallback: "fast",
		},
		{
			name:         "layer without fields keeps explicit zero",
			doc:          "providers:\n  claude:\n    api_key_env: ANTHROPIC_API_KEY\nmodels:\n  profiles:\n    default:\n      default_model: default\n",
			wantAfter:    0,
			wantTTL:      3 * time.Minute,
			wantFallback: "fast",
		},
		{
			name:         "later layer sets explicit zero ttl and overrides fallback",
			doc:          "providers:\n  claude:\n    prompt_cache_ttl: 0s\nmodels:\n  profiles:\n    default:\n      compaction_fallback: smart\n",
			wantAfter:    0,
			wantTTL:      0,
			wantFallback: "smart",
		},
	}
	for _, layer := range layers {
		t.Run(layer.name, func(t *testing.T) {
			applyPatch(&cfg, decodeCompactionFallbackPatch(t, layer.doc))
			if got := cfg.CompactionFallbackAfter("claude"); got != layer.wantAfter {
				t.Fatalf("CompactionFallbackAfter() = %v, want %v", got, layer.wantAfter)
			}
			if got := cfg.PromptCacheTTL("claude"); got != layer.wantTTL {
				t.Fatalf("PromptCacheTTL() = %v, want %v", got, layer.wantTTL)
			}
			if got := cfg.Models.Profiles["default"].CompactionFallback; got != layer.wantFallback {
				t.Fatalf("CompactionFallback = %q, want %q", got, layer.wantFallback)
			}
		})
	}
}

func decodeCompactionFallbackPatch(t *testing.T, doc string) configPatch {
	t.Helper()
	var patch configPatch
	if err := yaml.Unmarshal([]byte(doc), &patch); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	return patch
}
