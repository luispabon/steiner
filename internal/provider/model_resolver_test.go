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

type doneObservationContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *doneObservationContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

type countingCatalog struct {
	calls         *atomic.Int32
	contextWindow int
}

func (c countingCatalog) CatalogModel(string, string) (CatalogModel, bool) {
	c.calls.Add(1)
	if c.contextWindow == 0 {
		return CatalogModel{}, false
	}
	return CatalogModel{ContextWindow: c.contextWindow}, true
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
	defer func() {
		close(transport.release)
		<-leaderDone
	}()
	select {
	case <-transport.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("leader did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := make(chan struct{})
	waiterCtx := &doneObservationContext{Context: ctx, observed: observed}
	waiterDone := make(chan error, 1)
	go func() { _, err := resolver.Resolve(waiterCtx, cfg, "model"); waiterDone <- err }()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("waiter did not observe Done")
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
	resolver := NewResolver(ResolverOptions{
		ModelsDev: func(context.Context) metadata.LoadResult {
			return metadata.LoadResult{Data: []byte("not json")}
		},
	})
	cfg := resolverTestConfig("model", "model-v1")
	mc := cfg.Models.Definitions["model"]
	mc.Advanced.Limits = config.AdvancedLimitsConfig{}
	mc.Advanced.Reasoning.SupportedEfforts = []string{"low", "high"}
	mc.Params = map[string]any{"nested": map[string]any{"items": []any{"before"}}}
	mc.ExtraParams = map[string]any{"nested": map[string]any{"items": []any{"extra"}}}
	cfg.Models.Definitions["model"] = mc
	first, err := resolver.Resolve(context.Background(), cfg, "model")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Warnings) == 0 || len(first.Reasoning.SupportedEfforts) != 2 {
		t.Fatalf("first resolution = %+v, want warnings and supported efforts", first)
	}
	wantWarning := first.Warnings[0]
	first.Warnings[0] = "changed warning"
	first.Params["nested"].(map[string]any)["items"].([]any)[0] = "changed"
	first.ExtraParams["nested"].(map[string]any)["items"].([]any)[0] = "changed"
	first.Reasoning.SupportedEfforts[0] = "changed effort"
	second, err := resolver.Resolve(context.Background(), cfg, "model")
	if err != nil {
		t.Fatal(err)
	}
	if second.Warnings[0] != wantWarning {
		t.Fatalf("cached Warnings changed to %q", second.Warnings[0])
	}
	if got := second.Params["nested"].(map[string]any)["items"].([]any)[0]; got != "before" {
		t.Fatalf("cached Params changed to %v", got)
	}
	if got := second.ExtraParams["nested"].(map[string]any)["items"].([]any)[0]; got != "extra" {
		t.Fatalf("cached ExtraParams changed to %v", got)
	}
	if got := second.Reasoning.SupportedEfforts[0]; got != "low" {
		t.Fatalf("cached Reasoning.SupportedEfforts changed to %q", got)
	}
}

func TestResolverKeepsReferencesIndependent(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	cfg := resolverTestConfig("luna", "luna-v1")
	cfg.Models.Definitions["nova"] = config.ModelConfig{
		Provider: "local",
		ID:       "nova-v1",
		Advanced: config.AdvancedConfig{Limits: config.AdvancedLimitsConfig{ContextWindow: 32768, MaxOutputTokens: 4096}},
	}

	lunaKey, err := resolverCacheKey(cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	novaKey, err := resolverCacheKey(cfg, "nova")
	if err != nil {
		t.Fatal(err)
	}
	if lunaKey == novaKey {
		t.Fatalf("cache keys are equal: %q", lunaKey)
	}

	luna, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	nova, err := resolver.Resolve(context.Background(), cfg, "nova")
	if err != nil {
		t.Fatal(err)
	}
	if luna.BackendModelID != "luna-v1" || nova.BackendModelID != "nova-v1" {
		t.Fatalf("resolved backends = %q, %q, want luna-v1, nova-v1", luna.BackendModelID, nova.BackendModelID)
	}
	if luna.BackendModelID == nova.BackendModelID || luna.Alias == nova.Alias {
		t.Fatalf("resolved results are not distinct: luna=%+v nova=%+v", luna, nova)
	}
}

func TestResolverReResolvesWhenConfigChangesMidSession(t *testing.T) {
	var catalogCalls atomic.Int32
	resolver := NewResolver(ResolverOptions{
		Catalog: countingCatalog{calls: &catalogCalls, contextWindow: 9000},
		ModelsDev: func(context.Context) metadata.LoadResult {
			return metadata.LoadResult{}
		},
	})
	cfg := resolverTestConfig("luna", "luna-v1")
	modelCfg := cfg.Models.Definitions["luna"]
	modelCfg.Advanced.Limits = config.AdvancedLimitsConfig{}
	cfg.Models.Definitions["luna"] = modelCfg

	first, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if first.EffectiveLimits.ContextWindow != 9000 {
		t.Fatalf("first ContextWindow = %d, want 9000", first.EffectiveLimits.ContextWindow)
	}

	modelCfg.Params = map[string]any{"revision": 2}
	cfg.Models.Definitions["luna"] = modelCfg
	second, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if second.EffectiveLimits.ContextWindow != 9000 {
		t.Fatalf("second ContextWindow = %d, want 9000", second.EffectiveLimits.ContextWindow)
	}
	if got := catalogCalls.Load(); got != 2 {
		t.Fatalf("catalog calls = %d, want 2 after config fingerprint change", got)
	}
}

func TestResolverInvalidateClearsMemoizedEntries(t *testing.T) {
	var catalogCalls atomic.Int32
	resolver := NewResolver(ResolverOptions{
		Catalog: countingCatalog{calls: &catalogCalls, contextWindow: 9000},
		ModelsDev: func(context.Context) metadata.LoadResult {
			return metadata.LoadResult{}
		},
	})
	cfg := resolverTestConfig("luna", "luna-v1")
	modelCfg := cfg.Models.Definitions["luna"]
	modelCfg.Advanced.Limits = config.AdvancedLimitsConfig{}
	cfg.Models.Definitions["luna"] = modelCfg

	if _, err := resolver.Resolve(context.Background(), cfg, "luna"); err != nil {
		t.Fatal(err)
	}
	resolver.Invalidate()
	if _, err := resolver.Resolve(context.Background(), cfg, "luna"); err != nil {
		t.Fatal(err)
	}
	if got := catalogCalls.Load(); got != 2 {
		t.Fatalf("catalog calls = %d, want 2 after Invalidate", got)
	}
}

func TestResolverLoadsModelsDevOnceAcrossAliasesWithDistinctResults(t *testing.T) {
	var loadCalls atomic.Int32
	resolver := NewResolver(ResolverOptions{
		ModelsDev: func(context.Context) metadata.LoadResult {
			loadCalls.Add(1)
			return metadata.LoadResult{Data: []byte(`{"openai":{"models":{"model-one":{"limit":{"context":10000,"output":2000}},"model-two":{"limit":{"context":20000,"output":3000}}}}}`)}
		},
	})
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"}},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"one": {Provider: "local", ID: "model-one"},
			"two": {Provider: "local", ID: "model-two"},
		}},
	}

	one, err := resolver.Resolve(context.Background(), cfg, "one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := resolver.Resolve(context.Background(), cfg, "two")
	if err != nil {
		t.Fatal(err)
	}
	if got := loadCalls.Load(); got != 1 {
		t.Fatalf("models.dev load calls = %d, want 1 across aliases", got)
	}
	if one.BackendModelID != "model-one" || two.BackendModelID != "model-two" {
		t.Fatalf("backend IDs = %q, %q, want model-one, model-two", one.BackendModelID, two.BackendModelID)
	}
	if one.EffectiveLimits.ContextWindow != 10000 || two.EffectiveLimits.ContextWindow != 20000 {
		t.Fatalf("context windows = %d, %d, want 10000, 20000", one.EffectiveLimits.ContextWindow, two.EffectiveLimits.ContextWindow)
	}
	if one.EffectiveLimits.ContextWindow == two.EffectiveLimits.ContextWindow {
		t.Fatal("resolved model results are not distinct")
	}
}
