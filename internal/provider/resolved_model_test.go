package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

func TestResolve(t *testing.T) {
	baseCfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1", APIKey: "test-key"},
		},
		Models: map[string]config.ModelConfig{
			"mymodel": {
				Provider:     "local",
				ID:           "llama3",
				ExtraParams:  map[string]any{"temperature": 0.8},
				PromptSuffix: "nothink",
				Advanced: config.AdvancedConfig{
					Limits: config.AdvancedLimitsConfig{
						ContextWindow:   128000,
						MaxOutputTokens: 8192,
					},
				},
			},
		},
	}

	tests := []struct {
		name        string
		cfg         config.Config
		alias       string
		wantErr     bool
		wantErrText string
		check       func(t *testing.T, rm ResolvedModel)
	}{
		{
			name:  "valid alias and provider",
			cfg:   baseCfg,
			alias: "mymodel",
			check: func(t *testing.T, rm ResolvedModel) {
				if rm.Alias != "mymodel" {
					t.Errorf("Alias=%q, want %q", rm.Alias, "mymodel")
				}
				if rm.ProviderAlias != "local" {
					t.Errorf("ProviderAlias=%q, want %q", rm.ProviderAlias, "local")
				}
				if rm.BackendModelID != "llama3" {
					t.Errorf("BackendModelID=%q, want %q", rm.BackendModelID, "llama3")
				}
				if rm.ProviderConfig.BaseURL != "http://localhost:11434/v1" {
					t.Errorf("ProviderConfig.BaseURL=%q, want http://localhost:11434/v1", rm.ProviderConfig.BaseURL)
				}
				if rm.EffectiveProviderType != config.ProviderTypeOpenAICompat {
					t.Errorf("EffectiveProviderType=%q, want %q", rm.EffectiveProviderType, config.ProviderTypeOpenAICompat)
				}
				if rm.EffectiveTransport != TransportConfigured {
					t.Errorf("EffectiveTransport=%q, want %q", rm.EffectiveTransport, TransportConfigured)
				}
				if rm.MetadataSource != "config" {
					t.Errorf("MetadataSource=%q, want config", rm.MetadataSource)
				}
				if rm.Confidence != "high" {
					t.Errorf("Confidence=%q, want high", rm.Confidence)
				}
				if rm.PromptSuffix != "nothink" {
					t.Errorf("PromptSuffix=%q, want nothink", rm.PromptSuffix)
				}
				if rm.ReasoningEchoBack {
					t.Error("ReasoningEchoBack=true, want false (no discovery)")
				}
				if v, ok := rm.ExtraParams["temperature"]; !ok || v != 0.8 {
					t.Errorf("ExtraParams[temperature]=%v, want 0.8", v)
				}
			},
		},
		{
			name:        "unknown alias",
			cfg:         baseCfg,
			alias:       "nonexistent",
			wantErr:     true,
			wantErrText: `model alias "nonexistent" not found`,
		},
		{
			name: "model referencing unknown provider",
			cfg: config.Config{
				Providers: map[string]config.ProviderConfig{},
				Models: map[string]config.ModelConfig{
					"orphan": {Provider: "missing", ID: "some-model"},
				},
			},
			alias:       "orphan",
			wantErr:     true,
			wantErrText: `provider "missing" not found for model "orphan"`,
		},
		{
			name:  "effective limits populated from advanced limits",
			cfg:   baseCfg,
			alias: "mymodel",
			check: func(t *testing.T, rm ResolvedModel) {
				lim := rm.EffectiveLimits
				if lim.ContextWindow != 128000 {
					t.Errorf("ContextWindow=%d, want 128000", lim.ContextWindow)
				}
				if lim.MaxOutputTokens != 8192 {
					t.Errorf("MaxOutputTokens=%d, want 8192", lim.MaxOutputTokens)
				}
				if lim.EstimatorPadTokens != 1280 {
					t.Errorf("EstimatorPadTokens=%d, want 1280", lim.EstimatorPadTokens)
				}
				if lim.NormalSummaryMaxTokens != 8192 {
					t.Errorf("NormalSummaryMaxTokens=%d, want 8192", lim.NormalSummaryMaxTokens)
				}
				if lim.EmergencySummaryMaxTokens != 5120 {
					t.Errorf("EmergencySummaryMaxTokens=%d, want 5120", lim.EmergencySummaryMaxTokens)
				}
				if lim.CompactionThreshold != 0.70 {
					t.Errorf("CompactionThreshold=%v, want 0.70", lim.CompactionThreshold)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rm, err := Resolve(tt.cfg, tt.alias)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve() error = nil, want error containing %q", tt.wantErrText)
				}
				if tt.wantErrText != "" && err.Error() != tt.wantErrText {
					t.Errorf("Resolve() error = %q, want %q", err.Error(), tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() unexpected error = %v", err)
			}
			if tt.check != nil {
				tt.check(t, rm)
			}
		})
	}
}

