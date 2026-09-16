package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"sync"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/metadata"
)

// ResolverOptions configures a session-scoped Resolver.
type ResolverOptions struct {
	// HTTPClient is used for live provider probes and models.dev refreshes.
	HTTPClient *http.Client
	// ModelsDev overrides how models.dev cache data is loaded, for tests.
	// nil uses the default metadata.Cache-backed loader.
	ModelsDev func(context.Context) metadata.LoadResult
	// Catalog looks up enumerated provider catalog data. nil leaves
	// catalog-sourced facts unknown.
	Catalog ModelCatalog
}

// Resolver resolves model references with shared, lazily loaded metadata
// sources, memoizing successful resolutions and coalescing concurrent
// resolutions for the same effective config fingerprint. The models.dev
// index it holds is loaded at most once for the Resolver's whole lifetime.
type Resolver struct {
	httpClient *http.Client
	mdLoader   *modelsDevLoader
	catalog    ModelCatalog

	mu      sync.Mutex
	entries map[string]*resolverEntry
}

type resolverEntry struct {
	done  chan struct{}
	model ResolvedModel
	err   error
}

// NewResolver returns a Resolver with a fresh, unloaded models.dev cache.
func NewResolver(opts ResolverOptions) *Resolver {
	var loader *modelsDevLoader
	if opts.ModelsDev != nil {
		loader = newModelsDevLoaderWithFunc(opts.ModelsDev)
	} else {
		loader = newModelsDevLoader(&metadata.Cache{Dir: metadata.DefaultCacheDir(), HTTPClient: opts.HTTPClient})
	}
	return &Resolver{
		httpClient: opts.HTTPClient,
		mdLoader:   loader,
		catalog:    opts.Catalog,
		entries:    make(map[string]*resolverEntry),
	}
}

// Resolve resolves reference against cfg, memoizing by reference plus a
// fingerprint of the resolved model/provider config so mid-session config
// changes are picked up correctly. Concurrent calls for the same key are
// coalesced (single-flight). Failed resolutions are never memoized.
func (r *Resolver) Resolve(ctx context.Context, cfg config.Config, reference string) (ResolvedModel, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedModel{}, err
	}
	key, keyErr := resolverCacheKey(cfg, reference)
	if keyErr != nil {
		// Reference doesn't resolve to a model/provider at all (e.g. unknown
		// alias) — fall through directly so the caller gets the same error
		// resolveReference would produce; nothing to memoize.
		return resolveReferenceWithLoader(ctx, &cfg, reference, true, r.httpClient, r.mdLoader, r.catalog)
	}

	r.mu.Lock()
	if entry, ok := r.entries[key]; ok {
		r.mu.Unlock()
		select {
		case <-entry.done:
			return cloneResolvedModel(entry.model), entry.err
		case <-ctx.Done():
			return ResolvedModel{}, ctx.Err()
		}
	}
	entry := &resolverEntry{done: make(chan struct{})}
	r.entries[key] = entry
	r.mu.Unlock()

	model, err := resolveReferenceWithLoader(ctx, &cfg, reference, true, r.httpClient, r.mdLoader, r.catalog)
	if err == nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
	}

	r.mu.Lock()
	if err == nil {
		// entry.model is never handed to a caller directly — every read path
		// (this return and the cache-hit branch above) goes through
		// cloneResolvedModel first, so storing the raw value here is safe.
		entry.model = model
	} else {
		// Failed resolutions must not prevent a later retry.
		delete(r.entries, key)
	}
	entry.err = err
	close(entry.done)
	r.mu.Unlock()

	return cloneResolvedModel(model), err
}

// Invalidate drops all memoized resolutions (e.g. after a config or catalog
// refresh). The models.dev index stays loaded — only per-model results reset.
func (r *Resolver) Invalidate() {
	r.mu.Lock()
	r.entries = make(map[string]*resolverEntry)
	r.mu.Unlock()
}

// resolverCacheKey builds a stable key combining reference with a fingerprint
// of its resolved ModelConfig/ProviderConfig, so a mid-session config edit
// for the same reference produces a different key. It returns an error when
// reference doesn't resolve to a known model/provider, mirroring
// resolveReference's own error paths.
func resolverCacheKey(cfg config.Config, reference string) (string, error) {
	modelCfg, isAlias := config.ResolveModelConfig(&cfg, reference)
	if !isAlias && (modelCfg.Provider == "" || modelCfg.ID == "") {
		return "", fmt.Errorf("model alias %q not found", reference)
	}
	provCfg, ok := cfg.Providers[modelCfg.Provider]
	if !ok {
		return "", fmt.Errorf("provider %q not found for model %q", modelCfg.Provider, reference)
	}

	h := fnv.New64a()
	enc := json.NewEncoder(h)
	if err := enc.Encode(struct {
		Model    config.ModelConfig
		Provider config.ProviderConfig
	}{modelCfg, provCfg}); err != nil {
		return "", fmt.Errorf("fingerprint model config: %w", err)
	}
	return fmt.Sprintf("%s\x00%x", reference, h.Sum64()), nil
}

// cloneResolvedModel returns a defensive deep-enough copy of rm so a caller
// mutating the returned value's slices/maps cannot corrupt a memoized entry
// shared with other callers.
func cloneResolvedModel(rm ResolvedModel) ResolvedModel {
	cloned := rm
	cloned.Warnings = append([]string(nil), rm.Warnings...)
	cloned.Params = cloneAnyMap(rm.Params)
	cloned.ExtraParams = cloneAnyMap(rm.ExtraParams)
	cloned.ProviderConfig.Headers = cloneStringMap(rm.ProviderConfig.Headers)
	cloned.Reasoning.SupportedEfforts = copyStrings(rm.Reasoning.SupportedEfforts)
	cloned.Facts.ReasoningEfforts.Value = copyStrings(rm.Facts.ReasoningEfforts.Value)
	return cloned
}

func cloneAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cloned := make(map[string]any, len(m))
	for k, v := range m {
		cloned[k] = cloneAny(v)
	}
	return cloned
}

func cloneAny(v any) any {
	switch value := v.(type) {
	case map[string]any:
		return cloneAnyMap(value)
	case []any:
		cloned := make([]any, len(value))
		for i, item := range value {
			cloned[i] = cloneAny(item)
		}
		return cloned
	case []string:
		return append([]string(nil), value...)
	case map[string]string:
		return cloneStringMap(value)
	default:
		return v
	}
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cloned := make(map[string]string, len(m))
	for k, v := range m {
		cloned[k] = v
	}
	return cloned
}
