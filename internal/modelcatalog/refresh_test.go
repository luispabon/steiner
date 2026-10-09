package modelcatalog

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

func TestServiceRefreshUsesClaudeSubscriptionDiscoveryTimeout(t *testing.T) {
	service := NewService(func(_ string, _ *http.Client) (Enumerator, error) {
		return testEnumerator{enumerate: func(ctx context.Context, _ Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
			select {
			case <-time.After(6 * time.Second):
				return EnumerationResult{Models: []DiscoveredModel{{ID: "model"}}}, nil
			case <-ctx.Done():
				return EnumerationResult{}, ctx.Err()
			}
		}}, nil
	}, NewCache(t.TempDir()), NewStore(""), nil)
	got := service.RefreshAll(context.Background(), []Endpoint{{Alias: "claude", Type: string(config.ProviderTypeClaudeSubscription)}}, RefreshOptions{Force: true})
	if got.Results[0].Err != nil {
		t.Fatalf("Claude subscription refresh error = %v, want nil", got.Results[0].Err)
	}
}

func TestCatalogEnumerationTimeoutForNonClaudeProvider(t *testing.T) {
	if got := catalogEnumerationTimeoutFor(string(config.ProviderTypeOpenAI)); got != 5*time.Second {
		t.Fatalf("OpenAI catalog timeout = %s, want 5s", got)
	}
	if got := catalogEnumerationTimeoutFor(string(config.ProviderTypeClaudeSubscription)); got != 20*time.Second {
		t.Fatalf("Claude subscription catalog timeout = %s, want 20s", got)
	}
}

func TestServiceRefreshPrepareFailureUsesFreshCache(t *testing.T) {
	cache := NewCache(t.TempDir())
	endpoint := Endpoint{Alias: "codex", Type: "codex", BaseURL: "current"}
	if err := cache.SaveAtomic(endpoint.Alias, CacheEnvelope{Fingerprint: CacheFingerprint{ProviderType: endpoint.Type, BaseURL: endpoint.BaseURL}, Models: []DiscoveredModel{{ID: "cached"}}}); err != nil {
		t.Fatalf("save cache: %v", err)
	}
	prepared := false
	endpoint.Prepare = func(context.Context) (Endpoint, error) {
		prepared = true
		return endpoint, errors.New("refresh failed")
	}
	service := NewService(func(_ string, _ *http.Client) (Enumerator, error) { t.Fatal("enumerator called"); return nil, nil }, cache, NewStore(""), nil)
	got := service.RefreshAll(context.Background(), []Endpoint{endpoint}, RefreshOptions{})
	if got.Results[0].Status != RefreshStatusFreshSkipped || got.Results[0].Err != nil || prepared {
		t.Fatalf("refresh: report=%+v prepared=%v", got, prepared)
	}
}

func TestServiceRefreshFallsThroughOnCacheStatusError(t *testing.T) {
	cachePath := t.TempDir()
	cache := NewCache(cachePath)
	if err := cache.SaveAtomic("local", CacheEnvelope{Fingerprint: CacheFingerprint{ProviderType: "openai_compat", BaseURL: "current"}, Models: []DiscoveredModel{{ID: "cached"}}}); err != nil {
		t.Fatalf("save cache: %v", err)
	}
	if err := os.RemoveAll(cachePath); err != nil {
		t.Fatalf("remove cache dir: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("replace cache dir: %v", err)
	}
	called := false
	service := NewService(func(_ string, _ *http.Client) (Enumerator, error) {
		return testEnumerator{enumerate: func(context.Context, Endpoint, EnumerationOptions) (EnumerationResult, error) {
			called = true
			return EnumerationResult{Models: []DiscoveredModel{{ID: "fresh"}}}, nil
		}}, nil
	}, cache, NewStore(""), nil)
	got := service.RefreshAll(context.Background(), []Endpoint{{Alias: "local", Type: "openai_compat", BaseURL: "current"}}, RefreshOptions{})
	if !called || got.Results[0].Status == RefreshStatusFreshSkipped {
		t.Fatalf("refresh: called=%v report=%+v", called, got)
	}
}

func TestServiceRefreshPreparesEndpointBeforeEnumeration(t *testing.T) {
	var prepared atomic.Bool
	var sawBase string
	service := NewService(func(_ string, _ *http.Client) (Enumerator, error) {
		return testEnumerator{enumerate: func(_ context.Context, endpoint Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
			sawBase = endpoint.BaseURL
			return EnumerationResult{Models: []DiscoveredModel{{ID: "model"}}}, nil
		}}, nil
	}, NewCache(t.TempDir()), NewStore(""), nil)
	endpoint := Endpoint{Alias: "codex", Type: "codex", BaseURL: "stale", Prepare: func(context.Context) (Endpoint, error) {
		prepared.Store(true)
		return Endpoint{Alias: "codex", Type: "codex", BaseURL: "current"}, nil
	}}
	got := service.RefreshAll(context.Background(), []Endpoint{endpoint}, RefreshOptions{Force: true})
	if got.Results[0].Err != nil || !prepared.Load() || sawBase != "current" {
		t.Fatalf("refresh: report=%+v prepared=%v base=%q", got, prepared.Load(), sawBase)
	}
}
