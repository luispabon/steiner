package provider

import (
	"os"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

func TestProviderProfilesExhaustive(t *testing.T) {
	// If you add a new ProviderType constant, add it here too.
	allTypes := []config.ProviderType{
		config.ProviderTypeOpenAI,
		config.ProviderTypeCodex,
		config.ProviderTypeAnthropic,
		config.ProviderTypeGemini,
		config.ProviderTypeOpenRouter,
		config.ProviderTypeOpencodeGo,
		config.ProviderTypeOpencodeZen,
		config.ProviderTypeLMStudio,
		config.ProviderTypeOpenAICompat,
		config.ProviderTypeOllama,
		config.ProviderTypeLiteLLM,
	}

	for _, typ := range allTypes {
		t.Run(string(typ), func(t *testing.T) {
			p := profileFor(typ)
			// All known types must have an explicit entry in providerProfiles.
			if _, ok := providerProfiles[typ]; !ok {
				t.Fatalf("ProviderType %q not in providerProfiles map", typ)
			}

			// Profiles must be non-zero for known types.
			if p.Generic == false && p.ModelsDevID == "" && p.DefaultBaseURL == "" && p.FixedTransport == nil && !p.LiveProbe {
				t.Fatalf("profileFor(%q) returned zero-initialized profile", typ)
			}
		})
	}
}

func TestProviderProfileTable(t *testing.T) {
	tests := []struct {
		name               string
		typ                config.ProviderType
		wantModelsDevID    string
		wantBaseURL        string
		wantGeneric        bool
		wantFixedTransport *transportChoice
		wantLiveProbe      bool
	}{
		{
			name:               "openai",
			typ:                config.ProviderTypeOpenAI,
			wantModelsDevID:    "openai",
			wantBaseURL:        "https://api.openai.com/v1",
			wantGeneric:        false,
			wantFixedTransport: nil,
			wantLiveProbe:      false,
		},
		{
			name:            "codex",
			typ:             config.ProviderTypeCodex,
			wantModelsDevID: "openai",
			wantBaseURL:     "https://api.openai.com/v1",
			wantGeneric:     false,
			wantFixedTransport: &transportChoice{
				ProviderType: config.ProviderTypeCodex,
				Transport:    TransportConfigured,
				Reason:       "codex provider uses OAuth Responses transport",
			},
			wantLiveProbe: false,
		},
		{
			name:               "anthropic",
			typ:                config.ProviderTypeAnthropic,
			wantModelsDevID:    "anthropic",
			wantBaseURL:        "",
			wantGeneric:        false,
			wantFixedTransport: nil,
			wantLiveProbe:      false,
		},
		{
			name:               "gemini",
			typ:                config.ProviderTypeGemini,
			wantModelsDevID:    "google",
			wantBaseURL:        "",
			wantGeneric:        false,
			wantFixedTransport: nil,
			wantLiveProbe:      false,
		},
		{
			name:               "openrouter",
			typ:                config.ProviderTypeOpenRouter,
			wantModelsDevID:    "openrouter",
			wantBaseURL:        "https://openrouter.ai/api/v1",
			wantGeneric:        false,
			wantFixedTransport: nil,
			wantLiveProbe:      false,
		},
		{
			name:               "opencode_go",
			typ:                config.ProviderTypeOpencodeGo,
			wantModelsDevID:    "opencode-go",
			wantBaseURL:        "https://opencode.ai/zen/go/v1",
			wantGeneric:        false,
			wantFixedTransport: nil,
			wantLiveProbe:      false,
		},
		{
			name:               "opencode_zen",
			typ:                config.ProviderTypeOpencodeZen,
			wantModelsDevID:    "opencode",
			wantBaseURL:        "https://opencode.ai/zen/v1",
			wantGeneric:        false,
			wantFixedTransport: nil,
			wantLiveProbe:      false,
		},
		{
			name:               "lmstudio",
			typ:                config.ProviderTypeLMStudio,
			wantModelsDevID:    "lmstudio",
			wantBaseURL:        "",
			wantGeneric:        false,
			wantFixedTransport: nil,
			wantLiveProbe:      false,
		},
		{
			name:               "openai_compat",
			typ:                config.ProviderTypeOpenAICompat,
			wantModelsDevID:    "",
			wantBaseURL:        "",
			wantGeneric:        true,
			wantFixedTransport: nil,
			wantLiveProbe:      false,
		},
		{
			name:               "ollama",
			typ:                config.ProviderTypeOllama,
			wantModelsDevID:    "",
			wantBaseURL:        "",
			wantGeneric:        true,
			wantFixedTransport: nil,
			wantLiveProbe:      true,
		},
		{
			name:               "litellm",
			typ:                config.ProviderTypeLiteLLM,
			wantModelsDevID:    "",
			wantBaseURL:        "",
			wantGeneric:        true,
			wantFixedTransport: nil,
			wantLiveProbe:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := profileFor(tt.typ)

			if p.ModelsDevID != tt.wantModelsDevID {
				t.Errorf("ModelsDevID = %q, want %q", p.ModelsDevID, tt.wantModelsDevID)
			}
			if p.DefaultBaseURL != tt.wantBaseURL {
				t.Errorf("DefaultBaseURL = %q, want %q", p.DefaultBaseURL, tt.wantBaseURL)
			}
			if p.Generic != tt.wantGeneric {
				t.Errorf("Generic = %v, want %v", p.Generic, tt.wantGeneric)
			}
			if p.LiveProbe != tt.wantLiveProbe {
				t.Errorf("LiveProbe = %v, want %v", p.LiveProbe, tt.wantLiveProbe)
			}

			if tt.wantFixedTransport == nil {
				if p.FixedTransport != nil {
					t.Errorf("FixedTransport = %v, want nil", p.FixedTransport)
				}
			} else {
				if p.FixedTransport == nil {
					t.Errorf("FixedTransport = nil, want non-nil")
				} else if *p.FixedTransport != *tt.wantFixedTransport {
					t.Errorf("FixedTransport = %+v, want %+v", p.FixedTransport, tt.wantFixedTransport)
				}
			}
		})
	}
}

