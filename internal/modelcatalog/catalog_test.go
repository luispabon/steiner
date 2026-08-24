package modelcatalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

func TestServiceChoicesMergeRankAndCurrent(t *testing.T) {
	cache := NewCache(t.TempDir())
	if err := cache.SaveAtomic("local", CacheEnvelope{
		Fingerprint: CacheFingerprint{ProviderType: "openai", BaseURL: "https://local.example"},
		Models: []DiscoveredModel{
			{ID: "suppressed", DisplayName: "Discovered suppressed", SupportedEfforts: []string{"low"}},
			{ID: "popular", DisplayName: "Popular"},
			{ID: "raw", DisplayName: "Raw"},
		},
	}); err != nil {
		t.Fatalf("save cache: %v", err)
	}
	popularity := NewStore(filepath.Join(t.TempDir(), "popularity.json"))
	if err := popularity.Record("local", "popular"); err != nil {
		t.Fatalf("record popular: %v", err)
	}
	if err := popularity.Record("local", "popular"); err != nil {
		t.Fatalf("record popular: %v", err)
	}
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAI, BaseURL: "https://local.example"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"alias": {
				Provider: "local",
				ID:       "suppressed",
				Advanced: config.AdvancedConfig{Reasoning: config.ReasoningConfig{SupportedEfforts: []string{}}},
			},
			"unknown": {Provider: "missing-provider", ID: "unknown-model"},
		}},
	}

	service := NewService(nil, cache, popularity, nil)
	choices := service.Choices(cfg, "local/suppressed")
	if len(choices) != 4 {
		t.Fatalf("choices: got %d, want 4: %+v", len(choices), choices)
	}
	wantRefs := []string{"local/popular", "alias", "unknown", "local/raw"}
	for i, want := range wantRefs {
		if choices[i].Ref != want {
			t.Fatalf("choice %d ref: got %q, want %q", i, choices[i].Ref, want)
		}
	}
	if choices[1].Display != "local/alias" || choices[1].SupportedEfforts == nil || len(choices[1].SupportedEfforts) != 0 {
		t.Fatalf("configured choice precedence: %+v", choices[1])
	}
	if !choices[1].Current {
		t.Fatal("alias should be current through canonical provider/model reference")
	}
	for _, choice := range choices {
		if choice.Ref == "local/suppressed" {
			t.Fatal("aliased definition did not suppress discovered twin")
		}
	}
	if choices[0].SwitchCount != 2 {
		t.Fatalf("popularity: got %d, want 2", choices[0].SwitchCount)
	}
}

func TestServiceChoicesDiscoveryDisabledDoesNotReadCache(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	service := NewService(nil, NewCache(cacheDir), NewStore(filepath.Join(t.TempDir(), "popularity.json")), nil, false)
	cfg := &config.Config{Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
		"configured": {Provider: "unknown", ID: "model"},
	}}}
	choices := service.Choices(cfg, "configured")
	if len(choices) != 1 || choices[0].Ref != "configured" || !choices[0].Current {
		t.Fatalf("disabled choices: %+v", choices)
	}
	if _, err := os.Stat(cacheDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled choices touched cache: %v", err)
	}
}

func TestServiceRefreshForceAndStaleOnly(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model"}]}`))
	}))
	defer server.Close()

	cache := NewCache(t.TempDir())
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	service := NewService(nil, cache, NewStore(filepath.Join(t.TempDir(), "popularity.json")), server.Client())
	endpoint := Endpoint{Alias: "local", Type: "openai_compat", BaseURL: server.URL}

	report := service.RefreshAll(context.Background(), []Endpoint{endpoint}, RefreshOptions{Force: true})
	if report.Results[0].Status != RefreshStatusUpdated || requests.Load() != 1 {
		t.Fatalf("force refresh: report=%+v requests=%d", report, requests.Load())
	}
	report = service.RefreshAll(context.Background(), []Endpoint{endpoint}, RefreshOptions{})
	if report.Results[0].Status != RefreshStatusFreshSkipped || requests.Load() != 1 {
		t.Fatalf("fresh refresh: report=%+v requests=%d", report, requests.Load())
	}
	now = now.Add(CacheTTL)
	report = service.RefreshAll(context.Background(), []Endpoint{endpoint}, RefreshOptions{})
	if report.Results[0].Status != RefreshStatusUpdated || requests.Load() != 2 {
		t.Fatalf("stale refresh: report=%+v requests=%d", report, requests.Load())
	}
}

