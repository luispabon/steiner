package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

type blockingFailTransport struct {
	calls        atomic.Int32
	firstStarted chan struct{}
	release      chan struct{}
}

func (t *blockingFailTransport) RoundTrip(*http.Request) (*http.Response, error) {
	if t.calls.Add(1) == 1 {
		close(t.firstStarted)
		<-t.release
	}
	return nil, fmt.Errorf("offline")
}

type uncanceledObservationContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *uncanceledObservationContext) Err() error {
	err := c.Context.Err()
	if err == nil {
		c.once.Do(func() { close(c.observed) })
	}
	return err
}

type countingCatalog struct{ calls *atomic.Int32 }

func (c countingCatalog) CatalogModel(string, string) (CatalogModel, bool) {
	c.calls.Add(1)
	return CatalogModel{}, false
}

type recoveringCatalog struct {
	calls        atomic.Int32
	firstStarted chan struct{}
	release      chan struct{}
}

func (c *recoveringCatalog) CatalogModel(string, string) (CatalogModel, bool) {
	if c.calls.Add(1) == 1 {
		close(c.firstStarted)
		<-c.release
		return CatalogModel{}, false
	}
	return CatalogModel{ContextWindow: 9000}, true
}

func resolverTestConfig(alias, backendID string) config.Config {
	return config.Config{Providers: map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"}}, Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{alias: {Provider: "local", ID: backendID, Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{ContextWindow: 32768, MaxOutputTokens: 4096}}}}}}
}

func TestResolverCanceledCoalescedWaiterReturnsContextError(t *testing.T) {
	transport := &blockingFailTransport{firstStarted: make(chan struct{}), release: make(chan struct{})}
	resolver := NewResolver(ResolverOptions{HTTPClient: &http.Client{Transport: transport}, ModelsDev: func(context.Context) metadata.LoadResult { return metadata.LoadResult{} }})
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOllama, BaseURL: "http://localhost:11434"}}, Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{"model": {Provider: "local", ID: "model-v1"}}}}
	leaderDone := make(chan struct{})
	go func() { _, _ = resolver.Resolve(context.Background(), cfg, "model"); close(leaderDone) }()
	<-transport.firstStarted
	ctx, cancel := context.WithCancel(context.Background())
	observed := make(chan struct{})
	waiterCtx := &uncanceledObservationContext{Context: ctx, observed: observed}
	waiterDone := make(chan error, 1)
	go func() { _, err := resolver.Resolve(waiterCtx, cfg, "model"); waiterDone <- err }()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("waiter did not observe an uncanceled context")
	}
	cancel()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter did not return while leader was blocked")
	}
	close(transport.release)
	<-leaderDone
}

func TestResolverResolveCoalescesConcurrentCallsForSameKey(t *testing.T) {
	transport := &blockingFailTransport{firstStarted: make(chan struct{}), release: make(chan struct{})}
	resolver := NewResolver(ResolverOptions{HTTPClient: &http.Client{Transport: transport}, ModelsDev: func(context.Context) metadata.LoadResult { return metadata.LoadResult{} }})
	cfg := config.Config{Providers: map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOllama, BaseURL: "http://localhost:11434"}}, Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{"model": {Provider: "local", ID: "model-v1"}}}}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = resolver.Resolve(context.Background(), cfg, "model") }()
	}
	<-transport.firstStarted
	close(transport.release)
	wg.Wait()
	if got := transport.calls.Load(); got != 1 {
		t.Fatalf("probe HTTP calls = %d, want 1", got)
	}
}

func TestResolverMemoizesValidResolutionForUnchangedKey(t *testing.T) {
	var catalogCalls atomic.Int32
	catalog := countingCatalog{calls: &catalogCalls}
	resolver := NewResolver(ResolverOptions{Catalog: catalog})
	cfg := resolverTestConfig("model", "model-v1")
	for i := 0; i < 2; i++ {
		if _, err := resolver.Resolve(context.Background(), cfg, "model"); err != nil {
			t.Fatal(err)
		}
	}
	if catalogCalls.Load() != 1 {
		t.Fatalf("catalog calls = %d, want 1", catalogCalls.Load())
	}
}

func TestResolverDoesNotMemoizeFailures(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	if _, err := resolver.Resolve(context.Background(), config.Config{}, "missing"); err == nil {
		t.Fatal("expected error resolving unknown alias")
	}
	if _, err := resolver.Resolve(context.Background(), resolverTestConfig("missing", "missing-v1"), "missing"); err != nil {
		t.Fatal(err)
	}
}

func TestResolverRetriesSameValidKeyAfterCanceledCall(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	cfg := resolverTestConfig("model", "model-v1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.Resolve(ctx, cfg, "model"); !errors.Is(err, context.Canceled) {
		t.Fatalf("first resolution error = %v, want context.Canceled", err)
	}
	if _, err := resolver.Resolve(context.Background(), cfg, "model"); err != nil {
		t.Fatalf("retry after source recovery error = %v", err)
	}
}

func TestResolverFailedValidKeyIsNotMemoizedAndRetriesAfterRecovery(t *testing.T) {
	catalog := &recoveringCatalog{firstStarted: make(chan struct{}), release: make(chan struct{})}
	resolver := NewResolver(ResolverOptions{Catalog: catalog})
	cfg := resolverTestConfig("model", "model-v1")
	modelCfg := cfg.Models.Definitions["model"]
	modelCfg.Advanced.Limits = config.AdvancedLimitsConfig{}
	cfg.Models.Definitions["model"] = modelCfg
	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { _, err := resolver.Resolve(ctx, cfg, "model"); firstDone <- err }()
	<-catalog.firstStarted
	cancel()
	close(catalog.release)
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first resolution error = %v, want context.Canceled", err)
	}
	resolved, err := resolver.Resolve(context.Background(), cfg, "model")
	if err != nil {
		t.Fatalf("retry error = %v", err)
	}
	if resolved.Facts.ContextWindow.Value != 9000 || resolved.Facts.ContextWindow.Source != FactSourceCatalog {
		t.Fatalf("retry context = %+v, want catalog 9000", resolved.Facts.ContextWindow)
	}
	if got := catalog.calls.Load(); got != 2 {
		t.Fatalf("catalog calls = %d, want 2", got)
	}
}

func TestResolverNestedParamsAreIsolatedAcrossCacheHits(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	cfg := resolverTestConfig("model", "model-v1")
	mc := cfg.Models.Definitions["model"]
	mc.Params = map[string]any{"nested": map[string]any{"items": []any{"before"}}}
	mc.ExtraParams = map[string]any{"nested": map[string]any{"items": []any{"extra"}}}
	cfg.Models.Definitions["model"] = mc
	first, err := resolver.Resolve(context.Background(), cfg, "model")
	if err != nil {
		t.Fatal(err)
	}
	first.Params["nested"].(map[string]any)["items"].([]any)[0] = "changed"
	first.ExtraParams["nested"].(map[string]any)["items"].([]any)[0] = "changed"
	second, err := resolver.Resolve(context.Background(), cfg, "model")
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Params["nested"].(map[string]any)["items"].([]any)[0]; got != "before" {
		t.Fatalf("cached Params changed to %v", got)
	}
	if got := second.ExtraParams["nested"].(map[string]any)["items"].([]any)[0]; got != "extra" {
		t.Fatalf("cached ExtraParams changed to %v", got)
	}
}

func TestResolverKeepsReferencesIndependent(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	cfg := resolverTestConfig("luna", "luna-v1")
	luna, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if luna.BackendModelID != "luna-v1" {
		t.Fatal(luna.BackendModelID)
	}
}