func TestResolveMinimalConfig(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: map[string]config.ModelConfig{
			"default": {
				Provider: "local",
				ID:       "qwen3",
			},
		},
	}

	rm, err := Resolve(cfg, "default")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := rm.MetadataSource, "config"; got != want {
		t.Fatalf("MetadataSource = %q, want %q", got, want)
	}
	if got, want := rm.ProviderAlias, "local"; got != want {
		t.Fatalf("ProviderAlias = %q, want %q", got, want)
	}
	if got, want := rm.BackendModelID, "qwen3"; got != want {
		t.Fatalf("BackendModelID = %q, want %q", got, want)
	}
	if got, want := rm.EffectiveLimits.ContextWindow, 32768; got != want {
		t.Fatalf("ContextWindow = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.MaxOutputTokens, 4096; got != want {
		t.Fatalf("MaxOutputTokens = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.EstimatorPadTokens, 327; got != want {
		t.Fatalf("EstimatorPadTokens = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.NormalSummaryMaxTokens, 4096; got != want {
		t.Fatalf("NormalSummaryMaxTokens = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.EmergencySummaryMaxTokens, 2048; got != want {
		t.Fatalf("EmergencySummaryMaxTokens = %d, want %d", got, want)
	}
}

func TestResolveProviderConfigAppliesOpenRouterDefaults(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "router-secret")

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"router": {Type: config.ProviderTypeOpenRouter, APIKeyEnv: "OPENROUTER_API_KEY"},
		},
		Models: map[string]config.ModelConfig{
			"sonnet": {Provider: "router", ID: "anthropic/claude-3.7-sonnet"},
		},
	}

	rm, err := Resolve(cfg, "sonnet")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := rm.ProviderConfig.BaseURL, "https://openrouter.ai/api/v1"; got != want {
		t.Fatalf("ProviderConfig.BaseURL = %q, want %q", got, want)
	}
	if got, want := rm.ProviderConfig.APIKey, "router-secret"; got != want {
		t.Fatalf("ProviderConfig.APIKey = %q, want %q", got, want)
	}
}

func TestResolveProviderConfigAppliesCodexDefaults(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"codex": {Type: config.ProviderTypeCodex},
		},
		Models: map[string]config.ModelConfig{
			"codex-model": {Provider: "codex", ID: "codex-default"},
		},
	}

	rm, err := Resolve(cfg, "codex-model")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got, want := rm.ProviderConfig.BaseURL, "https://api.openai.com/v1"; got != want {
		t.Fatalf("ProviderConfig.BaseURL = %q, want %q", got, want)
	}
}

