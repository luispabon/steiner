package delegation

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// compactionWiringDeps returns child deps with a fallback ref configured and
// ResolveModel and ProviderFactory wired to the given counters. The fallback
// model's BackendModelID is "fallback-model".
func compactionWiringDeps(t *testing.T, resolves, builds *atomic.Int32) SubAgentHandlerDeps {
	t.Helper()
	fallback := compactionFallbackTestModel("fallback-provider", "fallback-model")
	return SubAgentHandlerDeps{
		Config:            *compactionFallbackTestConfig(compactionFallbackTestRef),
		ResolveModel:      compactionFallbackTestResolver(compactionFallbackTestRef, fallback, resolves),
		ProviderFactory:   compactionFallbackTestFactory(builds),
		CompactionTargets: NewCompactionFallbackTargets(),
		SessionID:         "parent-session",
		WorkDir:           t.TempDir(),
		Sandbox:           tool.Unsandboxed{},
		CacheKeyStore:     NewCacheKeyStore(),
	}
}

// buildCompactionWiringChild runs BuildChildRun for a child of agentType whose
// main model is compactionFallbackTestMain.
func buildCompactionWiringChild(t *testing.T, deps SubAgentHandlerDeps, agentType AgentType, agentID string) agent.RunRequest {
	t.Helper()
	req, _, err := BuildChildRun(context.Background(), deps, ChildBootstrapOverrides{
		AgentType:     agentType,
		AllowedTools:  []string{"read"},
		Provider:      &compactionFallbackTestProvider{},
		ResolvedModel: compactionFallbackTestMain(),
	}, Spec{Task: "inspect the repo", AgentID: agentID})
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	return req
}

func TestBuildChildRunWiresCompactionFallbackLazily(t *testing.T) {
	t.Parallel()
	var resolves, builds atomic.Int32
	deps := compactionWiringDeps(t, &resolves, &builds)
	var factorySessionID string
	factory := deps.ProviderFactory
	deps.ProviderFactory = func(model provider.ResolvedModel, sessionID string) (provider.Provider, error) {
		factorySessionID = sessionID
		return factory(model, sessionID)
	}

	fb := buildCompactionWiringChild(t, deps, AgentTypeExplore, "explore-1").CompactionFallback
	if fb.Ref != compactionFallbackTestRef {
		t.Errorf("CompactionFallback.Ref = %q, want %q", fb.Ref, compactionFallbackTestRef)
	}
	if fb.After != 5*time.Minute {
		t.Errorf("CompactionFallback.After = %v, want 5m0s (anthropic default for the main provider)", fb.After)
	}
	if fb.Resolve == nil {
		t.Fatal("CompactionFallback.Resolve = nil, want lazy resolver")
	}
	if n := resolves.Load(); n != 0 {
		t.Fatalf("ResolveModel calls at request build = %d, want 0", n)
	}
	if n := builds.Load(); n != 0 {
		t.Fatalf("ProviderFactory calls at request build = %d, want 0", n)
	}

	target, err := fb.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if target.ResolvedModel.BackendModelID != "fallback-model" || target.Provider == nil {
		t.Fatalf("Resolve() target model = %q, provider nil = %t; want fallback-model with a provider", target.ResolvedModel.BackendModelID, target.Provider == nil)
	}
	if factorySessionID != "parent-session" {
		t.Errorf("ProviderFactory session ID = %q, want parent session %q", factorySessionID, "parent-session")
	}
	if _, err := fb.Resolve(context.Background()); err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if resolves.Load() != 2 || builds.Load() != 1 {
		t.Errorf("after two Resolve calls: ResolveModel = %d, ProviderFactory = %d; want 2 and 1 (target memoized)", resolves.Load(), builds.Load())
	}
}