type testEnumerator struct {
	enumerate func(context.Context, Endpoint, EnumerationOptions) (EnumerationResult, error)
}

func (e testEnumerator) Enumerate(ctx context.Context, endpoint Endpoint, opts EnumerationOptions) (EnumerationResult, error) {
	return e.enumerate(ctx, endpoint, opts)
}

func TestServiceRefreshCodexNotModifiedExtendsFreshness(t *testing.T) {
	var sawETag atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == "etag-1" {
			sawETag.Store(true)
			w.Header().Set("ETag", "etag-1")
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", "etag-1")
		_, _ = w.Write([]byte(`{"models":[{"slug":"codex-model","visibility":"list"}]}`))
	}))
	defer server.Close()

	cache := NewCache(t.TempDir())
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	dispatcher := func(_ string, client *http.Client) (Enumerator, error) {
		return NewCodexEnumerator(client, "test", func(context.Context) (string, string, error) {
			return "token", "account", nil
		}), nil
	}
	service := NewService(dispatcher, cache, NewStore(filepath.Join(t.TempDir(), "popularity.json")), server.Client())
	endpoint := Endpoint{Alias: "codex", Type: "codex", BaseURL: server.URL}
	if got := service.RefreshAll(context.Background(), []Endpoint{endpoint}, RefreshOptions{Force: true}); got.Results[0].Status != RefreshStatusUpdated {
		t.Fatalf("initial refresh: %+v", got)
	}
	now = now.Add(CacheTTL)
	if got := service.RefreshAll(context.Background(), []Endpoint{endpoint}, RefreshOptions{}); got.Results[0].Status != RefreshStatusUpdated {
		t.Fatalf("not modified refresh: %+v", got)
	}
	if !sawETag.Load() || !cache.IsFresh("codex", "codex", server.URL) {
		t.Fatalf("etag was not passed or freshness was not extended")
	}
}

func TestServiceRefreshReportsFailuresAndBoundsConcurrency(t *testing.T) {
	var current atomic.Int32
	var maximum atomic.Int32
	var calls atomic.Int32
	enumerator := testEnumerator{enumerate: func(_ context.Context, endpoint Endpoint, _ EnumerationOptions) (EnumerationResult, error) {
		calls.Add(1)
		active := current.Add(1)
		for {
			old := maximum.Load()
			if active <= old || maximum.CompareAndSwap(old, active) {
				break
			}
		}
		defer current.Add(-1)
		time.Sleep(10 * time.Millisecond)
		if endpoint.Alias == "failed" {
			return EnumerationResult{}, errors.New("provider unavailable")
		}
		return EnumerationResult{Models: []DiscoveredModel{{ID: endpoint.Alias}}}, nil
	}}
	dispatcher := func(string, *http.Client) (Enumerator, error) { return enumerator, nil }
	service := NewService(dispatcher, NewCache(t.TempDir()), NewStore(filepath.Join(t.TempDir(), "popularity.json")), nil)
	endpoints := make([]Endpoint, 10)
	for i := range endpoints {
		endpoints[i] = Endpoint{Alias: "provider-" + string(rune('a'+i)), Type: "test", BaseURL: "https://example.test"}
	}
	endpoints[3].Alias = "failed"
	var callbackMu sync.Mutex
	callbacks := make(map[string]error)
	report := service.RefreshAll(context.Background(), endpoints, RefreshOptions{OnResult: func(alias string, err error) {
		callbackMu.Lock()
		callbacks[alias] = err
		callbackMu.Unlock()
	}})
	if calls.Load() != int32(len(endpoints)) || maximum.Load() > 4 {
		t.Fatalf("refresh bound: calls=%d maximum=%d", calls.Load(), maximum.Load())
	}
	if report.Results[3].Status != RefreshStatusFailed || report.Results[3].Err == nil {
		t.Fatalf("failed provider report: %+v", report.Results[3])
	}
	if len(callbacks) != len(endpoints) {
		t.Fatalf("callbacks: got %d, want %d", len(callbacks), len(endpoints))
	}
}