// TestResolveEffectiveLimits tests derivation logic for missing token limits.
func TestResolveEffectiveLimits(t *testing.T) {
	tests := []struct {
		name  string
		input config.AdvancedLimitsConfig
		want  EffectiveLimits
	}{
		{
			name: "all values configured",
			input: config.AdvancedLimitsConfig{
				ContextWindow:   128000,
				MaxOutputTokens: 8192,
			},
			want: EffectiveLimits{
				ContextWindow:             128000,
				MaxOutputTokens:           8192,
				CompactionThreshold:       0.70,
				EstimatorPadTokens:        1280,
				NormalSummaryMaxTokens:    8192,
				EmergencySummaryMaxTokens: 5120,
			},
		},
		{
			name: "context_window only",
			input: config.AdvancedLimitsConfig{
				ContextWindow: 64000,
			},
			want: EffectiveLimits{
				ContextWindow:             64000,
				MaxOutputTokens:           0,
				CompactionThreshold:       0.70,
				EstimatorPadTokens:        640,
				NormalSummaryMaxTokens:    5120,
				EmergencySummaryMaxTokens: 2560,
			},
		},
		{
			name: "context_window and max_output",
			input: config.AdvancedLimitsConfig{
				ContextWindow:   100000,
				MaxOutputTokens: 6000,
			},
			want: EffectiveLimits{
				ContextWindow:             100000,
				MaxOutputTokens:           6000,
				CompactionThreshold:       0.70,
				EstimatorPadTokens:        1000,
				NormalSummaryMaxTokens:    6000,
				EmergencySummaryMaxTokens: 4000,
			},
		},
		{
			name: "max_output only with unknown context",
			input: config.AdvancedLimitsConfig{
				MaxOutputTokens: 5000,
			},
			want: EffectiveLimits{
				ContextWindow:             0,
				MaxOutputTokens:           5000,
				CompactionThreshold:       0.70,
				EstimatorPadTokens:        256,
				NormalSummaryMaxTokens:    4096,
				EmergencySummaryMaxTokens: 2048,
			},
		},
		{
			name:  "fallback (nothing configured)",
			input: config.AdvancedLimitsConfig{},
			want: EffectiveLimits{
				ContextWindow:             32768,
				MaxOutputTokens:           4096,
				CompactionThreshold:       0.70,
				EstimatorPadTokens:        327,
				NormalSummaryMaxTokens:    4096,
				EmergencySummaryMaxTokens: 2048,
			},
		},
		{
			name: "context_window with summary floors",
			input: config.AdvancedLimitsConfig{
				ContextWindow: 32000,
			},
			want: EffectiveLimits{
				ContextWindow:             32000,
				MaxOutputTokens:           0,
				CompactionThreshold:       0.70,
				EstimatorPadTokens:        320,
				NormalSummaryMaxTokens:    4096,
				EmergencySummaryMaxTokens: 2048,
			},
		},
		{
			name: "large context_window capped at derived max",
			input: config.AdvancedLimitsConfig{
				ContextWindow: 1000000,
			},
			want: EffectiveLimits{
				ContextWindow:             1000000,
				MaxOutputTokens:           0,
				CompactionThreshold:       0.70,
				EstimatorPadTokens:        2048,
				NormalSummaryMaxTokens:    16000,
				EmergencySummaryMaxTokens: 8000,
			},
		},
		{
			name: "only max_output_tokens configured",
			input: config.AdvancedLimitsConfig{
				MaxOutputTokens: 2000,
			},
			want: EffectiveLimits{
				ContextWindow:             0,
				MaxOutputTokens:           2000,
				CompactionThreshold:       0.70,
				EstimatorPadTokens:        256,
				NormalSummaryMaxTokens:    2000,
				EmergencySummaryMaxTokens: 2000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveEffectiveLimits(tt.input)
			if got.ContextWindow != tt.want.ContextWindow {
				t.Errorf("ContextWindow = %d, want %d", got.ContextWindow, tt.want.ContextWindow)
			}
			if got.MaxOutputTokens != tt.want.MaxOutputTokens {
				t.Errorf("MaxOutputTokens = %d, want %d", got.MaxOutputTokens, tt.want.MaxOutputTokens)
			}
			if got.EstimatorPadTokens != tt.want.EstimatorPadTokens {
				t.Errorf("EstimatorPadTokens = %d, want %d", got.EstimatorPadTokens, tt.want.EstimatorPadTokens)
			}
			if got.NormalSummaryMaxTokens != tt.want.NormalSummaryMaxTokens {
				t.Errorf("NormalSummaryMaxTokens = %d, want %d", got.NormalSummaryMaxTokens, tt.want.NormalSummaryMaxTokens)
			}
			if got.EmergencySummaryMaxTokens != tt.want.EmergencySummaryMaxTokens {
				t.Errorf("EmergencySummaryMaxTokens = %d, want %d", got.EmergencySummaryMaxTokens, tt.want.EmergencySummaryMaxTokens)
			}
			if got.CompactionThreshold != tt.want.CompactionThreshold {
				t.Errorf("CompactionThreshold = %f, want %f", got.CompactionThreshold, tt.want.CompactionThreshold)
			}
		})
	}
}

