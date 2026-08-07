package delegation

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestBuildDelegateRegistryAppliesAdvisorTimeout(t *testing.T) {
	// Build a minimal config that allows ResolveWithDiscovery to succeed
	// without making any real HTTP calls (limits are fully configured).
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"testprov": {
				Type:    config.ProviderTypeOpenAICompat,
				BaseURL: "http://example.invalid",
				Timeout: config.MustDuration("180s"),
			},
		},
		Models: config.ModelsConfig{
			Advisor: "advisor",
			Definitions: map[string]config.ModelConfig{
				"advisor": {
					Provider: "testprov",
					ID:       "advisor-model",
					Advanced: config.AdvancedConfig{
						Limits: config.AdvancedLimitsConfig{
							ContextWindow:   8192,
							MaxOutputTokens: 1024,
						},
					},
				},
			},
		},
	}

	timeout300s := config.MustDuration("300s")
	timeout5s := config.MustDuration("5s")

	tests := []struct {
		name          string
		advisorCfg    config.AdvisorConfig
		wantTimeout   config.Duration
		parentTimeout config.Duration
	}{
		{
			name: "default timeout is applied",
			advisorCfg: config.AdvisorConfig{
				Enabled:       true,
				MaxUsesPerRun: 1,
				Timeout:       nil,
			},
			wantTimeout:   config.MustDuration("180s"),
			parentTimeout: timeout5s,
		},
		{
			name: "explicit override is applied",
			advisorCfg: config.AdvisorConfig{
				Enabled:       true,
				MaxUsesPerRun: 1,
				Timeout:       &timeout300s,
			},
			wantTimeout:   timeout300s,
			parentTimeout: timeout5s,
		},
		{
			name: "primary model timeout is left untouched",
			advisorCfg: config.AdvisorConfig{
				Enabled:       true,
				MaxUsesPerRun: 1,
				Timeout:       nil,
			},
			wantTimeout:   config.MustDuration("180s"),
			parentTimeout: timeout5s,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured provider.ResolvedModel
			providerFactory := func(m provider.ResolvedModel) (provider.Provider, error) {
				captured = m
				return &fakeProvider{}, nil
			}

			parentResolved := provider.ResolvedModel{
				ProviderAlias:         "testprov",
				EffectiveProviderType: config.ProviderTypeOpenAICompat,
				ProviderConfig: config.ProviderConfig{
					Timeout: tt.parentTimeout,
				},
			}
			originalParentTimeout := parentResolved.ProviderConfig.Timeout

			_, err := BuildDelegateRegistry(DelegateDeps{
				BaseRegistry:    tool.NewRegistry(),
				SubAgentCfg:     config.SubAgentConfig{Enabled: false},
				AdvisorCfg:      tt.advisorCfg,
				Provider:        &fakeProvider{},
				Events:          output.NoopSink{},
				WorkDir:         "/tmp/work",
				ResolvedModel:   parentResolved,
				MaxTokens:       256,
				Config:          cfg,
				ProviderFactory: providerFactory,
			})
			if err != nil {
				t.Fatalf("BuildDelegateRegistry() error = %v", err)
			}

			if captured.ProviderConfig.Timeout != tt.wantTimeout {
				t.Errorf("captured provider Timeout = %v, want %v", captured.ProviderConfig.Timeout.Duration(), tt.wantTimeout.Duration())
			}

			if parentResolved.ProviderConfig.Timeout != originalParentTimeout {
				t.Errorf("parent ResolvedModel.ProviderConfig.Timeout was mutated: got %v, want %v",
					parentResolved.ProviderConfig.Timeout.Duration(), originalParentTimeout.Duration())
			}
		})
	}
}

func TestBuildDelegateRegistryRegistersAdvisorSchemaWithQuestionAndFiles(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"testprov": {
				Type:    config.ProviderTypeOpenAICompat,
				BaseURL: "http://example.invalid",
				Timeout: config.MustDuration("180s"),
			},
		},
		Models: config.ModelsConfig{
			Advisor: "advisor",
			Definitions: map[string]config.ModelConfig{
				"advisor": {
					Provider: "testprov",
					ID:       "advisor-model",
					Advanced: config.AdvancedConfig{
						Limits: config.AdvancedLimitsConfig{
							ContextWindow:   8192,
							MaxOutputTokens: 1024,
						},
					},
				},
			},
		},
	}

	reg, err := BuildDelegateRegistry(DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: false},
		AdvisorCfg:   config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 1},
		Provider:     &fakeProvider{},
		Events:       output.NoopSink{},
		WorkDir:      "/tmp/work",
		ResolvedModel: provider.ResolvedModel{
			ProviderAlias:         "testprov",
			EffectiveProviderType: config.ProviderTypeOpenAICompat,
		},
		MaxTokens: 256,
		Config:    cfg,
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}

	def, ok := reg.Get("advisor")
	if !ok {
		t.Fatal("advisor tool not registered")
	}
	props, ok := def.ParameterSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %T, want map[string]any", def.ParameterSchema["properties"])
	}
	if _, ok := props["question"]; !ok {
		t.Fatal("registered advisor schema missing question property")
	}
	if _, ok := props["files"]; !ok {
		t.Fatal("registered advisor schema missing files property")
	}
}
