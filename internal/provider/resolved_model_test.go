package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
	"github.com/tiktoken-go/tokenizer"
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
				Advanced: config.AdvancedConfig{
					Limits: config.AdvancedLimitsConfig{
						ContextWindow:       128000,
						MaxOutputTokens:     8192,
						MaxInputTokens:      120000,
						OutputReserveTokens: 512,
						SafetyMarginTokens:  500,
						SummaryMaxTokens:    2000,
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
				if lim.MaxInputTokens != 120000 {
					t.Errorf("MaxInputTokens=%d, want 120000", lim.MaxInputTokens)
				}
				if lim.OutputReserveTokens != 512 {
					t.Errorf("OutputReserveTokens=%d, want 512", lim.OutputReserveTokens)
				}
				if lim.SafetyMarginTokens != 500 {
					t.Errorf("SafetyMarginTokens=%d, want 500", lim.SafetyMarginTokens)
				}
				if lim.SummaryMaxTokens != 2000 {
					t.Errorf("SummaryMaxTokens=%d, want 2000", lim.SummaryMaxTokens)
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
				ContextWindow:       128000,
				MaxOutputTokens:     8192,
				MaxInputTokens:      120000,
				OutputReserveTokens: 512,
				SafetyMarginTokens:  500,
				SummaryMaxTokens:    2000,
			},
			want: EffectiveLimits{
				ContextWindow:       128000,
				MaxInputTokens:      120000,
				MaxOutputTokens:     8192,
				OutputReserveTokens: 512,
				SafetyMarginTokens:  500,
				SummaryMaxTokens:    2000,
				CompactionThreshold: 0.70,
			},
		},
		{
			name: "context_window only",
			input: config.AdvancedLimitsConfig{
				ContextWindow: 64000,
			},
			want: EffectiveLimits{
				ContextWindow:       64000,
				MaxInputTokens:      0,
				MaxOutputTokens:     4096,
				OutputReserveTokens: 4096,
				SafetyMarginTokens:  2048,
				SummaryMaxTokens:    8192, // min(64000/4, 8192) = min(16000, 8192) = 8192
				CompactionThreshold: 0.70,
			},
		},
		{
			name: "context_window and max_output",
			input: config.AdvancedLimitsConfig{
				ContextWindow:   100000,
				MaxOutputTokens: 6000,
			},
			want: EffectiveLimits{
				ContextWindow:       100000,
				MaxInputTokens:      0,
				MaxOutputTokens:     6000,
				OutputReserveTokens: 6000, // derives from max_output
				SafetyMarginTokens:  2048,
				SummaryMaxTokens:    8192, // min(100000/4, 8192) = 8192
				CompactionThreshold: 0.70,
			},
		},
		{
			name: "max_input and max_output only",
			input: config.AdvancedLimitsConfig{
				MaxInputTokens:  90000,
				MaxOutputTokens: 5000,
			},
			want: EffectiveLimits{
				ContextWindow:       0,
				MaxInputTokens:      90000,
				MaxOutputTokens:     5000,
				OutputReserveTokens: 5000,
				SafetyMarginTokens:  0, // no safety margin when context_window unknown
				SummaryMaxTokens:    0, // no summary max when context_window unknown
				CompactionThreshold: 0.70,
			},
		},
		{
			name:  "fallback (nothing configured)",
			input: config.AdvancedLimitsConfig{},
			want: EffectiveLimits{
				ContextWindow:       32768,
				MaxInputTokens:      0,
				MaxOutputTokens:     4096,
				OutputReserveTokens: 4096,
				SafetyMarginTokens:  2048,
				SummaryMaxTokens:    4096,
				CompactionThreshold: 0.70,
			},
		},
		{
			name: "context_window where context/4 < 8192 cap",
			input: config.AdvancedLimitsConfig{
				ContextWindow: 32000, // 32000/4 = 8000, which is < 8192
			},
			want: EffectiveLimits{
				ContextWindow:       32000,
				MaxInputTokens:      0,
				MaxOutputTokens:     4096,
				OutputReserveTokens: 4096,
				SafetyMarginTokens:  2048,
				SummaryMaxTokens:    8000, // min(32000/4, 8192) = min(8000, 8192) = 8000
				CompactionThreshold: 0.70,
			},
		},
		{
			name: "large context_window capped at 8192",
			input: config.AdvancedLimitsConfig{
				ContextWindow: 1000000, // 1000000/4 = 250000, capped at 8192
			},
			want: EffectiveLimits{
				ContextWindow:       1000000,
				MaxInputTokens:      0,
				MaxOutputTokens:     4096,
				OutputReserveTokens: 4096,
				SafetyMarginTokens:  2048,
				SummaryMaxTokens:    8192, // min(1000000/4, 8192) = 8192
				CompactionThreshold: 0.70,
			},
		},
		{
			name: "only max_output_tokens configured",
			input: config.AdvancedLimitsConfig{
				MaxOutputTokens: 2000,
			},
			want: EffectiveLimits{
				ContextWindow:       0,
				MaxInputTokens:      0,
				MaxOutputTokens:     2000,
				OutputReserveTokens: 2000, // derives from max_output
				SafetyMarginTokens:  0,
				SummaryMaxTokens:    0,
				CompactionThreshold: 0.70,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveEffectiveLimits(tt.input)
			if got.ContextWindow != tt.want.ContextWindow {
				t.Errorf("ContextWindow = %d, want %d", got.ContextWindow, tt.want.ContextWindow)
			}
			if got.MaxInputTokens != tt.want.MaxInputTokens {
				t.Errorf("MaxInputTokens = %d, want %d", got.MaxInputTokens, tt.want.MaxInputTokens)
			}
			if got.MaxOutputTokens != tt.want.MaxOutputTokens {
				t.Errorf("MaxOutputTokens = %d, want %d", got.MaxOutputTokens, tt.want.MaxOutputTokens)
			}
			if got.OutputReserveTokens != tt.want.OutputReserveTokens {
				t.Errorf("OutputReserveTokens = %d, want %d", got.OutputReserveTokens, tt.want.OutputReserveTokens)
			}
			if got.SafetyMarginTokens != tt.want.SafetyMarginTokens {
				t.Errorf("SafetyMarginTokens = %d, want %d", got.SafetyMarginTokens, tt.want.SafetyMarginTokens)
			}
			if got.SummaryMaxTokens != tt.want.SummaryMaxTokens {
				t.Errorf("SummaryMaxTokens = %d, want %d", got.SummaryMaxTokens, tt.want.SummaryMaxTokens)
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