func TestResolveWithDiscoveryFallbackWarning(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: map[string]config.ModelConfig{
			"unknown": {
				Provider: "local",
				ID:       "custom-unknown-model",
			},
		},
	}

	rm, err := ResolveWithDiscovery(cfg, "unknown", nil)
	if err != nil {
		t.Fatalf("ResolveWithDiscovery() error = %v", err)
	}
	if got, want := rm.MetadataSource, "fallback"; got != want {
		t.Fatalf("MetadataSource = %q, want %q", got, want)
	}
	if got, want := rm.Confidence, "low"; got != want {
		t.Fatalf("Confidence = %q, want %q", got, want)
	}
	if len(rm.Warnings) != 1 {
		t.Fatalf("Warnings len = %d, want 1", len(rm.Warnings))
	}
	wantWarning := "Model metadata warning: unknown/custom-unknown-model has unknown context limits. Using conservative fallback: context_window=32768, max_output_tokens=4096. Set models.unknown.advanced.limits.context_window to remove this warning."
	if got := rm.Warnings[0]; got != wantWarning {
		t.Fatalf("warning = %q, want %q", got, wantWarning)
	}
	if got, want := rm.TokenizerStrategy, TokenizerStrategyTiktoken; got != want {
		t.Fatalf("TokenizerStrategy = %q, want %q", got, want)
	}
	if got, want := rm.TokenizerConfidence, "low"; got != want {
		t.Fatalf("TokenizerConfidence = %q, want %q", got, want)
	}
}

func TestResolveTokenizerMetadataFallsBackToHeuristicWhenTiktokenUnavailable(t *testing.T) {
	t.Parallel()

	strategy, confidence := resolveTokenizerMetadataWithLoader("custom-model", func(string) (tokenizer.Codec, error) {
		return nil, errors.New("unavailable")
	})
	if strategy != TokenizerStrategyHeuristic {
		t.Fatalf("strategy = %q, want %q", strategy, TokenizerStrategyHeuristic)
	}
	if confidence != "low" {
		t.Fatalf("confidence = %q, want low", confidence)
	}
}

func TestResolveWithDiscoveryUsesModelsDevWithoutWarning(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000,"output":16384}}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: map[string]config.ModelConfig{
			"gpt4o": {
				Provider: "local",
				ID:       "gpt-4o",
			},
		},
	}

	rm, err := ResolveWithDiscovery(cfg, "gpt4o", nil)
	if err != nil {
		t.Fatalf("ResolveWithDiscovery() error = %v", err)
	}
	if got, want := rm.MetadataSource, "models.dev"; got != want {
		t.Fatalf("MetadataSource = %q, want %q", got, want)
	}
	if len(rm.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none", rm.Warnings)
	}
	if got, want := rm.EffectiveLimits.ContextWindow, 128000; got != want {
		t.Fatalf("ContextWindow = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.MaxOutputTokens, 16384; got != want {
		t.Fatalf("MaxOutputTokens = %d, want %d", got, want)
	}
	if !strings.HasSuffix(cache.CachePath(), filepath.Join("steiner", "model-metadata", "models.dev.json")) {
		t.Fatalf("cache path = %q, want steiner model metadata path", cache.CachePath())
	}
}

