package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

func TestCLIRunnerCompactUsesResolvedLimitsAndAssembly(t *testing.T) {
	const modelID = "openrouter/test-model"
	const discoveredContextWindow = 262144
	const discoveredMaxTokens = 8192

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"id": modelID, "context_length": discoveredContextWindow,
			"top_provider": map[string]any{"max_completion_tokens": discoveredMaxTokens},
		}}})
	}))
	defer srv.Close()

	projectRoot := t.TempDir()
	prov := &fakeProvider{responses: []provider.ChatResponse{{
		Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"}, FinishReason: "stop",
	}}}
	var captured provider.ResolvedModel
	r := cliRunner{
		runtime: cliRuntime{
			cfg: config.Config{
				Providers: map[string]config.ProviderConfig{"openrouter": {Type: config.ProviderTypeOpenRouter, BaseURL: srv.URL}},
				Models:    config.ModelsConfig{Default: "test", Definitions: map[string]config.ModelConfig{"test": {Provider: "openrouter", ID: modelID}}},
				SubAgent:  config.SubAgentConfig{Enabled: true}, Advisor: config.AdvisorConfig{Enabled: true},
			},
			providerFactory: func(rm provider.ResolvedModel) (provider.Provider, error) { captured = rm; return prov, nil },
			httpClient:      srv.Client(), projectRoot: projectRoot, workDir: projectRoot, homeDir: projectRoot,
		},
		currentAlias: func() string { return "test" },
	}

	conversation, err := r.Compact(context.Background(), []agent.Message{
		{Role: agent.MessageRoleUser, Content: "first request"}, {Role: agent.MessageRoleAssistant, Content: "first answer"},
		{Role: agent.MessageRoleUser, Content: "second request"}, {Role: agent.MessageRoleAssistant, Content: "second answer"},
	}, nil, nil)
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
