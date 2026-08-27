package main

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/modelcatalog"
	"github.com/luispabon/steiner/internal/tui"
)

func TestModelEntriesFromChoices(t *testing.T) {
	got := modelEntriesFromChoices([]modelcatalog.ModelChoice{{
		Ref:              "openai/gpt-5",
		Display:          "GPT 5",
		SupportedEfforts: []string{"low", "high"},
		Current:          true,
	}})
	want := []tui.ModelEntry{{
		Ref:              "openai/gpt-5",
		Display:          "GPT 5",
		SupportedEfforts: []string{"low", "high"},
		Current:          true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("modelEntriesFromChoices() = %#v, want %#v", got, want)
	}
}

func TestModelEntriesFromChoicesIncludesConfigOnlyChoicesWhenDiscoveryDisabled(t *testing.T) {
	cfg := config.Config{
		Models: config.ModelsConfig{
			Effective: config.EffectiveModelAssignments{
				DefaultModel:            "local",
				ActiveOrchestratorModel: "local",
			},
			Definitions: map[string]config.ModelConfig{
				"local": {
					Provider: "ollama",
					ID:       "qwen",
					Advanced: config.AdvancedConfig{Reasoning: config.ReasoningConfig{SupportedEfforts: []string{"low"}}},
				},
			},
		},
		Providers: map[string]config.ProviderConfig{"ollama": {Type: config.ProviderTypeOllama}},
	}
	service := modelcatalog.NewService(nil, modelcatalog.NewCache(t.TempDir()), modelcatalog.NewStore(t.TempDir()+"/popularity.json"), nil, false)
	entries := modelEntriesFromChoices(service.Choices(&cfg, "local"))
	if len(entries) != 1 || entries[0].Ref != "local" || !entries[0].Current || !reflect.DeepEqual(entries[0].SupportedEfforts, []string{"low"}) {
		t.Fatalf("config-only entries = %#v", entries)
	}
}

func TestBuildModelCatalogService(t *testing.T) {
	t.Setenv("CATALOG_TEST_KEY", "secret")
	cfg := config.Config{
		Models: config.ModelsConfig{DiscoveryEnabled: true},
		Providers: map[string]config.ProviderConfig{
			"openai": {
				Type:      config.ProviderTypeOpenAI,
				APIKeyEnv: "CATALOG_TEST_KEY",
				Headers:   map[string]string{"X-Test": "value"},
			},
			"unknown": {Type: config.ProviderTypeGemini},
		},
	}
	service, endpoints, _ := buildModelCatalogService(&cfg, &http.Client{})
	if service == nil || len(endpoints) != 1 {
		t.Fatalf("buildModelCatalogService() endpoints = %#v, want one supported endpoint", endpoints)
	}
	if endpoints[0].APIKey != "secret" {
		t.Fatalf("endpoint API key = %q, want env value", endpoints[0].APIKey)
	}
	if endpoints[0].BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("endpoint base URL = %q, want provider default", endpoints[0].BaseURL)
	}
	if got := cfg.Providers["openai"].BaseURL; got != "" {
		t.Fatalf("config provider base URL = %q, want unchanged empty value", got)
	}
	endpoints[0].Headers["X-Test"] = "changed"
	if cfg.Providers["openai"].Headers["X-Test"] != "value" {
		t.Fatal("endpoint headers share config map")
	}

	cfg.Models.DiscoveryEnabled = false
	service, endpoints, popularity := buildModelCatalogService(&cfg, nil)
	if service == nil || service.DiscoveryEnabled || len(endpoints) != 0 || popularity == nil {
		t.Fatalf("discovery-disabled build = service=%v enabled=%v endpoints=%d popularity=%v", service, service.DiscoveryEnabled, len(endpoints), popularity)
	}
}

func TestSelectedModelConfigResolvesDefaultReference(t *testing.T) {
	tests := []struct {
		name         string
		defaultModel string
		definitions  map[string]config.ModelConfig
		providers    map[string]config.ProviderConfig
		wantProvider string
		wantID       string
	}{
		{
			name:         "configured alias",
			defaultModel: "alias",
			definitions:  map[string]config.ModelConfig{"alias": {Provider: "local", ID: "configured-id"}},
			providers:    map[string]config.ProviderConfig{"local": {}},
			wantProvider: "local",
			wantID:       "configured-id",
		},
		{
			name:         "raw reference",
			defaultModel: "openrouter/openai/gpt-4",
			providers:    map[string]config.ProviderConfig{"openrouter": {}},
			wantProvider: "openrouter",
			wantID:       "openai/gpt-4",
		},
		{
			name:         "unknown reference",
			defaultModel: "garbage",
			wantProvider: "",
			wantID:       "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectedModelConfig(config.Config{Models: config.ModelsConfig{
				Effective:   config.EffectiveModelAssignments{DefaultModel: tt.defaultModel, ActiveOrchestratorModel: tt.defaultModel},
				Definitions: tt.definitions,
			}, Providers: tt.providers})
			if got.Provider != tt.wantProvider || got.ID != tt.wantID {
				t.Fatalf("selectedModelConfig() = provider=%q id=%q, want provider=%q id=%q", got.Provider, got.ID, tt.wantProvider, tt.wantID)
			}
		})
	}
}

