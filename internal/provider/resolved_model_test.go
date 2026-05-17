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
				Provider:                  "local",
				ID:                        "llama3",
				ExtraParams:               map[string]any{"temperature": 0.8},
				ThinkingEnabled:           true,
				ThinkingDisableMarker:     "nothink",
				ThinkingScaffoldInference: true,
				ThinkingParams:            map[string]any{"budget_tokens": 1024},
				ReasoningEchoBack:         true,
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
				if rm.MetadataSource != "config" {
					t.Errorf("MetadataSource=%q, want config", rm.MetadataSource)
				}
				if rm.Confidence != "high" {
					t.Errorf("Confidence=%q, want high", rm.Confidence)
				}
				if !rm.ThinkingEnabled {
					t.Error("ThinkingEnabled=false, want true")
				}
				if rm.ThinkingDisableMarker != "nothink" {
					t.Errorf("ThinkingDisableMarker=%q, want nothink", rm.ThinkingDisableMarker)
				}
				if !rm.ThinkingScaffoldInference {
					t.Error("ThinkingScaffoldInference=false, want true")
				}
				if !rm.ReasoningEchoBack {
					t.Error("ReasoningEchoBack=false, want true")
				}
				if v, ok := rm.ThinkingParams["budget_tokens"]; !ok || v != 1024 {
					t.Errorf("ThinkingParams[budget_tokens]=%v, want 1024", v)
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
	if err := os.WriteFile(cache.CachePath(), []byte(`{"models":{"gpt-4o":{"context":128000,"maxOutputTokens":16384}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2026-05-20T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
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

func TestResolveWithDiscoveryRefreshesStaleModelsDevCache(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{"models":{"old":{"context":4096,"maxOutputTokens":512}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2026-05-02T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":{"gpt-4o":{"context":128000,"maxOutputTokens":16384}}}`))
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
	if err := os.WriteFile(cache.CachePath(), []byte(`{"models":{"gpt-4o":{"context":128000,"maxOutputTokens":16384}}}`), 0o644); err != nil {
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
	if err := os.WriteFile(cache.CachePath(), []byte(`{"models":{"openai/gpt-4o":{"context":64000,"maxOutputTokens":4096}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2026-05-20T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
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
	if err := os.WriteFile(cache.CachePath(), []byte(`{"models":{"openai/gpt-4o":{"context":64000,"maxOutputTokens":4096}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2026-05-20T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
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
