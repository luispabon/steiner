package provider

import (
	"net/http"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestLoadAndApplyMetadataWarnsOnProviderMismatchWithConfiguredLimits(t *testing.T) {
	rm := ResolvedModel{ProviderAlias: "codex", BackendModelID: "gpt-5.6-luna"}
	modelCfg := config.ModelConfig{Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{ContextWindow: 100000, MaxOutputTokens: 10000}}}
	loadAndApplyModelsDevMetadataFromData(&rm, modelCfg, []byte(`{"openai":{"models":{"gpt-5.6-luna":{"limit":{"context":200000,"output":100000}}}}}`))
	if len(rm.Warnings) != 1 || !strings.Contains(rm.Warnings[0], "provider_mismatch") {
		t.Fatalf("Warnings = %v, want provider mismatch warning", rm.Warnings)
	}
}

func TestResolveWithDiscoveryWarnsOnMetadataCacheDegradation(t *testing.T) {
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
	if !strings.Contains(rm.Warnings[0], "models.dev cache degradation") {
		t.Fatalf("Warnings = %q, want cache degradation warning", rm.Warnings)
	}
	if rm.MetadataSource != "fallback" {
		t.Fatalf("MetadataSource = %q, want fallback", rm.MetadataSource)
	}
}