type catalogTestEnumerator struct {
	wait time.Duration
}

func (e catalogTestEnumerator) Enumerate(ctx context.Context, ep modelcatalog.Endpoint, _ modelcatalog.EnumerationOptions) (modelcatalog.EnumerationResult, error) {
	wait := e.wait
	if ep.Alias == "slow" {
		wait = 40 * time.Millisecond
	}
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return modelcatalog.EnumerationResult{}, ctx.Err()
	}
	return modelcatalog.EnumerationResult{Models: []modelcatalog.DiscoveredModel{{
		ProviderAlias: ep.Alias,
		ID:            ep.Alias + "-discovered",
		DisplayName:   ep.Alias,
	}}}, nil
}

func TestModelCatalogRefreshPublishesAndCloses(t *testing.T) {
	cache := modelcatalog.NewCache(t.TempDir())
	service := modelcatalog.NewService(func(_ string, _ *http.Client) (modelcatalog.Enumerator, error) {
		return catalogTestEnumerator{}, nil
	}, cache, modelcatalog.NewStore(t.TempDir()+"/popularity.json"), &http.Client{}, true)
	cfg := config.Config{
		Models: config.ModelsConfig{Effective: config.EffectiveModelAssignments{
			DefaultModel:            "fast",
			ActiveOrchestratorModel: "fast",
		}, Definitions: map[string]config.ModelConfig{
			"fast": {Provider: "fast", ID: "fast-model"},
		}},
		Providers: map[string]config.ProviderConfig{
			"fast": {Type: config.ProviderTypeOpenAI, BaseURL: "http://fast"},
			"slow": {Type: config.ProviderTypeOpenAI, BaseURL: "http://slow"},
		},
	}
	sess, err := interactive.NewSession(interactive.Dependencies{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	updates := make(chan []tui.ModelEntry, 2)
	startModelCatalogRefresh(context.Background(), cliRuntime{
		cfg:          cfg,
		modelCatalog: service,
		modelCatalogEndpoints: []modelcatalog.Endpoint{
			{Alias: "fast", Type: "openai", BaseURL: "http://fast"},
			{Alias: "slow", Type: "openai", BaseURL: "http://slow"},
		},
	}, sess, updates)
	first := <-updates
	foundFast := false
	for _, entry := range first {
		if entry.Ref == "fast/fast-discovered" {
			foundFast = true
		}
	}
	if !foundFast {
		t.Fatalf("first refresh batch = %#v, want fast provider completion", first)
	}
	batches := 1
	for range updates {
		batches++
	}
	if batches != 2 {
		t.Fatalf("refresh batches = %d, want two", batches)
	}
}

func TestModelCatalogRefreshCancelDoesNotBlock(t *testing.T) {
	cache := modelcatalog.NewCache(t.TempDir())
	service := modelcatalog.NewService(func(_ string, _ *http.Client) (modelcatalog.Enumerator, error) {
		return catalogTestEnumerator{wait: 50 * time.Millisecond}, nil
	}, cache, modelcatalog.NewStore(t.TempDir()+"/popularity.json"), &http.Client{}, true)
	cfg := config.Config{Models: config.ModelsConfig{Effective: config.EffectiveModelAssignments{
		DefaultModel:            "slow",
		ActiveOrchestratorModel: "slow",
	}}}
	sess, err := interactive.NewSession(interactive.Dependencies{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan []tui.ModelEntry)
	done := make(chan struct{})
	go func() {
		startModelCatalogRefresh(ctx, cliRuntime{
			cfg:                   cfg,
			modelCatalog:          service,
			modelCatalogEndpoints: []modelcatalog.Endpoint{{Alias: "slow", Type: "fake", BaseURL: "http://slow"}},
		}, sess, updates)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh setup blocked after cancellation")
	}
	select {
	case _, ok := <-updates:
		if ok {
			_, ok = <-updates
		}
		if ok {
			t.Fatal("refresh channel did not close")
		}
	case <-time.After(time.Second):
		t.Fatal("refresh channel did not close")
	}
}
