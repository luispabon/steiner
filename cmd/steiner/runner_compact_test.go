package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
	"github.com/luispabon/steiner/internal/modelcatalog"
	"github.com/luispabon/steiner/internal/provider"
)

func TestCLIRunnerCompactUsesResolvedLimitsAndAssembly(t *testing.T) {
	const modelID = "openrouter/test-model"
	const discoveredContextWindow = 262144
	const discoveredMaxTokens = 8192
	const providerBaseURL = "https://openrouter.example/api/v1"

	// Isolate the models.dev cache so this test never touches the real
	// on-disk cache or network: catalog answers context/max output, but
	// modelsDevSource still runs for the remaining unconfigured facts
	// (vision, reasoning efforts, echo-back).
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mdCache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(mdCache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(mdCache.CachePath(), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(mdCache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	cacheDir := t.TempDir()
	cache := modelcatalog.NewCache(cacheDir)
	envelope := modelcatalog.CacheEnvelope{
		Fingerprint: modelcatalog.CacheFingerprint{ProviderType: string(config.ProviderTypeOpenRouter), BaseURL: providerBaseURL},
		FetchedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Hour),
		Models: []modelcatalog.DiscoveredModel{
			{ID: modelID, ContextLength: discoveredContextWindow, MaxOutputTokens: discoveredMaxTokens},
		},
	}
	if err := cache.SaveAtomic("openrouter", envelope); err != nil {
		t.Fatalf("SaveAtomic() error = %v", err)
	}
	modelCatalog := modelcatalog.NewService(nil, cache, modelcatalog.NewStore(cacheDir+"/popularity.json"), nil, true)

	projectRoot := t.TempDir()
	prov := &fakeProvider{responses: []provider.ChatResponse{{
		Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"}, FinishReason: "stop",
	}}}
	var captured provider.ResolvedModel
	r := cliRunner{
		runtime: cliRuntime{
			cfg: config.Config{
				Providers: map[string]config.ProviderConfig{"openrouter": {Type: config.ProviderTypeOpenRouter, BaseURL: providerBaseURL}},
				Models: config.ModelsConfig{
					Effective: config.EffectiveModelAssignments{
						DefaultModel:            "test",
						ActiveOrchestratorModel: "test",
					},
					Definitions: map[string]config.ModelConfig{"test": {Provider: "openrouter", ID: modelID}},
				},
				SubAgent: config.SubAgentConfig{Enabled: true}, Advisor: config.AdvisorConfig{Enabled: true},
			},
			providerFactory: func(rm provider.ResolvedModel, _ string) (provider.Provider, error) { captured = rm; return prov, nil },
			modelCatalog:    modelCatalog, projectRoot: projectRoot, workDir: projectRoot, homeDir: projectRoot,
		},
		currentAlias: func() string { return "test" },
	}

	conversation, err := r.Compact(context.Background(), []agent.Message{
		{Role: agent.MessageRoleUser, Content: "first request"}, {Role: agent.MessageRoleAssistant, Content: "first answer"},
		{Role: agent.MessageRoleUser, Content: "second request"}, {Role: agent.MessageRoleAssistant, Content: "second answer"},
	}, nil, nil, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(conversation) == 0 {
		t.Fatal("Compact() returned empty conversation")
	}
	if got := captured.EffectiveLimits.ContextWindow; got != discoveredContextWindow {
		t.Fatalf("context window = %d, want %d", got, discoveredContextWindow)
	}
	if got := captured.EffectiveLimits.MaxOutputTokens; got != discoveredMaxTokens {
		t.Fatalf("max output tokens = %d, want %d", got, discoveredMaxTokens)
	}
	if len(prov.requests) == 0 {
		t.Fatal("Compact() made no provider request")
	}
	preamble := prov.requests[0].Messages[0].Content
	if !strings.Contains(preamble, "## Your role") {
		t.Fatalf("preamble missing delegation role section:\n%s", preamble)
	}
	if !strings.Contains(preamble, "## Advisor") {
		t.Fatalf("preamble missing advisor section:\n%s", preamble)
	}
}
