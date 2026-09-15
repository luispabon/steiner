package provider

import (
	"net/http"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

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
