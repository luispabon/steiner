package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func advisorTestConfig() config.Config {
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"testprov": {
				Type:    config.ProviderTypeOpenAICompat,
				BaseURL: "http://example.invalid",
				Timeout: config.MustDuration("180s"),
			},
		},
		Models: config.ModelsConfig{
			Effective: config.EffectiveModelAssignments{Advisor: "advisor"},
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
}

func callAdvisorHandler(t *testing.T, reg *tool.Registry) {
	t.Helper()
	def, ok := reg.Get(advisor.ToolName)
	if !ok {
		t.Fatal("advisor tool not registered")
	}
	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hi"},
	})
	if _, err := def.Handler(ctx, map[string]any{"question": "test"}); err != nil {
		t.Fatalf("advisor handler() error = %v", err)
	}
}

func TestBuildDelegateRegistryAdvisorCacheKeyStableAcrossCalls(t *testing.T) {
	store := NewCacheKeyStore()
	prov := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Content: "ok"}, FinishReason: "stop"},
	}}
	providerFactory := func(provider.ResolvedModel) (provider.Provider, error) { return prov, nil }

	deps := DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: false},
		AdvisorCfg:   config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 5},
		Provider:     prov,
		Events:       output.NoopSink{},
		WorkDir:      "/tmp/work",
		ResolvedModel: provider.ResolvedModel{
			ProviderAlias:         "testprov",
			EffectiveProviderType: config.ProviderTypeOpenAICompat,
		},
		MaxTokens:       256,
		Config:          advisorTestConfig(),
		ProviderFactory: providerFactory,
		CacheKeyStore:   store,
	}

	reg1, err := BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() call 1 error = %v", err)
	}
	reg2, err := BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() call 2 error = %v", err)
	}

	callAdvisorHandler(t, reg1)
	callAdvisorHandler(t, reg2)

	if len(prov.requests) != 2 {
		t.Fatalf("captured %d requests, want 2", len(prov.requests))
	}
	if prov.requests[0].PromptCacheKey == "" {
		t.Fatal("first request PromptCacheKey is empty, want a minted key")
	}
	if prov.requests[0].PromptCacheKey != prov.requests[1].PromptCacheKey {
		t.Errorf("advisor PromptCacheKey differs across BuildDelegateRegistry calls sharing a CacheKeyStore: %q vs %q",
			prov.requests[0].PromptCacheKey, prov.requests[1].PromptCacheKey)
	}
}

func TestBuildDelegateRegistryAdvisorFallsBackToProfileDefault(t *testing.T) {
	cfg := advisorTestConfig()
	cfg.Models.Effective.Advisor = ""
	cfg.Models.Effective.DefaultModel = "profile-default"
	cfg.Models.Definitions["profile-default"] = config.ModelConfig{
		Provider: "testprov",
		ID:       "profile-default-model",
		Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{ContextWindow: 8192, MaxOutputTokens: 1024}},
	}

	var captured provider.ResolvedModel
	_, err := BuildDelegateRegistry(DelegateDeps{
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
		ProviderFactory: func(model provider.ResolvedModel) (provider.Provider, error) {
			captured = model
			return &fakeProvider{}, nil
		},
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	if captured.Alias != "profile-default" || captured.BackendModelID != "profile-default-model" {
		t.Fatalf("advisor resolved model = alias %q, backend %q, want profile-default/profile-default-model", captured.Alias, captured.BackendModelID)
	}
}

func TestBuildDelegateRegistryAdvisorNamedProfileFallsBackToProfileDefault(t *testing.T) {
	cfg := advisorTestConfig()
	cfg.Models.Effective.ProfileName = "named"
	cfg.Models.Effective.Advisor = ""
	cfg.Models.Effective.DefaultModel = "named-profile-default"
	cfg.Models.Definitions["named-profile-default"] = config.ModelConfig{
		Provider: "testprov",
		ID:       "named-profile-default-model",
		Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{ContextWindow: 8192, MaxOutputTokens: 1024}},
	}

	var captured provider.ResolvedModel
	_, err := BuildDelegateRegistry(DelegateDeps{
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
		ProviderFactory: func(model provider.ResolvedModel) (provider.Provider, error) {
			captured = model
			return &fakeProvider{}, nil
		},
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	if captured.Alias != "named-profile-default" || captured.BackendModelID != "named-profile-default-model" {
		t.Fatalf("advisor resolved model = alias %q, backend %q, want named-profile-default/named-profile-default-model", captured.Alias, captured.BackendModelID)
	}
}

func TestBuildDelegateRegistryAdvisorProfileDefaultResolverError(t *testing.T) {
	cfg := advisorTestConfig()
	cfg.Models.Effective.Advisor = ""
	cfg.Models.Effective.DefaultModel = "missing-profile-default"
	_, err := BuildDelegateRegistry(DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: false},
		AdvisorCfg:   config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 1},
		Provider:     &fakeProvider{},
		Events:       output.NoopSink{},
		WorkDir:      "/tmp/work",
		MaxTokens:    256,
		Config:       cfg,
	})
	if err == nil {
		t.Fatal("expected profile default resolution error")
	}
	if !strings.Contains(err.Error(), "missing-profile-default") || !strings.Contains(err.Error(), "model alias") {
		t.Fatalf("error = %q, want profile default alias and resolver detail", err)
	}
}