func TestProfileForUnknownType(t *testing.T) {
	p := profileFor("unknown_type")
	if !p.Generic {
		t.Errorf("Generic = %v, want true for unknown type", p.Generic)
	}
	if p.ModelsDevID != "" {
		t.Errorf("ModelsDevID = %q, want empty string for unknown type", p.ModelsDevID)
	}
	if p.DefaultBaseURL != "" {
		t.Errorf("DefaultBaseURL = %q, want empty string for unknown type", p.DefaultBaseURL)
	}
	if p.FixedTransport != nil {
		t.Errorf("FixedTransport = %v, want nil for unknown type", p.FixedTransport)
	}
	if p.LiveProbe {
		t.Errorf("LiveProbe = %v, want false for unknown type", p.LiveProbe)
	}
}

func TestLoadAndApplyMetadataMapsCodexAliasToOpenAI(t *testing.T) {
	cacheJSON := `{
		"302ai":{"models":{"gpt-5.6-luna":{"limit":{"context":1050000,"output":128000}}}},
		"abacus":{"models":{"gpt-5.6-luna":{"limit":{"context":1000000,"output":128000}}}},
		"openai":{"models":{"gpt-5.6-luna":{"limit":{"context":1050000,"output":128000}}}}
	}`
	for _, alias := range []string{"codex", "luna", "openai"} {
		t.Run(alias, func(t *testing.T) {
			cacheRoot := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", cacheRoot)
			cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
			if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(cache.CachePath(), []byte(cacheJSON), 0o644); err != nil {
				t.Fatalf("WriteFile(cache) error = %v", err)
			}
			if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
				t.Fatalf("WriteFile(meta) error = %v", err)
			}

			cfg := config.Config{
				Providers: map[string]config.ProviderConfig{
					alias: {Type: config.ProviderTypeCodex},
				},
				Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
					"luna": {Provider: alias, ID: "gpt-5.6-luna"},
				}},
			}

			rm, err := ResolveWithDiscovery(cfg, "luna", nil)
			if err != nil {
				t.Fatalf("ResolveWithDiscovery() error = %v", err)
			}
			// Regardless of the provider config alias, Codex's models.dev ID
			// is always "openai", so the 302ai/abacus entries never apply.
			if rm.EffectiveLimits.ContextWindow != 1050000 {
				t.Fatalf("EffectiveLimits.ContextWindow = %d, want 1050000", rm.EffectiveLimits.ContextWindow)
			}
			if rm.EffectiveLimits.MaxOutputTokens != 128000 {
				t.Fatalf("EffectiveLimits.MaxOutputTokens = %d, want 128000", rm.EffectiveLimits.MaxOutputTokens)
			}
			if len(rm.Warnings) != 0 {
				t.Fatalf("Warnings = %v, want none", rm.Warnings)
			}
		})
	}
}
