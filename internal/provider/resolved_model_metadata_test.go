package provider

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// TestResolveModelMetadataNoWarningOnProviderMismatchWithConfiguredLimits
// reverses #737's TestLoadAndApplyMetadataWarnsOnProviderMismatchWithConfiguredLimits:
// warnings are now derived from final facts, not individual lookups, per the
// model-metadata-fix plan §1.3(2)/§2.5.
func TestResolveModelMetadataNoWarningOnProviderMismatchWithConfiguredLimits(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	cache := &metadata.Cache{Dir: metadata.DefaultCacheDir()}
	if err := os.MkdirAll(cache.Dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(cache.CachePath(), []byte(`{"anthropic":{"models":{"gpt-5.6-luna":{"limit":{"context":200000,"output":100000}}}}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(cache) error = %v", err)
	}
	if err := os.WriteFile(cache.MetaPath(), []byte(`{"downloaded_at":"2026-05-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json"}`), 0o644); err != nil {
		t.Fatalf("WriteFile(meta) error = %v", err)
	}

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"codex": {Type: config.ProviderTypeCodex},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"luna": {
				Provider: "codex",
				ID:       "gpt-5.6-luna",
				Advanced: config.AdvancedConfig{
					Limits: config.AdvancedLimitsConfig{ContextWindow: 100000, MaxOutputTokens: 10000},
				},
			},
		}},
	}

	rm, err := resolveReference(&cfg, "luna", true, nil)
	if err != nil {
		t.Fatalf("resolveReference() error = %v", err)
	}
	if len(rm.Warnings) != 0 {
		t.Fatalf("Warnings = %v, want none (limits fully configured, provider_mismatch on other facts must not warn)", rm.Warnings)
	}
}

func TestResolveModelMetadataWarnsOnMetadataCacheDegradation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"codex": {Type: config.ProviderTypeCodex},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"luna": {Provider: "codex", ID: "gpt-5.6-luna"},
		}},
	}

	rm, err := resolveReference(&cfg, "luna", true, &http.Client{Transport: &alwaysFailTransport{}})
	if err != nil {
		t.Fatalf("resolveReference() error = %v", err)
	}
	if len(rm.Warnings) == 0 {
		t.Fatal("Warnings is empty, want cache degradation warning")
	}
	if !strings.Contains(rm.Warnings[0], "models.dev unavailable") {
		t.Fatalf("Warnings = %q, want cache degradation warning", rm.Warnings)
	}
	if rm.MetadataSource != "fallback" {
		t.Fatalf("MetadataSource = %q, want fallback", rm.MetadataSource)
	}
}
