package delegation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
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

func TestBuildDelegateRegistryChildFactoryKeepsParentSessionSeparateFromCacheKey(t *testing.T) {
	const parentSessionID = "parent-session-id"
	childProvider := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Content: "child result"}, FinishReason: "stop"},
	}}
	var factorySessionID string
	providerFactory := func(_ provider.ResolvedModel, sessionID string) (provider.Provider, error) {
		factorySessionID = sessionID
		return childProvider, nil
	}

	cfg := advisorTestConfig()
	cfg.Models.Effective.DefaultModel = "advisor"
	deps := DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: true, MaxFollowUps: 1},
		Provider:     childProvider,
		Events:       output.NoopSink{},
		WorkDir:      t.TempDir(),
		HomeDir:      t.TempDir(),
		SessionID:    parentSessionID,
		ResolvedModel: provider.ResolvedModel{
			ProviderAlias:         "testprov",
			EffectiveProviderType: config.ProviderTypeOpenAICompat,
		},
		MaxTokens:       1024,
		Config:          cfg,
		ProviderFactory: providerFactory,
		CacheKeyStore:   NewCacheKeyStore(),
		Sandbox:         tool.Unsandboxed{},
	}

	registry, err := BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	def, ok := registry.Get(SubAgentToolName)
	if !ok {
		t.Fatal("sub_agent tool not registered")
	}
	if _, err := def.Handler(context.Background(), subAgentTask(AgentTypeExplore, "inspect the codebase")); err != nil {
		t.Fatalf("sub_agent handler() error = %v", err)
	}

	if factorySessionID != parentSessionID {
		t.Errorf("ProviderFactory session ID = %q, want parent %q", factorySessionID, parentSessionID)
	}
	if len(childProvider.requests) != 1 {
		t.Fatalf("captured %d child provider requests, want 1", len(childProvider.requests))
	}
	childCacheKey := childProvider.requests[0].PromptCacheKey
	if childCacheKey == "" {
		t.Fatal("child PromptCacheKey is empty, want separately allocated key")
	}
	if childCacheKey == parentSessionID {
		t.Fatalf("child PromptCacheKey = parent SessionID %q, want separate scope", childCacheKey)
	}
}

func TestBuildDelegateRegistryDisablesChildLSPGuidanceWithoutServers(t *testing.T) {
	for _, servers := range []map[string]config.LSPServerConfig{nil, {}} {
		t.Run("enabled LSP without configured servers", func(t *testing.T) {
			fake := &fakeProvider{responses: []provider.ChatResponse{
				{Message: provider.Message{Content: "child result"}, FinishReason: "stop"},
			}}
			sessions := NewSessionStore()
			deps := DelegateDeps{
				BaseRegistry: tool.NewRegistry(),
				SubAgentCfg:  config.SubAgentConfig{Enabled: true, MaxFollowUps: 1},
				Provider:     fake, Events: output.NoopSink{}, WorkDir: t.TempDir(), HomeDir: t.TempDir(),
				ResolvedModel: provider.ResolvedModel{EffectiveLimits: provider.EffectiveLimits{ContextWindow: 32768, MaxOutputTokens: 1024}},
				MaxTokens:     1024, Config: config.Config{LSP: config.LSPConfig{Enabled: true, Servers: servers}},
				Sandbox: tool.Unsandboxed{}, SessionStore: sessions,
			}
			registry, err := BuildDelegateRegistry(deps)
			if err != nil {
				t.Fatalf("BuildDelegateRegistry() error = %v", err)
			}
			def, ok := registry.Get(SubAgentToolName)
			if !ok {
				t.Fatal("sub_agent tool not registered")
			}
			result, err := def.Handler(context.Background(), subAgentTask("explore", "inspect the codebase"))
			if err != nil {
				t.Fatalf("sub_agent handler() error = %v", err)
			}
			delegationResult, ok := result.(tool.ExecutionResult)
			if !ok {
				t.Fatalf("sub_agent result = %T, want tool.ExecutionResult", result)
			}
			childResult, ok := delegationResult.Value.(Result)
			if !ok {
				t.Fatalf("delegation result value = %T, want delegation.Result", delegationResult.Value)
			}
			session, ok := sessions.Get(childResult.AgentID)
			if !ok {
				t.Fatalf("child session for agent %q not saved", childResult.AgentID)
			}
			if strings.Contains(session.Request.Prompt.PromptOverrides.SystemSuffix, "## Code intelligence (LSP)") {
				t.Fatalf("child system suffix = %q, want no LSP guidance", session.Request.Prompt.PromptOverrides.SystemSuffix)
			}
		})
	}
}

