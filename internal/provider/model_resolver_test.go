package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
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

type invalidateRaceCatalog struct {
	calls         atomic.Int32
	firstStarted  chan struct{}
	releaseFirst  chan struct{}
	secondStarted chan struct{}
	releaseSecond chan struct{}
	thirdStarted  chan struct{}
}

func (c *invalidateRaceCatalog) CatalogModel(string, string) (CatalogModel, bool) {
	switch c.calls.Add(1) {
	case 1:
		close(c.firstStarted)
		<-c.releaseFirst
		return CatalogModel{}, false
	case 2:
		close(c.secondStarted)
		<-c.releaseSecond
	case 3:
		close(c.thirdStarted)
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

func TestResolverInvalidateDoesNotDeleteNewSameKeyEntry(t *testing.T) {
	catalog := &invalidateRaceCatalog{
		firstStarted:  make(chan struct{}),
		releaseFirst:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		releaseSecond: make(chan struct{}),
		thirdStarted:  make(chan struct{}),
	}
	resolver := NewResolver(ResolverOptions{Catalog: catalog})
	cfg := resolverTestConfig("model", "model-v1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstDone := make(chan error, 1)
	go func() {
		_, err := resolver.Resolve(ctx, cfg, "model")
		firstDone <- err
	}()
	<-catalog.firstStarted

	resolver.Invalidate()

	secondDone := make(chan struct {
		model ResolvedModel
		err   error
	}, 1)
	go func() {
		model, err := resolver.Resolve(context.Background(), cfg, "model")
		secondDone <- struct {
			model ResolvedModel
			err   error
		}{model: model, err: err}
	}()
	<-catalog.secondStarted

	cancel()
	close(catalog.releaseFirst)
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first resolution error = %v, want context.Canceled", err)
	}

	thirdWaiting := make(chan struct{})
	thirdCtx := &doneObservationContext{Context: context.Background(), observed: thirdWaiting}
	thirdDone := make(chan struct {
		model ResolvedModel
		err   error
	}, 1)
	go func() {
		model, err := resolver.Resolve(thirdCtx, cfg, "model")
		thirdDone <- struct {
			model ResolvedModel
			err   error
		}{model: model, err: err}
	}()
	thirdLaunched := false
	select {
	case <-thirdWaiting:
	case <-catalog.thirdStarted:
		thirdLaunched = true
	}

	close(catalog.releaseSecond)
	second := <-secondDone
	third := <-thirdDone
	if thirdLaunched {
		t.Fatal("third same-key resolution launched another source call")
	}
	if second.err != nil || third.err != nil {
		t.Fatalf("same-key resolutions returned errors: second=%v third=%v", second.err, third.err)
	}
	if !reflect.DeepEqual(second.model, third.model) {
		t.Fatalf("same-key results differ: second=%+v third=%+v", second.model, third.model)
	}
	if got := catalog.calls.Load(); got != 2 {
		t.Fatalf("catalog calls = %d, want 2 after Invalidate race", got)
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

func TestResolverCacheDifferentiatesExplicitInheritedCodexContext(t *testing.T) {
	resolver := NewResolver(ResolverOptions{Catalog: fakeModelCatalog{"codex\x00gpt-5-codex": {ContextWindow: 128000}}})
	load := func(t *testing.T, contextLine string) config.Config {
		t.Helper()
		dir := t.TempDir()
		project := filepath.Join(dir, "project")
		if err := os.MkdirAll(filepath.Join(project, ".steiner"), 0o755); err != nil {
			t.Fatal(err)
		}
		data := "providers:\n  codex:\n    type: codex\nmodels:\n  definitions:\n    model:\n      provider: codex\n      id: gpt-5-codex\n      advanced:\n        limits:\n" + contextLine
		if err := os.WriteFile(filepath.Join(project, ".steiner", "config.yaml"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(config.LoadOptions{WorkingDir: project, HomeDir: filepath.Join(dir, "home"), Env: map[string]string{}})
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	inherited := load(t, "")
	explicit := load(t, "          context_window: 32768\n")
	inheritedKey, err := resolverCacheKey(inherited, "model")
	if err != nil {
		t.Fatal(err)
	}
	explicitKey, err := resolverCacheKey(explicit, "model")
	if err != nil {
		t.Fatal(err)
	}
	if inheritedKey == explicitKey {
		t.Fatal("resolver keys equal for inherited and explicit context")
	}
	inheritedModel, err := resolver.Resolve(context.Background(), inherited, "model")
	if err != nil {
		t.Fatal(err)
	}
	explicitModel, err := resolver.Resolve(context.Background(), explicit, "model")
	if err != nil {
		t.Fatal(err)
	}
	if inheritedModel.EffectiveLimits.ContextWindow != 128000 || explicitModel.EffectiveLimits.ContextWindow != 32768 {
		t.Fatalf("context windows = %d, %d; want 128000, 32768", inheritedModel.EffectiveLimits.ContextWindow, explicitModel.EffectiveLimits.ContextWindow)
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
