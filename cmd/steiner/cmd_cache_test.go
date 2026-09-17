package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
	"github.com/luispabon/steiner/internal/modelcatalog"
)

func TestCacheRefreshRunsActionsInOrder(t *testing.T) {
	const configuredProviderURL = "http://configured-cache-refresh.test"
	configPath := writeModelsTestConfig(t, configuredProviderURL, true)
	metadataDir := t.TempDir()
	var metadataCalls, providerCalls int
	var observedProviderURL string

	oldMetadataFactory := metadataCacheFactory
	oldServiceFactory := modelCatalogServiceFactory
	t.Cleanup(func() {
		metadataCacheFactory = oldMetadataFactory
		modelCatalogServiceFactory = oldServiceFactory
	})
	metadataCacheFactory = func(*http.Client) *metadata.Cache {
		metadataCalls++
		return &metadata.Cache{Dir: metadataDir, HTTPClient: &http.Client{Transport: refreshTestTransport{}}}
	}
	modelCatalogServiceFactory = func(cfg *config.Config, _ *http.Client) (*modelcatalog.Service, []modelcatalog.Endpoint, *modelcatalog.Store) {
		providerCalls++
		observedProviderURL = cfg.Providers["provider"].BaseURL
		if _, err := os.Stat(metadataDir + "/models.dev.json"); err != nil {
			t.Errorf("metadata cache not written before provider refresh: %v", err)
		}
		service := modelcatalog.NewService(func(string, *http.Client) (modelcatalog.Enumerator, error) {
			return refreshTestEnumerator{}, nil
		}, modelcatalog.NewCache(t.TempDir()), nil, nil, cfg.Models.DiscoveryEnabled)
		return service, []modelcatalog.Endpoint{{Alias: "provider", Type: "openai_compat", BaseURL: "http://unused"}}, nil
	}

	output, err := executeCacheRefresh(t, "--config", configPath, "cache", "refresh")
	if err != nil {
		t.Fatalf("refresh error = %v\noutput:\n%s", err, output)
	}
	if metadataCalls != 1 || providerCalls != 1 {
		t.Fatalf("refresh calls = metadata %d, provider %d, want one each", metadataCalls, providerCalls)
	}
	if observedProviderURL != configuredProviderURL {
		t.Fatalf("provider URL = %q, want config value %q", observedProviderURL, configuredProviderURL)
	}
	wantOutput := "## Model metadata\nmodel metadata cache refreshed\n\n## Provider models\nprovider: ok\n"
	if output != wantOutput {
		t.Fatalf("refresh output = %q, want %q", output, wantOutput)
	}
}