func TestBuildDelegateRegistryAdvisorCacheKeyStableAcrossCalls(t *testing.T) {
	store := NewCacheKeyStore()
	prov := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Content: "ok"}, FinishReason: "stop"},
	}}
	providerFactory := func(provider.ResolvedModel, string) (provider.Provider, error) { return prov, nil }

	deps := DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: false, MaxFollowUps: 100},
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
		SubAgentCfg:  config.SubAgentConfig{Enabled: false, MaxFollowUps: 100},
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
		ProviderFactory: func(model provider.ResolvedModel, _ string) (provider.Provider, error) {
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
		SubAgentCfg:  config.SubAgentConfig{Enabled: false, MaxFollowUps: 100},
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
		ProviderFactory: func(model provider.ResolvedModel, _ string) (provider.Provider, error) {
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
		SubAgentCfg:  config.SubAgentConfig{Enabled: false, MaxFollowUps: 100},
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
		SubAgentCfg:  config.SubAgentConfig{Enabled: true, MaxFollowUps: 100},
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
	providerFactory := func(provider.ResolvedModel, string) (provider.Provider, error) { return prov, nil }

	deps := DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: false, MaxFollowUps: 100},
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
		SubAgentCfg:  config.SubAgentConfig{Enabled: false, MaxFollowUps: 100},
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
			providerFactory := func(m provider.ResolvedModel, _ string) (provider.Provider, error) {
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
		SubAgentCfg:  config.SubAgentConfig{Enabled: false, MaxFollowUps: 100},
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

func TestBuildDelegateRegistryAdvisorNotRegisteredWhenDisabled(t *testing.T) {
	reg, err := BuildDelegateRegistry(DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: false, MaxFollowUps: 100},
		AdvisorCfg:   config.AdvisorConfig{Enabled: false},
		Provider:     &fakeProvider{},
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
	if _, ok := reg.Get(advisor.ToolName); ok {
		t.Fatal("advisor tool registered when disabled")
	}
}

func TestBuildDelegateRegistryAdvisorUsesConfigMaxUsesPerRun(t *testing.T) {
	state := advisor.NewSharedState()
	prov := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Content: "ok"}, FinishReason: "stop"},
	}}
	providerFactory := func(provider.ResolvedModel, string) (provider.Provider, error) { return prov, nil }

	deps := DelegateDeps{
		BaseRegistry: tool.NewRegistry(),
		SubAgentCfg:  config.SubAgentConfig{Enabled: false, MaxFollowUps: 100},
		AdvisorCfg:   config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 2},
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

	reg, err := BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}

	def, ok := reg.Get(advisor.ToolName)
	if !ok {
		t.Fatal("advisor tool not registered")
	}

	ctx := agent.WithConversationSnapshot(context.Background(), []provider.Message{
		{Role: provider.MessageRoleUser, Content: "hi"},
	})

	callAdvisorHandler(t, reg)
	callAdvisorHandler(t, reg)

	def, ok = reg.Get(advisor.ToolName)
	if !ok {
		t.Fatal("advisor tool not registered on third call")
	}

	got, err := def.Handler(ctx, map[string]any{"question": "test"})
	if err != nil {
		t.Fatalf("third call handler() error = %v", err)
	}

	want := advisor.BudgetExhaustedMessage(2, 2)
	if got != want {
		t.Fatalf("third call handler() = %#v, want %q (MaxUsesPerRun=2 from config)", got, want)
	}
}

func TestBuildDelegateRegistryCopiesSessionDate(t *testing.T) {
	t.Parallel()

	testTime := time.Date(2026, 9, 13, 10, 30, 0, 0, time.UTC)
	sessionDate := prompt.NewSessionDate(testTime)

	// Verify that the SessionDate was passed through to SubAgentHandlerDeps
	// by checking that a child run would receive it.
	maxTokens := 1000
	_, _, err := BuildChildRun(context.Background(), SubAgentHandlerDeps{
		Provider:         stubProvider{},
		ParentReg:        tool.NewRegistry(),
		WorkDir:          "/tmp/work",
		HomeDir:          "/home/user",
		SessionDate:      sessionDate,
		SubAgentCfg:      config.SubAgentConfig{Enabled: true},
		Events:           output.NoopSink{},
		Runner:           agent.NewRunner(),
		ResolvedModel:    provider.ResolvedModel{BackendModelID: "test-model"},
		MaxTokens:        &maxTokens,
		SandboxEnabled:   false,
		Sandbox:          tool.Unsandboxed{},
	}, ChildBootstrapOverrides{
		AgentType:     AgentTypeCode,
		AllowedTools:  []string{},
		Provider:      stubProvider{},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
	}, Spec{
		AgentID: "test-agent",
		Task:    "test task",
	})
	if err != nil {
		t.Fatalf("BuildChildRun error = %v", err)
	}
}
