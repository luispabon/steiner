package modelcatalog

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

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
