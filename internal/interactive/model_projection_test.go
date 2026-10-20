package interactive

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestBuildModelProjection(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want ModelProjection
	}{
		{
			name: "no definitions leaves every field nil",
			cfg:  config.Config{},
			want: ModelProjection{},
		},
		{
			name: "unknown provider keeps alias and backend ID only",
			cfg: config.Config{Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
				"a": {Provider: "missing", ID: "model-a"},
			}}},
			want: ModelProjection{
				AliasNames:     []string{"a"},
				BackendAliases: map[string]string{"model-a": "a"},
				ContextSizes:   map[string]int{},
				BaseURLs:       map[string]string{},
				ProviderNames:  map[string]string{},
			},
		},
		{
			name: "zero context window is skipped and the map stays non-nil",
			cfg: config.Config{
				Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
					"a": {Provider: "p", ID: "model-a"},
				}},
				Providers: map[string]config.ProviderConfig{"p": {BaseURL: "http://p"}},
			},
			want: ModelProjection{
				AliasNames:     []string{"a"},
				BackendAliases: map[string]string{"model-a": "a"},
				ContextSizes:   map[string]int{},
				BaseURLs:       map[string]string{"a": "http://p"},
				ProviderNames:  map[string]string{"a": "p"},
			},
		},
		{
			name: "negative context window is skipped",
			cfg: config.Config{Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
				"a": {Provider: "p", ID: "model-a", Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{ContextWindow: -1}}},
			}}, Providers: map[string]config.ProviderConfig{"p": {}}},
			want: ModelProjection{
				AliasNames:     []string{"a"},
				BackendAliases: map[string]string{"model-a": "a"},
				ContextSizes:   map[string]int{},
				BaseURLs:       map[string]string{"a": ""},
				ProviderNames:  map[string]string{"a": "p"},
			},
		},
		{
			name: "empty base URL is still recorded for a known provider",
			cfg: config.Config{
				Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
					"a": {Provider: "p", ID: "model-a"},
				}},
				Providers: map[string]config.ProviderConfig{"p": {}},
			},
			want: ModelProjection{
				AliasNames:     []string{"a"},
				BackendAliases: map[string]string{"model-a": "a"},
				ContextSizes:   map[string]int{},
				BaseURLs:       map[string]string{"a": ""},
				ProviderNames:  map[string]string{"a": "p"},
			},
		},
		{
			name: "multiple models are sorted by alias with mixed providers",
			cfg: config.Config{
				Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
					"zeta":  {Provider: "known", ID: "id-z", Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{ContextWindow: 8192}}},
					"alpha": {Provider: "unknown", ID: "id-a", Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{ContextWindow: 4096}}},
					"Mid":   {Provider: "known", ID: "id-m"},
				}},
				Providers: map[string]config.ProviderConfig{"known": {BaseURL: "https://known"}},
			},
			want: ModelProjection{
				AliasNames:     []string{"Mid", "alpha", "zeta"},
				BackendAliases: map[string]string{"id-z": "zeta", "id-a": "alpha", "id-m": "Mid"},
				ContextSizes:   map[string]int{"zeta": 8192, "alpha": 4096},
				BaseURLs:       map[string]string{"zeta": "https://known", "Mid": "https://known"},
				ProviderNames:  map[string]string{"zeta": "known", "Mid": "known"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildModelProjection(tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("BuildModelProjection() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSelectedModelConfig(t *testing.T) {
	tests := []struct {
		name         string
		cfg          config.Config
		wantProvider string
		wantID       string
	}{
		{
			name: "configured alias",
			cfg: config.Config{
				Models: config.ModelsConfig{
					Effective:   config.EffectiveModelAssignments{DefaultModel: "alias"},
					Definitions: map[string]config.ModelConfig{"alias": {Provider: "local", ID: "configured-id"}},
				},
				Providers: map[string]config.ProviderConfig{"local": {}},
			},
			wantProvider: "local",
			wantID:       "configured-id",
		},
		{
			name: "raw reference",
			cfg: config.Config{
				Models:    config.ModelsConfig{Effective: config.EffectiveModelAssignments{DefaultModel: "openrouter/openai/gpt-4"}},
				Providers: map[string]config.ProviderConfig{"openrouter": {}},
			},
			wantProvider: "openrouter",
			wantID:       "openai/gpt-4",
		},
		{
			name: "unknown reference",
			cfg: config.Config{
				Models: config.ModelsConfig{Effective: config.EffectiveModelAssignments{DefaultModel: "garbage"}},
			},
		},
		{
			name: "active orchestrator model overrides the default",
			cfg: config.Config{
				Models: config.ModelsConfig{
					Effective: config.EffectiveModelAssignments{DefaultModel: "default-alias", ActiveOrchestratorModel: "active-alias"},
					Definitions: map[string]config.ModelConfig{
						"active-alias":  {Provider: "local", ID: "active-id"},
						"default-alias": {Provider: "local", ID: "default-id"},
					},
				},
				Providers: map[string]config.ProviderConfig{"local": {}},
			},
			wantProvider: "local",
			wantID:       "active-id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectedModelConfig(tt.cfg)
			if got.Provider != tt.wantProvider || got.ID != tt.wantID {
				t.Fatalf("SelectedModelConfig() = provider=%q id=%q, want provider=%q id=%q", got.Provider, got.ID, tt.wantProvider, tt.wantID)
			}
		})
	}
}

func TestSortedProfileNames(t *testing.T) {
	tests := []struct {
		name     string
		profiles map[string]config.ModelProfile
		want     []string
	}{
		{name: "nil profiles yield an empty non-nil slice", profiles: nil, want: []string{}},
		{name: "empty profiles yield an empty non-nil slice", profiles: map[string]config.ModelProfile{}, want: []string{}},
		{
			name: "names are sorted",
			profiles: map[string]config.ModelProfile{
				"fast":     {},
				"default":  {},
				"careful":  {},
				"advanced": {},
			},
			want: []string{"advanced", "careful", "default", "fast"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SortedProfileNames(tt.profiles)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SortedProfileNames() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