func TestBuildChildRunCompactionFallbackDisabledWithoutDeps(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*SubAgentHandlerDeps)
	}{
		{name: "nil ResolveModel", mutate: func(d *SubAgentHandlerDeps) { d.ResolveModel = nil }},
		{name: "nil ProviderFactory", mutate: func(d *SubAgentHandlerDeps) { d.ProviderFactory = nil }},
		{name: "nil CompactionTargets", mutate: func(d *SubAgentHandlerDeps) { d.CompactionTargets = nil }},
		{name: "blank fallback ref", mutate: func(d *SubAgentHandlerDeps) { d.Config.Models.Effective.CompactionFallback = "  " }},
		{name: "explicit zero delay", mutate: func(d *SubAgentHandlerDeps) {
			d.Config.Providers["main"] = config.ProviderConfig{
				Type:                    config.ProviderTypeAnthropic,
				CompactionFallbackAfter: config.MustDuration("0s"),
			}
		}},
		{name: "unknown main provider alias", mutate: func(d *SubAgentHandlerDeps) { d.Config.Providers = map[string]config.ProviderConfig{} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var resolves, builds atomic.Int32
			deps := compactionWiringDeps(t, &resolves, &builds)
			tc.mutate(&deps)

			fb := buildCompactionWiringChild(t, deps, AgentTypeExplore, "explore-1").CompactionFallback
			if fb.Ref != "" || fb.After != 0 || fb.Resolve != nil {
				t.Errorf("CompactionFallback Ref = %q, After = %v, Resolve set = %t; want zero fallback", fb.Ref, fb.After, fb.Resolve != nil)
			}
		})
	}
}

func TestBuildChildRunCompactionFallbackMemoScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		secondType       AgentType
		sharedRegistry   bool
		wantSameKey      bool
		wantSameProvider bool
	}{
		{name: "same type sibling shares cache scope and target", secondType: AgentTypeExplore, sharedRegistry: true, wantSameKey: true, wantSameProvider: true},
		{name: "same type in another registry gets its own target", secondType: AgentTypeExplore, sharedRegistry: false, wantSameKey: true, wantSameProvider: false},
		{name: "other type gets its own cache scope and target", secondType: AgentTypeReview, sharedRegistry: true, wantSameKey: false, wantSameProvider: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var resolves, builds atomic.Int32
			first := compactionWiringDeps(t, &resolves, &builds)
			second := first
			if !tc.sharedRegistry {
				second.CompactionTargets = NewCompactionFallbackTargets()
			}

			reqA := buildCompactionWiringChild(t, first, AgentTypeExplore, "explore-1")
			reqB := buildCompactionWiringChild(t, second, tc.secondType, "child-2")
			if got := reqA.PromptCacheKey == reqB.PromptCacheKey; got != tc.wantSameKey {
				t.Fatalf("same PromptCacheKey = %t, want %t", got, tc.wantSameKey)
			}

			targetA, err := reqA.CompactionFallback.Resolve(context.Background())
			if err != nil {
				t.Fatalf("first Resolve() error = %v", err)
			}
			targetB, err := reqB.CompactionFallback.Resolve(context.Background())
			if err != nil {
				t.Fatalf("second Resolve() error = %v", err)
			}
			if got := targetA.Provider == targetB.Provider; got != tc.wantSameProvider {
				t.Errorf("same fallback provider = %t, want %t", got, tc.wantSameProvider)
			}
		})
	}
}

