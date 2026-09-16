package main

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/modelcatalog"
)

// TestCatalogMetadataAdapter_CodexUsesChatGPTBackendFingerprint verifies the
// adapter routes lookups through catalogConfigCopy so a Codex provider whose
// configured BaseURL differs from codexChatGPTBackendURL still finds cache
// entries the catalog cached under the ChatGPT backend fingerprint.
func TestCatalogMetadataAdapter_CodexUsesChatGPTBackendFingerprint(t *testing.T) {
	cacheDir := t.TempDir()
	cache := modelcatalog.NewCache(cacheDir)
	envelope := modelcatalog.CacheEnvelope{
		Fingerprint: modelcatalog.CacheFingerprint{
			ProviderType: string(config.ProviderTypeCodex),
			BaseURL:      codexChatGPTBackendURL,
		},
		FetchedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
		Models: []modelcatalog.DiscoveredModel{
			{ID: "gpt-5-codex", ContextLength: 272000, MaxOutputTokens: 128000, SupportedEfforts: []string{"low", "high"}},
		},
	}
	if err := cache.SaveAtomic("codex", envelope); err != nil {
		t.Fatalf("SaveAtomic() error = %v", err)
	}

	service := modelcatalog.NewService(nil, cache, modelcatalog.NewStore(cacheDir+"/popularity.json"), nil, true)
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{
			"codex": {Type: config.ProviderTypeCodex, BaseURL: "https://api.openai.com/v1"},
		},
	}

	adapter := newCatalogMetadataAdapter(service, cfg)
	model, ok := adapter.CatalogModel("codex", "gpt-5-codex")
	if !ok {
		t.Fatal("CatalogModel() ok = false, want true")
	}
	if model.ContextWindow != 272000 {
		t.Errorf("ContextWindow = %d, want 272000", model.ContextWindow)
	}
	if model.MaxOutputTokens != 128000 {
		t.Errorf("MaxOutputTokens = %d, want 128000", model.MaxOutputTokens)
	}
}

func TestCatalogMetadataAdapter_DefaultProviderBasesFindPersistedCache(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  config.ProviderType
		base string
	}{
		{"openai", config.ProviderTypeOpenAI, "https://api.openai.com/v1"},
		{"openrouter", config.ProviderTypeOpenRouter, "https://openrouter.ai/api/v1"},
		{"opencode_go", config.ProviderTypeOpencodeGo, "https://opencode.ai/zen/go/v1"},
		{"opencode_zen", config.ProviderTypeOpencodeZen, "https://opencode.ai/zen/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := modelcatalog.NewCache(t.TempDir())
			if err := cache.SaveAtomic(tc.name, modelcatalog.CacheEnvelope{Fingerprint: modelcatalog.CacheFingerprint{ProviderType: string(tc.typ), BaseURL: tc.base}, Models: []modelcatalog.DiscoveredModel{{ID: "model"}}}); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Providers: map[string]config.ProviderConfig{tc.name: {Type: tc.typ}}}
			adapter := newCatalogMetadataAdapter(modelcatalog.NewService(nil, cache, nil, nil), cfg)
			if _, ok := adapter.CatalogModel(tc.name, "model"); !ok {
				t.Fatal("CatalogModel() missed cache with omitted base_url")
			}
		})
	}
}

func TestCatalogMetadataAdapter_NilService(t *testing.T) {
	adapter := newCatalogMetadataAdapter(nil, &config.Config{})
	if _, ok := adapter.CatalogModel("any", "any"); ok {
		t.Fatal("CatalogModel() ok = true, want false for nil service")
	}
}
