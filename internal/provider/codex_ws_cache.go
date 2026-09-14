package provider

import (
	"context"
	"errors"
	"sync"
)

// CodexWSCache holds process-lifetime Codex WebSocket provider instances,
// keyed by caller-defined cache keys (typically "<alias>|<promptCacheKey>"),
// so a session's live connection is reused across turns instead of being
// reconstructed (and reconnected) every turn.
type CodexWSCache struct {
	mu        sync.Mutex
	instances map[string]Provider
}

// NewCodexWSCache returns an empty CodexWSCache ready for use.
func NewCodexWSCache() *CodexWSCache {
	return &CodexWSCache{instances: make(map[string]Provider)}
}

// NewCachingCodexWS returns the cached provider for key, building one via
// build on a miss and storing it. Concurrent misses for the same key are
// resolved by keeping whichever entry lands first and discarding the other.
func NewCachingCodexWS(cache *CodexWSCache, key string, build func() (Provider, error)) (Provider, error) {
	cache.mu.Lock()
	if prov, ok := cache.instances[key]; ok {
		cache.mu.Unlock()
		return prov, nil
	}
	cache.mu.Unlock()

	built, err := build()
	if err != nil {
		return nil, err
	}

	wrapped := &CachingCodexWS{cache: cache, key: key}
	wrapped.inner = built

	cache.mu.Lock()
	if existing, ok := cache.instances[key]; ok {
		cache.mu.Unlock()
		return existing, nil
	}
	cache.instances[key] = wrapped
	cache.mu.Unlock()

	return wrapped, nil
}

// CachingCodexWS wraps a cached Codex WebSocket provider instance, evicting
// itself from the cache when a request fails so the next lookup for this key
// reconstructs from scratch via the caller's build func (reloading/refreshing
// the OAuth token, redialing the socket).
type CachingCodexWS struct {
	inner Provider
	cache *CodexWSCache
	key   string
}

// isContextCanceledErr reports whether err is a wrapped context.Canceled or
// context.DeadlineExceeded, which surface from routine cancellations (e.g. a
// user interrupt) rather than unrecoverable request errors.
func isContextCanceledErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (p *CachingCodexWS) evict() {
	p.cache.mu.Lock()
	if p.cache.instances[p.key] == p {
		delete(p.cache.instances, p.key)
	}
	p.cache.mu.Unlock()
}

// ChatCompletion delegates to the wrapped provider, evicting p from the
// cache on any non-context-cancellation error.
func (p *CachingCodexWS) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	resp, err := p.inner.ChatCompletion(ctx, req)
	if err != nil && !isContextCanceledErr(err) {
		p.evict()
	}
	return resp, err
}

// StreamChatCompletion delegates to the wrapped provider, evicting p from the
// cache on stream setup failure or the first stream chunk carrying a
// non-context-cancellation error.
func (p *CachingCodexWS) StreamChatCompletion(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error) {
	chunks, err := p.inner.StreamChatCompletion(ctx, req)
	if err != nil {
		if !isContextCanceledErr(err) {
			p.evict()
		}
		return chunks, err
	}
	out := make(chan ChatChunk)
	go func() {
		defer close(out)
		evicted := false
		for chunk := range chunks {
			if !evicted && chunk.Error != "" {
				if chunk.OriginalError == nil || !isContextCanceledErr(chunk.OriginalError) {
					p.evict()
					evicted = true
				}
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// SupportsUsageStats delegates to the wrapped provider.
func (p *CachingCodexWS) SupportsUsageStats() bool {
	return p.inner.SupportsUsageStats()
}