func TestBuildDelegateRegistryCompactionFallbackOwnership(t *testing.T) {
	t.Parallel()
	cfg := compactionWiringRegistryConfig()
	var fallbackBuilds atomic.Int32
	childProvider := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Content: "child result"}, FinishReason: "stop"},
	}}
	base := DelegateDeps{
		BaseRegistry:  tool.NewRegistry(),
		SubAgentCfg:   config.SubAgentConfig{Enabled: true, MaxFollowUps: 1},
		Provider:      childProvider,
		Events:        output.NoopSink{},
		WorkDir:       t.TempDir(),
		HomeDir:       t.TempDir(),
		SessionID:     "parent-session",
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "testprov", EffectiveProviderType: config.ProviderTypeOpenAICompat},
		MaxTokens:     1024,
		Config:        cfg,
		ResolveModel:  resolveModelFunc(cfg),
		ProviderFactory: func(model provider.ResolvedModel, _ string) (provider.Provider, error) {
			if model.BackendModelID == "fallback-model" {
				return &compactionFallbackTestProvider{build: int(fallbackBuilds.Add(1))}, nil
			}
			return childProvider, nil
		},
		CacheKeyStore: NewCacheKeyStore(),
		Sandbox:       tool.Unsandboxed{},
	}
	ctx := context.Background()

	buildRegistry := func(t *testing.T) (*tool.Registry, *SessionStore) {
		t.Helper()
		deps := base
		deps.SessionStore = NewSessionStore()
		reg, err := BuildDelegateRegistry(deps)
		if err != nil {
			t.Fatalf("BuildDelegateRegistry() error = %v", err)
		}
		return reg, deps.SessionStore
	}
	runExplore := func(t *testing.T, reg *tool.Registry, store *SessionStore, agentID string) agent.RunRequest {
		t.Helper()
		def, ok := reg.Get(SubAgentToolName)
		if !ok {
			t.Fatal("sub_agent tool not registered")
		}
		if _, err := def.Handler(agent.WithDelegationAgentID(ctx, agentID), subAgentTask(AgentTypeExplore, "inspect the codebase")); err != nil {
			t.Fatalf("sub_agent handler() error = %v", err)
		}
		session, ok := store.Get(agentID)
		if !ok {
			t.Fatalf("child session %q not saved", agentID)
		}
		return session.Request
	}

	regA, storeA := buildRegistry(t)
	first := runExplore(t, regA, storeA, "explore-a1")
	second := runExplore(t, regA, storeA, "explore-a2")
	for _, req := range []agent.RunRequest{first, second} {
		if req.CompactionFallback.Ref != "fallback" || req.CompactionFallback.Resolve == nil {
			t.Fatalf("child CompactionFallback Ref = %q, Resolve nil = %t; want wired fallback", req.CompactionFallback.Ref, req.CompactionFallback.Resolve == nil)
		}
	}
	if n := fallbackBuilds.Load(); n != 0 {
		t.Fatalf("fallback provider builds after spawning children = %d, want 0 (lazy)", n)
	}
	if first.PromptCacheKey != second.PromptCacheKey {
		t.Fatalf("same-type siblings PromptCacheKey differ: %q vs %q", first.PromptCacheKey, second.PromptCacheKey)
	}
	targetA1, err := first.CompactionFallback.Resolve(ctx)
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}
	targetA2, err := second.CompactionFallback.Resolve(ctx)
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if targetA1.Provider != targetA2.Provider {
		t.Error("same-type siblings in one registry got different fallback providers, want one shared target")
	}
	if n := fallbackBuilds.Load(); n != 1 {
		t.Fatalf("fallback provider builds for shared scope = %d, want 1", n)
	}

	regB, storeB := buildRegistry(t)
	third := runExplore(t, regB, storeB, "explore-b1")
	if third.PromptCacheKey != first.PromptCacheKey {
		t.Fatalf("cross-registry PromptCacheKey = %q, want shared key %q so only registry ownership differs", third.PromptCacheKey, first.PromptCacheKey)
	}
	targetB, err := third.CompactionFallback.Resolve(ctx)
	if err != nil {
		t.Fatalf("third Resolve() error = %v", err)
	}
	if targetB.Provider == targetA1.Provider {
		t.Error("second registry reused the first registry's fallback provider, want its own target")
	}
	if n := fallbackBuilds.Load(); n != 2 {
		t.Fatalf("fallback provider builds after second registry = %d, want 2", n)
	}
}

// compactionWiringRegistryConfig returns the advisor test config with a
// testprov cold-cache delay of one minute and a "fallback" model on testprov.
func compactionWiringRegistryConfig() config.Config {
	cfg := advisorTestConfig()
	cfg.Providers["testprov"] = config.ProviderConfig{
		Type:                    config.ProviderTypeOpenAICompat,
		BaseURL:                 "http://example.invalid",
		Timeout:                 config.MustDuration("180s"),
		CompactionFallbackAfter: config.MustDuration("1m"),
	}
	cfg.Models.Definitions["fallback"] = config.ModelConfig{
		Provider: "testprov",
		ID:       "fallback-model",
		Advanced: config.AdvancedConfig{
			Limits: config.AdvancedLimitsConfig{
				ContextWindow:   8192,
				MaxOutputTokens: 1024,
			},
		},
	}
	cfg.Models.Effective.CompactionFallback = "fallback"
	return cfg
}