func TestResolveWithDiscoveryUsesProviderSpecificModelsDevLimits(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	cacheJSON := `{
		"ollama-cloud":{"models":{"deepseek-v4-flash":{"limit":{"context":1048576,"output":1048576}}}},
		"opencode-go":{"models":{"deepseek-v4-flash":{"limit":{"context":1000000,"output":384000},"interleaved":{"field":"reasoning_content"}}}}
	}`
	if err := os.WriteFile(cache.CachePath(), []byte(cacheJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"opencode-go": {Type: config.ProviderTypeOpenAICompat, BaseURL: "https://opencode.ai/zen/go/v1/"},
		},
		Models: map[string]config.ModelConfig{
			"opencode-go/deepseek-v4-flash": {
				Provider: "opencode-go",
				ID:       "deepseek-v4-flash",
			},
		},
	}

	rm, err := ResolveWithDiscovery(cfg, "opencode-go/deepseek-v4-flash", nil)
	if err != nil {
		t.Fatalf("ResolveWithDiscovery() error = %v", err)
	}
	if got, want := rm.MetadataSource, "models.dev"; got != want {
		t.Fatalf("MetadataSource = %q, want %q", got, want)
	}
	if got, want := rm.EffectiveLimits.ContextWindow, 1000000; got != want {
		t.Fatalf("ContextWindow = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.MaxOutputTokens, 384000; got != want {
		t.Fatalf("MaxOutputTokens = %d, want %d", got, want)
	}
	if !rm.ReasoningEchoBack {
		t.Fatal("ReasoningEchoBack = false, want true")
	}
}

func TestResolveWithDiscoveryRefreshesStaleModelsDevCache(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{"prov":{"models":{"old":{"limit":{"context":4096,"output":512}}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2026-05-02T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000,"output":16384}}}}}`))
	}))
	defer srv.Close()

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: map[string]config.ModelConfig{
			"gpt4o": {Provider: "local", ID: "gpt-4o"},
		},
	}

	client := &http.Client{Transport: &redirectTransport{target: srv.URL}}
	rm, err := ResolveWithDiscovery(cfg, "gpt4o", client)
	if err != nil {
		t.Fatalf("ResolveWithDiscovery() error = %v", err)
	}
	if got, want := rm.MetadataSource, "models.dev"; got != want {
		t.Fatalf("MetadataSource = %q, want %q", got, want)
	}
	if got, want := rm.EffectiveLimits.ContextWindow, 128000; got != want {
		t.Fatalf("ContextWindow = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.MaxOutputTokens, 16384; got != want {
		t.Fatalf("MaxOutputTokens = %d, want %d", got, want)
	}
}

func TestResolveWithDiscoveryOfflineUsesStaleModelsDevCache(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{"openai":{"models":{"gpt-4o":{"limit":{"context":128000,"output":16384}}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2026-05-02T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: map[string]config.ModelConfig{
			"gpt4o": {Provider: "local", ID: "gpt-4o"},
		},
	}

	client := &http.Client{Transport: &alwaysFailTransport{}}
	rm, err := ResolveWithDiscovery(cfg, "gpt4o", client)
	if err != nil {
		t.Fatalf("ResolveWithDiscovery() error = %v", err)
	}
	if got, want := rm.MetadataSource, "models.dev"; got != want {
		t.Fatalf("MetadataSource = %q, want %q", got, want)
	}
	if got, want := rm.EffectiveLimits.ContextWindow, 128000; got != want {
		t.Fatalf("ContextWindow = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.MaxOutputTokens, 16384; got != want {
		t.Fatalf("MaxOutputTokens = %d, want %d", got, want)
	}
}

func TestResolveWithDiscoveryProviderMetadataBeatsModelsDev(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{"openrouter":{"models":{"openai/gpt-4o":{"limit":{"context":64000,"output":4096}}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-4o","context_length":128000,"top_provider":{"max_completion_tokens":16384}}]}`))
	}))
	defer srv.Close()

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"router": {Type: config.ProviderTypeOpenRouter, BaseURL: srv.URL},
		},
		Models: map[string]config.ModelConfig{
			"gpt4o": {
				Provider: "router",
				ID:       "openai/gpt-4o",
			},
		},
	}

	rm, err := ResolveWithDiscovery(cfg, "gpt4o", srv.Client())
	if err != nil {
		t.Fatalf("ResolveWithDiscovery() error = %v", err)
	}
	if got, want := rm.MetadataSource, "discovery"; got != want {
		t.Fatalf("MetadataSource = %q, want %q", got, want)
	}
	if got, want := rm.EffectiveLimits.ContextWindow, 128000; got != want {
		t.Fatalf("ContextWindow = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.MaxOutputTokens, 16384; got != want {
		t.Fatalf("MaxOutputTokens = %d, want %d", got, want)
	}
	if len(rm.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none", rm.Warnings)
	}
}

func TestResolveWithDiscoveryManualOverrideWinsAll(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{"openrouter":{"models":{"openai/gpt-4o":{"limit":{"context":64000,"output":4096}}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-4o","context_length":128000,"top_provider":{"max_completion_tokens":16384}}]}`))
	}))
	defer srv.Close()

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"router": {Type: config.ProviderTypeOpenRouter, BaseURL: srv.URL},
		},
		Models: map[string]config.ModelConfig{
			"gpt4o": {
				Provider: "router",
				ID:       "openai/gpt-4o",
				Advanced: config.AdvancedConfig{
					Limits: config.AdvancedLimitsConfig{
						ContextWindow:   200000,
						MaxOutputTokens: 32000,
					},
				},
			},
		},
	}

	rm, err := ResolveWithDiscovery(cfg, "gpt4o", srv.Client())
	if err != nil {
		t.Fatalf("ResolveWithDiscovery() error = %v", err)
	}
	if got, want := rm.MetadataSource, "config"; got != want {
		t.Fatalf("MetadataSource = %q, want %q", got, want)
	}
	if got, want := rm.EffectiveLimits.ContextWindow, 200000; got != want {
		t.Fatalf("ContextWindow = %d, want %d", got, want)
	}
	if got, want := rm.EffectiveLimits.MaxOutputTokens, 32000; got != want {
		t.Fatalf("MaxOutputTokens = %d, want %d", got, want)
	}
	if len(rm.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none", rm.Warnings)
	}
}

