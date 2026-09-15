package provider

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// blockingFailTransport blocks the first RoundTrip until released, then
// always fails; every call after the first fails immediately. Used to prove
// Resolve's single-flight coalescing operates on the whole resolution (not
// just the shared modelsDevLoader's own sync.Once), since probeSource issues
// a fresh HTTP call on every independent resolveReferenceWithLoader
// invocation with no caching of its own.
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

// resolverTestConfig returns a config with context window/max output tokens
// configured (so neither probeSource nor the conservative fallback fire) but
// reasoning efforts and vision left unconfigured, so modelsDevSource is
// actually consulted under discovery.
func resolverTestConfig(alias, backendID string) config.Config {
	return config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			alias: {
				Provider: "local",
				ID:       backendID,
				Advanced: config.AdvancedConfig{
					Limits: config.AdvancedLimitsConfig{ContextWindow: 32768, MaxOutputTokens: 4096},
				},
			},
		}},
	}
}

func TestResolverResolveCoalescesConcurrentCallsForSameKey(t *testing.T) {
	transport := &blockingFailTransport{
		firstStarted: make(chan struct{}),
		release:      make(chan struct{}),
	}
	resolver := NewResolver(ResolverOptions{
		HTTPClient: &http.Client{Transport: transport},
		ModelsDev:  func(context.Context) metadata.LoadResult { return metadata.LoadResult{} },
	})

	// No context_window/max_output_tokens configured, so probeSource is
	// consulted on every independent resolveReferenceWithLoader call — unlike
	// modelsDevSource, it has no caching of its own, so counting HTTP calls
	// here proves Resolver's own key-based single-flight, not just the shared
	// modelsDevLoader's sync.Once.
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOllama, BaseURL: "http://localhost:11434"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"luna": {Provider: "local", ID: "luna-v1"},
		}},
	}

	const callers = 16
	results := make([]ResolvedModel, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[0], errs[0] = resolver.Resolve(context.Background(), cfg, "luna")
	}()
	<-transport.firstStarted

	wg.Add(callers - 1)
	for i := 1; i < callers; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i], errs[i] = resolver.Resolve(context.Background(), cfg, "luna")
		}()
	}
	close(transport.release)
	wg.Wait()

	if got := transport.calls.Load(); got != 1 {
		t.Fatalf("probe HTTP calls = %d, want 1 (single-flight)", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("resolver.Resolve call %d error = %v", i, err)
		}
		if results[i].BackendModelID != "luna-v1" {
			t.Errorf("resolver.Resolve call %d BackendModelID = %q, want luna-v1", i, results[i].BackendModelID)
		}
	}
}

func TestResolverDoesNotMemoizeFailures(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	cfg := config.Config{}

	if _, err := resolver.Resolve(context.Background(), cfg, "missing"); err == nil {
		t.Fatal("expected error resolving unknown alias")
	}

	cfg = resolverTestConfig("missing", "missing-v1")
	rm, err := resolver.Resolve(context.Background(), cfg, "missing")
	if err != nil {
		t.Fatalf("second resolution error = %v, want success now that config defines the alias", err)
	}
	if rm.BackendModelID != "missing-v1" {
		t.Fatalf("BackendModelID = %q, want missing-v1", rm.BackendModelID)
	}
}

func TestResolverKeepsReferencesIndependent(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"luna": {Provider: "local", ID: "luna-v1", Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{ContextWindow: 32768, MaxOutputTokens: 4096},
			}},
			"nova": {Provider: "local", ID: "nova-v1", Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{ContextWindow: 32768, MaxOutputTokens: 4096},
			}},
		}},
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
}

func TestResolverReResolvesWhenConfigChangesMidSession(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	cfg := resolverTestConfig("luna", "luna-v1")

	first, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if first.EffectiveLimits.ContextWindow != 32768 {
		t.Fatalf("first ContextWindow = %d, want 32768", first.EffectiveLimits.ContextWindow)
	}

	modelCfg := cfg.Models.Definitions["luna"]
	modelCfg.Advanced.Limits.ContextWindow = 65536
	cfg.Models.Definitions["luna"] = modelCfg

	second, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if second.EffectiveLimits.ContextWindow != 65536 {
		t.Fatalf("second ContextWindow = %d, want 65536 (fresh resolution after config change)", second.EffectiveLimits.ContextWindow)
	}
}

func TestResolverInvalidateClearsMemoizedEntries(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	cfg := resolverTestConfig("luna", "luna-v1")

	first, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if first.EffectiveLimits.ContextWindow != 32768 {
		t.Fatalf("first ContextWindow = %d, want 32768", first.EffectiveLimits.ContextWindow)
	}

	resolver.Invalidate()

	// Same fingerprint as before Invalidate — proves the stale entry, not just
	// a config-change fingerprint miss, was cleared.
	second, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if second.EffectiveLimits.ContextWindow != 32768 {
		t.Fatalf("second ContextWindow = %d, want 32768", second.EffectiveLimits.ContextWindow)
	}
}

func TestResolverResolveReturnsIsolatedCopies(t *testing.T) {
	resolver := NewResolver(ResolverOptions{})
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"luna": {
				Provider: "local",
				ID:       "luna-v1",
				Params:   map[string]any{"temperature": 0.5},
				Advanced: config.AdvancedConfig{
					Limits:    config.AdvancedLimitsConfig{ContextWindow: 32768, MaxOutputTokens: 4096},
					Reasoning: config.ReasoningConfig{SupportedEfforts: []string{"low", "high"}},
				},
			},
		}},
	}

	first, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	if first.Params["temperature"] != 0.5 || len(first.Reasoning.SupportedEfforts) != 2 {
		t.Fatalf("first resolution = %#v, want configured Params/SupportedEfforts populated", first)
	}
	first.Warnings = append(first.Warnings, "mutated warning")
	first.Params["mutated"] = true
	first.Reasoning.SupportedEfforts[0] = "mutated-effort"

	second, err := resolver.Resolve(context.Background(), cfg, "luna")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range second.Warnings {
		if w == "mutated warning" {
			t.Fatal("second resolution observed the first caller's mutated Warnings")
		}
	}
	if _, ok := second.Params["mutated"]; ok {
		t.Fatal("second resolution observed the first caller's mutated Params")
	}
	for _, e := range second.Reasoning.SupportedEfforts {
		if e == "mutated-effort" {
			t.Fatal("second resolution observed the first caller's mutated Reasoning.SupportedEfforts")
		}
	}
}

func TestResolverLoadsModelsDevAtMostOnceAcrossAliases(t *testing.T) {
	var loadCalls atomic.Int32
	resolver := NewResolver(ResolverOptions{
		ModelsDev: func(context.Context) metadata.LoadResult {
			loadCalls.Add(1)
			return metadata.LoadResult{}
		},
	})

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"local": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{Definitions: map[string]config.ModelConfig{
			"one": {Provider: "local", ID: "model-one", Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{ContextWindow: 32768, MaxOutputTokens: 4096},
			}},
			"two": {Provider: "local", ID: "model-two", Advanced: config.AdvancedConfig{
				Limits: config.AdvancedLimitsConfig{ContextWindow: 32768, MaxOutputTokens: 4096},
			}},
		}},
	}

	for _, alias := range []string{"one", "two", "one", "two"} {
		if _, err := resolver.Resolve(context.Background(), cfg, alias); err != nil {
			t.Fatalf("resolver.Resolve(%q) error = %v", alias, err)
		}
	}

	if got := loadCalls.Load(); got != 1 {
		t.Fatalf("models.dev load calls = %d, want exactly 1 (loaded once, shared across aliases)", got)
	}
}