func TestCacheRefreshContinuesAfterMetadataFailure(t *testing.T) {
	configPath := writeModelsTestConfig(t, "http://unused", true)
	metadataPath := t.TempDir() + "/not-a-directory"
	if err := os.WriteFile(metadataPath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	var providerCalls int

	oldMetadataFactory := metadataCacheFactory
	oldServiceFactory := modelCatalogServiceFactory
	t.Cleanup(func() {
		metadataCacheFactory = oldMetadataFactory
		modelCatalogServiceFactory = oldServiceFactory
	})
	metadataCacheFactory = func(*http.Client) *metadata.Cache {
		return &metadata.Cache{Dir: metadataPath, HTTPClient: &http.Client{Transport: refreshTestTransport{}}}
	}
	modelCatalogServiceFactory = func(cfg *config.Config, _ *http.Client) (*modelcatalog.Service, []modelcatalog.Endpoint, *modelcatalog.Store) {
		providerCalls++
		service := modelcatalog.NewService(func(string, *http.Client) (modelcatalog.Enumerator, error) {
			return refreshTestEnumerator{}, nil
		}, modelcatalog.NewCache(t.TempDir()), nil, nil, cfg.Models.DiscoveryEnabled)
		return service, []modelcatalog.Endpoint{{Alias: "provider", Type: "openai_compat", BaseURL: "http://unused"}}, nil
	}

	output, err := executeCacheRefresh(t, "--config", configPath, "cache", "refresh")
	if err == nil || !strings.Contains(err.Error(), "model metadata refresh") {
		t.Fatalf("refresh error = %v, want labeled metadata failure", err)
	}
	wantOutput := "## Model metadata\n\n## Provider models\nprovider: ok\n"
	if providerCalls != 1 || !strings.HasPrefix(output, wantOutput) {
		t.Fatalf("provider refresh calls/output = %d/%q, want one call and output prefix %q", providerCalls, output, wantOutput)
	}
}

func TestCacheRefreshProviderFailureIsLabeled(t *testing.T) {
	configPath := writeModelsTestConfig(t, "http://provider-failure.test", true)
	metadataCache := &metadata.Cache{Dir: t.TempDir(), HTTPClient: &http.Client{Transport: refreshTestTransport{}}}
	oldMetadataFactory := metadataCacheFactory
	oldServiceFactory := modelCatalogServiceFactory
	t.Cleanup(func() {
		metadataCacheFactory = oldMetadataFactory
		modelCatalogServiceFactory = oldServiceFactory
	})
	metadataCacheFactory = func(*http.Client) *metadata.Cache { return metadataCache }
	modelCatalogServiceFactory = func(cfg *config.Config, _ *http.Client) (*modelcatalog.Service, []modelcatalog.Endpoint, *modelcatalog.Store) {
		service := modelcatalog.NewService(func(string, *http.Client) (modelcatalog.Enumerator, error) {
			return refreshFailEnumerator{err: errors.New("provider unavailable")}, nil
		}, modelcatalog.NewCache(t.TempDir()), nil, nil, cfg.Models.DiscoveryEnabled)
		return service, []modelcatalog.Endpoint{{Alias: "provider", Type: "openai_compat", BaseURL: "http://provider-failure.test"}}, nil
	}

	_, err := executeCacheRefresh(t, "--config", configPath, "cache", "refresh")
	if err == nil || !strings.Contains(err.Error(), "provider model refresh") {
		t.Fatalf("refresh error = %v, want provider model label", err)
	}
}

func TestCacheRefreshReportsBothFailures(t *testing.T) {
	configPath := writeModelsTestConfig(t, "http://both-failures.test", true)
	metadataPath := t.TempDir() + "/not-a-directory"
	if err := os.WriteFile(metadataPath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldMetadataFactory := metadataCacheFactory
	oldServiceFactory := modelCatalogServiceFactory
	t.Cleanup(func() {
		metadataCacheFactory = oldMetadataFactory
		modelCatalogServiceFactory = oldServiceFactory
	})
	metadataCacheFactory = func(*http.Client) *metadata.Cache {
		return &metadata.Cache{Dir: metadataPath, HTTPClient: &http.Client{Transport: refreshTestTransport{}}}
	}
	modelCatalogServiceFactory = func(cfg *config.Config, _ *http.Client) (*modelcatalog.Service, []modelcatalog.Endpoint, *modelcatalog.Store) {
		service := modelcatalog.NewService(func(string, *http.Client) (modelcatalog.Enumerator, error) {
			return refreshFailEnumerator{err: errors.New("provider unavailable")}, nil
		}, modelcatalog.NewCache(t.TempDir()), nil, nil, cfg.Models.DiscoveryEnabled)
		return service, []modelcatalog.Endpoint{{Alias: "provider", Type: "openai_compat", BaseURL: "http://both-failures.test"}}, nil
	}

	_, err := executeCacheRefresh(t, "--config", configPath, "cache", "refresh")
	if err == nil {
		t.Fatal("refresh error = nil, want both action failures")
	}
	for _, label := range []string{"model metadata refresh", "provider model refresh"} {
		if !strings.Contains(err.Error(), label) {
			t.Fatalf("refresh error = %v, want %q", err, label)
		}
	}
}

func TestCacheRefreshRejectsArguments(t *testing.T) {
	if _, err := executeCacheRefresh(t, "cache", "refresh", "extra"); err == nil {
		t.Fatal("refresh error = nil, want positional argument error")
	}
}

func executeCacheRefresh(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCommand()
	var output strings.Builder
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return output.String(), err
}

type refreshTestTransport struct{}

func (refreshTestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
}

type refreshTestEnumerator struct{}

func (refreshTestEnumerator) Enumerate(context.Context, modelcatalog.Endpoint, modelcatalog.EnumerationOptions) (modelcatalog.EnumerationResult, error) {
	return modelcatalog.EnumerationResult{}, nil
}

type refreshFailEnumerator struct {
	err error
}

func (e refreshFailEnumerator) Enumerate(context.Context, modelcatalog.Endpoint, modelcatalog.EnumerationOptions) (modelcatalog.EnumerationResult, error) {
	return modelcatalog.EnumerationResult{}, e.err
}
