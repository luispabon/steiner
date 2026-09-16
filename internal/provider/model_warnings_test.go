package provider

import (
	"os"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

func writeModelsDevCache(t *testing.T, cacheJSON string) {
	t.Helper()
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
}

// TestDeriveWarningsScenarios exercises the §2.5 warning cases end to end
// through resolveReference, with a fresh (non-expired) meta file so cache
// loading never touches the network.
func TestDeriveWarningsScenarios(t *testing.T) {
	// Since Stage E, a generic provider (openai_compat/ollama/litellm) whose
	// alias isn't itself a models.dev provider key falls back to a
	// cross-provider merge on a provider_mismatch strict result, so this
	// scenario now resolves successfully (at low confidence) instead of
	// warning; see TestDeriveWarningsScenarios's "generic provider" subtests
	// below and TestModelsDevSource_GenericProviderMergesAcrossProviders in
	// fact_source_models_dev_test.go.
	t.Run("generic provider: provider_mismatch strict lookup falls back to merge, no warning", func(t *testing.T) {
		writeModelsDevCache(t, `{"other":{"models":{"unknown-model":{"limit":{"context":200000,"output":100000}}}}}`)

		cfg := config.Config{
			Providers: map[string]config.ProviderConfig{
				"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
			},
			Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
				"unknown": {Provider: "local", ID: "unknown-model"},
			}},
		}

		rm, err := resolveReference(&cfg, "unknown", true, nil)
		if err != nil {
			t.Fatalf("resolveReference(&) error = %v", err)
		}
		if len(rm.Warnings) != 0 {
			t.Fatalf("Warnings = %v, want none (merge fallback resolves it)", rm.Warnings)
		}
		if rm.EffectiveLimits.ContextWindow != 200000 {
			t.Fatalf("EffectiveLimits.ContextWindow = %d, want 200000", rm.EffectiveLimits.ContextWindow)
		}
		if rm.Confidence != "low" {
			t.Fatalf("Confidence = %q, want low", rm.Confidence)
		}
	})

	t.Run("generic provider: model missing everywhere still warns", func(t *testing.T) {
		writeModelsDevCache(t, `{"other":{"models":{"some-other-model":{"limit":{"context":200000,"output":100000}}}}}`)

		cfg := config.Config{
			Providers: map[string]config.ProviderConfig{
				"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
			},
			Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
				"unknown": {Provider: "local", ID: "unknown-model"},
			}},
		}

		rm, err := resolveReference(&cfg, "unknown", true, nil)
		if err != nil {
			t.Fatalf("resolveReference(&) error = %v", err)
		}
		if len(rm.Warnings) != 1 {
			t.Fatalf("Warnings = %v, want exactly 1", rm.Warnings)
		}
		if !strings.Contains(rm.Warnings[0], "has unknown context limits") {
			t.Errorf("warning = %q, want it to mention unknown context limits", rm.Warnings[0])
		}
	})

	t.Run("named provider: provider_mismatch still warns (no merge fallback)", func(t *testing.T) {
		writeModelsDevCache(t, `{"other":{"models":{"unknown-model":{"limit":{"context":200000,"output":100000}}}}}`)

		cfg := config.Config{
			Providers: map[string]config.ProviderConfig{
				"claude": {Type: config.ProviderTypeAnthropic},
			},
			Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
				"unknown": {Provider: "claude", ID: "unknown-model"},
			}},
		}

		rm, err := resolveReference(&cfg, "unknown", true, nil)
		if err != nil {
			t.Fatalf("resolveReference(&) error = %v", err)
		}
		if len(rm.Warnings) != 1 {
			t.Fatalf("Warnings = %v, want exactly 1", rm.Warnings)
		}
		if !strings.Contains(rm.Warnings[0], "has unknown context limits") {
			t.Errorf("warning = %q, want it to mention unknown context limits", rm.Warnings[0])
		}
		if !strings.Contains(rm.Warnings[0], "provider_mismatch") {
			t.Errorf("warning = %q, want it to mention provider_mismatch", rm.Warnings[0])
		}
	})

	// Malformed on-disk cache data with a fresh meta file is discovered inside
	// LookupWithProviderResult (LookupReasonMalformed), not at cache-load time
	// (Cache.Load is a bare os.ReadFile; it does not validate JSON). So this
	// path never produces a sourceErr: it surfaces only as a note on
	// ContextWindow, folded into the single fallback warning's suffix. A
	// cache-load-level failure (see TestResolveWithDiscoveryWarnsOnMetadataCacheDegradation)
	// is the two-warning case; malformed-but-loadable data is one.
	t.Run("malformed cache data with unconfigured limits warns once, folding the reason into the fallback warning", func(t *testing.T) {
		writeModelsDevCache(t, `not valid json`)

		cfg := config.Config{
			Providers: map[string]config.ProviderConfig{
				"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
			},
			Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
				"unknown": {Provider: "local", ID: "some-model"},
			}},
		}

		rm, err := resolveReference(&cfg, "unknown", true, nil)
		if err != nil {
			t.Fatalf("resolveReference(&) error = %v", err)
		}
		if len(rm.Warnings) != 1 {
			t.Fatalf("Warnings = %v, want exactly 1", rm.Warnings)
		}
		if !strings.Contains(rm.Warnings[0], "has unknown context limits") {
			t.Errorf("warning = %q, want it to mention unknown context limits", rm.Warnings[0])
		}
		if !strings.Contains(rm.Warnings[0], "malformed") {
			t.Errorf("warning = %q, want it to mention malformed", rm.Warnings[0])
		}
	})

	t.Run("malformed cache data is never consulted when everything is configured", func(t *testing.T) {
		writeModelsDevCache(t, `not valid json`)

		vision := true
		echoBack := false
		cfg := config.Config{
			Providers: map[string]config.ProviderConfig{
				"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
			},
			Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
				"full": {
					Provider: "local",
					ID:       "some-model",
					Vision:   &vision,
					Advanced: config.AdvancedConfig{
						Limits:            config.AdvancedLimitsConfig{ContextWindow: 128000, MaxOutputTokens: 8192},
						Reasoning:         config.ReasoningConfig{SupportedEfforts: []string{"low", "high"}},
						ReasoningEchoBack: &echoBack,
						Transport:         config.ModelTransportOpenAICompat,
					},
				},
			}},
		}

		rm, err := resolveReference(&cfg, "full", true, nil)
		if err != nil {
			t.Fatalf("resolveReference(&) error = %v", err)
		}
		if len(rm.Warnings) != 0 {
			t.Fatalf("Warnings = %v, want none (models.dev never consulted)", rm.Warnings)
		}
	})

	// Intentional behavior change vs. the pre-fact-resolver resolver: max_output_tokens
	// configured alone with an unconfigured context_window used to leave
	// EffectiveLimits.ContextWindow at 0 with MetadataSource "config" and no
	// warning (resolveEffectiveLimitsWithMeta/isFallbackLimits treated "some
	// limit configured" as fully resolved). Per-field resolution now leaves
	// ContextWindow genuinely pending, so it lands on the conservative
	// fallback and warns, which is more useful than a silent 0 context window.
	t.Run("max_output_tokens alone now defaults context_window and warns", func(t *testing.T) {
		writeModelsDevCache(t, `{}`)

		cfg := config.Config{
			Providers: map[string]config.ProviderConfig{
				"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
			},
			Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
				"partial": {
					Provider: "local",
					ID:       "some-model",
					Advanced: config.AdvancedConfig{
						Limits: config.AdvancedLimitsConfig{MaxOutputTokens: 2000},
					},
				},
			}},
		}

		rm, err := resolveReference(&cfg, "partial", true, nil)
		if err != nil {
			t.Fatalf("resolveReference(&) error = %v", err)
		}
		if rm.EffectiveLimits.ContextWindow != 32768 {
			t.Fatalf("EffectiveLimits.ContextWindow = %d, want 32768 (fallback default)", rm.EffectiveLimits.ContextWindow)
		}
		if rm.MetadataSource != "fallback" {
			t.Fatalf("MetadataSource = %q, want fallback", rm.MetadataSource)
		}
		if len(rm.Warnings) != 1 {
			t.Fatalf("Warnings = %v, want exactly 1", rm.Warnings)
		}
	})
}