func TestResolveWithDiscoveryReasoningEchoBack(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	cacheJSON := `{"prov":{"models":{` +
		`"deepseek-r1":{"limit":{"context":128000,"output":8192},"interleaved":{"field":"reasoning_content"}},` +
		`"gpt-4o":{"limit":{"context":128000,"output":16384}}` +
		`}}}`
	if err := os.WriteFile(cache.CachePath(), []byte(cacheJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	tests := []struct {
		name         string
		modelID      string
		wantEchoBack bool
	}{
		{
			name:         "interleaved reasoning_content",
			modelID:      "deepseek-r1",
			wantEchoBack: true,
		},
		{
			name:         "no interleaved field",
			modelID:      "gpt-4o",
			wantEchoBack: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				Providers: map[string]config.ProviderConfig{
					"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
				},
				Models: map[string]config.ModelConfig{
					"test": {
						Provider:     "local",
						ID:           tt.modelID,
						PromptSuffix: "<|think_off|>",
					},
				},
			}
			rm, err := ResolveWithDiscovery(cfg, "test", nil)
			if err != nil {
				t.Fatalf("ResolveWithDiscovery() error = %v", err)
			}
			if rm.ReasoningEchoBack != tt.wantEchoBack {
				t.Errorf("ReasoningEchoBack=%v, want %v", rm.ReasoningEchoBack, tt.wantEchoBack)
			}
		})
	}
}

type redirectTransport struct {
	target string
	base   http.RoundTripper
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	cloned.URL.Host = strings.TrimPrefix(t.target, "http://")
	rt := t.base
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(cloned)
}

type alwaysFailTransport struct{}

func (alwaysFailTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("offline")
}

func boolPtr(v bool) *bool { return &v }

func TestResolveReasoningEchoBackConfigOverride(t *testing.T) {
	tests := []struct {
		name     string
		override *bool
		want     bool
	}{
		{name: "nil uses default false", override: nil, want: false},
		{name: "explicit true sets true", override: boolPtr(true), want: true},
		{name: "explicit false sets false", override: boolPtr(false), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				Providers: map[string]config.ProviderConfig{
					"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
				},
				Models: map[string]config.ModelConfig{
					"mymodel": {
						Provider: "local",
						ID:       "llama3",
						Advanced: config.AdvancedConfig{
							ReasoningEchoBack: tt.override,
						},
					},
				},
			}

			rm, err := Resolve(cfg, "mymodel")
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if rm.ReasoningEchoBack != tt.want {
				t.Errorf("ReasoningEchoBack = %v, want %v", rm.ReasoningEchoBack, tt.want)
			}
		})
	}
}

func TestResolveWithDiscoveryConfigOverrideWinsOverModelsDevReasoningEchoBack(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	// Seed a cache where the model has ReasoningEchoBack=true from models.dev.
	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	cacheJSON := `{"opencode-go":{"models":{"kimi-k2":{"interleaved":{"field":"reasoning_content"}}}}}`
	if err := os.WriteFile(cache.CachePath(), []byte(cacheJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	tests := []struct {
		name     string
		override *bool
		want     bool
	}{
		{
			name:     "config override false suppresses models.dev true",
			override: boolPtr(false),
			want:     false,
		},
		{
			name:     "config override true agrees with models.dev",
			override: boolPtr(true),
			want:     true,
		},
		{
			name:     "nil falls through to models.dev",
			override: nil,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				Providers: map[string]config.ProviderConfig{
					"opencode-go": {Type: config.ProviderTypeOpenAICompat, BaseURL: "https://opencode.ai/zen/go/v1/"},
				},
				Models: map[string]config.ModelConfig{
					"kimi": {
						Provider: "opencode-go",
						ID:       "kimi-k2",
						Advanced: config.AdvancedConfig{
							ReasoningEchoBack: tt.override,
						},
					},
				},
			}

			rm, err := ResolveWithDiscovery(cfg, "kimi", nil)
			if err != nil {
				t.Fatalf("ResolveWithDiscovery() error = %v", err)
			}
			if rm.ReasoningEchoBack != tt.want {
				t.Errorf("ReasoningEchoBack = %v, want %v", rm.ReasoningEchoBack, tt.want)
			}
		})
	}
}

