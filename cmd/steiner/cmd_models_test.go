package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/modelcatalog"
)

func TestModelsRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	}))
	defer server.Close()

	configPath := writeModelsTestConfig(t, server.URL, true)
	output, err := executeModelsCommand(t, "--config", configPath, "models", "refresh")
	if err != nil {
		t.Fatalf("refresh error = %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "provider: ok") {
		t.Fatalf("refresh output = %q, want provider ok line", output)
	}
}

func TestModelsRefreshAllFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	configPath := writeModelsTestConfig(t, server.URL, true)
	output, err := executeModelsCommand(t, "--config", configPath, "models", "refresh")
	if err == nil {
		t.Fatalf("refresh error = nil, output = %q, want nonzero result when all providers fail", output)
	}
	if !strings.Contains(output, "provider: failed:") {
		t.Fatalf("refresh output = %q, want provider failure line", output)
	}
}

func TestModelsRefreshDisabled(t *testing.T) {
	configPath := writeModelsTestConfig(t, "http://unused", false)
	output, err := executeModelsCommand(t, "--config", configPath, "models", "refresh")
	if err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	if output != "model discovery disabled\n" {
		t.Fatalf("refresh output = %q, want disabled notice", output)
	}
}

func TestModelsStatus(t *testing.T) {
	cacheDir := t.TempDir()
	cache := modelcatalog.NewCache(cacheDir)
	if err := cache.SaveAtomic("fresh", modelcatalog.CacheEnvelope{Fingerprint: modelcatalog.CacheFingerprint{ProviderType: "openai_compat", BaseURL: "http://fresh"}}); err != nil {
		t.Fatal(err)
	}
	if err := cache.SaveAtomic("stale", modelcatalog.CacheEnvelope{Fingerprint: modelcatalog.CacheFingerprint{ProviderType: "openai_compat", BaseURL: "http://stale"}}); err != nil {
		t.Fatal(err)
	}
	makeCacheStale(t, cacheDir, "http://stale")

	oldCacheFactory := modelCatalogCacheFactory
	oldServiceFactory := modelCatalogServiceFactory
	t.Cleanup(func() {
		modelCatalogCacheFactory = oldCacheFactory
		modelCatalogServiceFactory = oldServiceFactory
	})
	modelCatalogCacheFactory = func(string) *modelcatalog.Cache { return cache }
	modelCatalogServiceFactory = func(cfg *config.Config, _ *http.Client) (*modelcatalog.Service, []modelcatalog.Endpoint, *modelcatalog.Store) {
		service := modelcatalog.NewService(nil, cache, nil, nil, cfg.Models.DiscoveryEnabled)
		return service, []modelcatalog.Endpoint{
			{Alias: "fresh", Type: "openai_compat", BaseURL: "http://fresh"},
			{Alias: "missing", Type: "openai_compat", BaseURL: "http://missing"},
			{Alias: "stale", Type: "openai_compat", BaseURL: "http://stale"},
		}, nil
	}

	configPath := writeModelsTestConfig(t, "http://unused", true)
	output, err := executeModelsCommand(t, "--config", configPath, "models", "status")
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	for _, want := range []string{"fresh: fresh", "missing: missing", "stale: stale"} {
		if !strings.Contains(output, want) {
			t.Errorf("status output = %q, missing %q", output, want)
		}
	}
}

func TestModelsStatusDisabled(t *testing.T) {
	configPath := writeModelsTestConfig(t, "http://unused", false)
	output, err := executeModelsCommand(t, "--config", configPath, "models", "status")
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if output != "model discovery: disabled\n" {
		t.Fatalf("status output = %q, want disabled status", output)
	}
}

func executeModelsCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string(args))
	err := cmd.Execute()
	return output.String(), err
}

func writeModelsTestConfig(t *testing.T, baseURL string, discoveryEnabled bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "models:\n  discovery_enabled: " + map[bool]string{true: "true", false: "false"}[discoveryEnabled] + "\nproviders:\n  local:\n    type: openai_compat\n    base_url: " + baseURL + "\n  provider:\n    type: openai_compat\n    base_url: " + baseURL + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return path
}

func makeCacheStale(t *testing.T, dir, baseURL string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var envelope modelcatalog.CacheEnvelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Fingerprint.BaseURL != baseURL {
			continue
		}
		envelope.ExpiresAt = time.Now().Add(-time.Hour)
		data, err = json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("cache envelope for %s not found", baseURL)
}
