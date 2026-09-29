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

	wrapped := &CachingCodexWS{cache: cache, inner: built, key: key, idle: make(chan struct{})}

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

	mu      sync.Mutex
	idle    chan struct{}
	active  bool
	retired bool
}

var errCachingCodexWSEvicted = errors.New("codex websocket cache entry evicted")

// isContextCanceledErr reports whether err is a wrapped context.Canceled or
// context.DeadlineExceeded, which surface from routine cancellations (e.g. a
// user interrupt) rather than unrecoverable request errors.
func isContextCanceledErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (p *CachingCodexWS) begin(ctx context.Context) error {
	for {
		p.mu.Lock()
		if err := ctx.Err(); err != nil {
			p.mu.Unlock()
			return err
		}
		if p.retired {
			p.mu.Unlock()
			return errCachingCodexWSEvicted
		}
		if !p.active {
			p.active = true
			p.mu.Unlock()
			return nil
		}
		idle := p.idle
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle:
		}
	}
}

func (p *CachingCodexWS) end() {
	p.mu.Lock()
	p.active = false
	close(p.idle)
	p.idle = make(chan struct{})
	p.mu.Unlock()
}

func (p *CachingCodexWS) evict() {
	p.mu.Lock()
	if p.retired {
		p.mu.Unlock()
		return
	}
	p.retired = true

	p.cache.mu.Lock()
	if p.cache.instances[p.key] == p {
		delete(p.cache.instances, p.key)
	}
	p.cache.mu.Unlock()
	p.mu.Unlock()

	if inner, ok := p.inner.(*codexWSProvider); ok {
		inner.mu.Lock()
		inner.closeConn()
		inner.mu.Unlock()
	}
}

// ChatCompletion delegates to the wrapped provider, evicting p from the
// cache on any non-context-cancellation error. Calls queued behind an active
// request return promptly when their context is canceled.
func (p *CachingCodexWS) ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if err := p.begin(ctx); err != nil {
		return ChatResponse{}, err
	}
	defer p.end()

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
	if err := p.begin(ctx); err != nil {
		return nil, err
	}

	chunks, err := p.inner.StreamChatCompletion(ctx, req)
	if err != nil {
		if !isContextCanceledErr(err) {
			p.evict()
		}
		p.end()
		return chunks, err
	}
	out := make(chan ChatChunk)
	go func() {
		ended := false
		end := func() {
			if !ended {
				p.end()
				ended = true
			}
		}
		defer end()
		defer close(out)

		evicted := false
		for chunk := range chunks {
			if !evicted && chunk.Error != "" {
				if chunk.OriginalError == nil || !isContextCanceledErr(chunk.OriginalError) {
					p.evict()
					evicted = true
				}
			}
			if chunk.Done {
				end()
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