func TestResolveWithDiscoveryMetadataTransportResolution(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	cacheJSON := `{
		"opencode-go":{
			"npm":"@ai-sdk/openai-compatible",
			"api":"https://opencode.ai/zen/go/v1/",
			"models":{
				"minimax-m3":{
					"provider":{"npm":"@ai-sdk/anthropic","api":"https://api.minimax.chat/anthropic"},
					"limit":{"context":256000,"output":8192}
				},
				"kimi-k2.6":{
					"provider":{"npm":"@ai-sdk/openai-compatible","api":"https://api.moonshot.ai/v1"},
					"limit":{"context":256000,"output":16384}
				},
				"gpt-5.4-mini":{
					"provider":{"npm":"@ai-sdk/openai-compatible","api":"https://api.openai.com/v1"},
					"limit":{"context":400000,"output":128000}
				}
			}
		}
	}`
	if err := os.WriteFile(cache.CachePath(), []byte(cacheJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	tests := []struct {
		name             string
		modelID          string
		override         config.ModelTransportType
		wantProviderType config.ProviderType
		wantTransport    TransportType
		wantBaseURL      string
	}{
		{
			name:             "model metadata switches minimax to anthropic",
			modelID:          "minimax-m3",
			wantProviderType: config.ProviderTypeAnthropic,
			wantTransport:    TransportAnthropic,
			wantBaseURL:      "https://opencode.ai/zen/go/v1/",
		},
		{
			name:             "model metadata keeps kimi on openai compatible",
			modelID:          "kimi-k2.6",
			wantProviderType: config.ProviderTypeOpenAICompat,
			wantTransport:    TransportOpenAICompat,
			wantBaseURL:      "https://opencode.ai/zen/go/v1/",
		},
		{
			name:             "config override wins over metadata",
			modelID:          "minimax-m3",
			override:         config.ModelTransportOpenAICompat,
			wantProviderType: config.ProviderTypeOpenAICompat,
			wantTransport:    TransportOpenAICompat,
			wantBaseURL:      "https://opencode.ai/zen/go/v1/",
		},
		{
			name:             "codex provider ignores models.dev openai-compatible transport",
			modelID:          "gpt-5.4-mini",
			wantProviderType: config.ProviderTypeCodex,
			wantTransport:    TransportConfigured,
			wantBaseURL:      "https://api.openai.com/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerType := config.ProviderTypeOpenAICompat
			providerBaseURL := "https://opencode.ai/zen/go/v1/"
			if tt.wantProviderType == config.ProviderTypeCodex {
				providerType = config.ProviderTypeCodex
				providerBaseURL = ""
			}
			cfg := config.Config{
				Providers: map[string]config.ProviderConfig{
					"opencode-go": {Type: providerType, BaseURL: providerBaseURL},
				},
				Models: map[string]config.ModelConfig{
					"test": {
						Provider: "opencode-go",
						ID:       tt.modelID,
						Advanced: config.AdvancedConfig{
							Transport: tt.override,
						},
					},
				},
			}

			rm, err := ResolveWithDiscovery(cfg, "test", nil)
			if err != nil {
				t.Fatalf("ResolveWithDiscovery() error = %v", err)
			}
			if got := rm.EffectiveProviderType; got != tt.wantProviderType {
				t.Fatalf("EffectiveProviderType = %q, want %q", got, tt.wantProviderType)
			}
			if got := rm.EffectiveTransport; got != tt.wantTransport {
				t.Fatalf("EffectiveTransport = %q, want %q", got, tt.wantTransport)
			}
			if got := rm.ProviderConfig.BaseURL; got != tt.wantBaseURL {
				t.Fatalf("ProviderConfig.BaseURL = %q, want %q", got, tt.wantBaseURL)
			}
		})
	}
}