func TestBuildDelegateRegistryExcludesVisionForEmptyAssignment(t *testing.T) {
	cfg := advisorTestConfig()
	cfg.Models.Effective.DefaultModel = "advisor"
	cfg.Models.Effective.SubAgents = map[string]string{string(AgentTypeVision): ""}
	reg, err := BuildDelegateRegistry(DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: true},
		Provider:     &fakeProvider{},
		Events:       output.NoopSink{},
		WorkDir:      "/tmp/work",
		MaxTokens:    256,
		Config:       cfg,
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	if _, ok := reg.Get(string(AgentTypeVision)); ok {
		t.Fatal("vision tool registered for empty vision assignment")
	}
}

func TestBuildDelegateRegistryAdvisorBudgetPersistsAcrossCallsViaAdvisorState(t *testing.T) {
	state := advisor.NewSharedState()
	prov := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Content: "ok"}, FinishReason: "stop"},
	}}
	providerFactory := func(provider.ResolvedModel) (provider.Provider, error) { return prov, nil }

	deps := DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: false},
		AdvisorCfg:   config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 1},
		Provider:     prov,
		Events:       output.NoopSink{},
		WorkDir:      "/tmp/work",
		ResolvedModel: provider.ResolvedModel{
			ProviderAlias:         "testprov",
			EffectiveProviderType: config.ProviderTypeOpenAICompat,
		},
		MaxTokens:       256,
		Config:          advisorTestConfig(),
		ProviderFactory: providerFactory,
		AdvisorState:    state,
	}

	// Simulate two turns: BuildDelegateRegistry runs once per turn, each time
	// building a fresh advisor handler, but both share the process-lifetime
	// AdvisorState.
	reg1, err := BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() call 1 error = %v", err)
	}
	reg2, err := BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() call 2 error = %v", err)
	}

	callAdvisorHandler(t, reg1)

	def, ok := reg2.Get(advisor.ToolName)
	if !ok {
		t.Fatal("advisor tool not registered on reg2")
	}
	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hi"},
	})
	got, err := def.Handler(ctx, map[string]any{"question": "test"})
	if err != nil {
		t.Fatalf("second-turn advisor handler() error = %v", err)
	}

	want := advisor.BudgetExhaustedMessage(1, 1)
	if got != want {
		t.Fatalf("second-turn handler() = %#v, want %q (budget should persist across turns via AdvisorState)", got, want)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("captured %d provider requests, want 1", len(prov.requests))
	}
}

func TestBuildDelegateRegistryAdvisorCacheKeyFallsBackWhenStoreNil(t *testing.T) {
	prov := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Content: "ok"}, FinishReason: "stop"},
	}}

	reg, err := BuildDelegateRegistry(DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: false},
		AdvisorCfg:   config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 5},
		Provider:     prov,
		Events:       output.NoopSink{},
		WorkDir:      "/tmp/work",
		ResolvedModel: provider.ResolvedModel{
			ProviderAlias:         "testprov",
			EffectiveProviderType: config.ProviderTypeOpenAICompat,
		},
		MaxTokens: 256,
		Config:    advisorTestConfig(),
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}

	callAdvisorHandler(t, reg)

	if len(prov.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(prov.requests))
	}
	if prov.requests[0].PromptCacheKey == "" {
		t.Fatal("PromptCacheKey is empty, want a freshly minted key when CacheKeyStore is nil")
	}
}

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
			Effective: config.EffectiveModelAssignments{Advisor: "advisor"},
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
			Effective: config.EffectiveModelAssignments{Advisor: "advisor"},
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
