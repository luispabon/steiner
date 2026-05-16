package provider

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
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
