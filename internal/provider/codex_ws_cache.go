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
//
// Entries are generation-scoped. The generation is the session's prompt cache
// key, and acquiring an entry for a new generation retires every entry of the
// previous one, so a connection cannot outlive the session generation that
// created it (interactive rotation, clear, load and fork each mint a new key).
// Only cliRunner.runtimeProvider acquires entries, and one interactive live
// driver serialises runs and compactions while those operations keep the driver
// idle, so generations advance between runs rather than during one.
//
// Close retires every entry and rejects later acquisition. Retirement detaches
// entries under the cache lock and closes their connections outside it, so an
// entry with a request in flight is never interrupted: its connection closes
// when that request ends.
type CodexWSCache struct {
	mu         sync.Mutex
	instances  map[string]*CachingCodexWS
	generation string
	closed     bool
}

var (
	errCodexWSCacheClosed            = errors.New("codex websocket cache closed")
	errCodexWSCacheGenerationChanged = errors.New("codex websocket cache generation changed")
)

// NewCodexWSCache returns an empty CodexWSCache ready for use.
func NewCodexWSCache() *CodexWSCache {
	return &CodexWSCache{instances: make(map[string]*CachingCodexWS)}
}

// Close retires every cached entry and rejects later acquisition. It is
// idempotent and does not wait for requests in flight: an active entry is
// detached now and closed when its request ends.
func (c *CodexWSCache) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	detached := c.detachAllLocked()
	c.mu.Unlock()

	retireCodexWSEntries(detached)
	return nil
}

// beginGenerationLocked makes generation the current generation, detaching the
// entries of the previous one. A closed cache rejects the acquisition. Callers
// hold c.mu and must retire the returned entries after releasing it.
func (c *CodexWSCache) beginGenerationLocked(generation string) ([]*CachingCodexWS, error) {
	if c.closed {
		return nil, errCodexWSCacheClosed
	}
	if c.generation == generation {
		return nil, nil
	}
	c.generation = generation
	return c.detachAllLocked(), nil
}

// currentLocked reports whether generation is still the cache's current
// generation and the cache is open. Callers hold c.mu.
func (c *CodexWSCache) currentLocked(generation string) error {
	if c.closed {
		return errCodexWSCacheClosed
	}
	if c.generation != generation {
		return errCodexWSCacheGenerationChanged
	}
	return nil
}

// detachAllLocked removes every cached entry and returns it. Callers hold c.mu.
func (c *CodexWSCache) detachAllLocked() []*CachingCodexWS {
	if len(c.instances) == 0 {
		return nil
	}
	detached := make([]*CachingCodexWS, 0, len(c.instances))
	for key, entry := range c.instances {
		detached = append(detached, entry)
		delete(c.instances, key)
	}
	return detached
}

// retireCodexWSEntries retires detached entries. Callers must not hold cache.mu.
func retireCodexWSEntries(entries []*CachingCodexWS) {
	for _, entry := range entries {
		entry.retire()
	}
}

// NewCachingCodexWS returns the cached provider for key within generation,
// building one via build on a miss and storing it. A new generation retires the
// previous generation's entries. A build that finishes after the cache closes,
// or after another generation became current, is rejected without being stored
// or used; only that never-dialled provider is closed.
func NewCachingCodexWS(cache *CodexWSCache, generation, key string, build func() (Provider, error)) (Provider, error) {
	cache.mu.Lock()
	retired, err := cache.beginGenerationLocked(generation)
	if err != nil {
		cache.mu.Unlock()
		return nil, err
	}
	if entry, ok := cache.instances[key]; ok {
		cache.mu.Unlock()
		retireCodexWSEntries(retired)
		return entry, nil
	}
	cache.mu.Unlock()
	retireCodexWSEntries(retired)

	built, err := build()
	if err != nil {
		return nil, err
	}

	entry := &CachingCodexWS{cache: cache, inner: built, key: key, idle: make(chan struct{})}

	cache.mu.Lock()
	// Recheck on insertion: another build for this key, a newer generation or
	// Close may have landed while build ran.
	err = cache.currentLocked(generation)
	if err == nil {
		if existing, ok := cache.instances[key]; ok {
			cache.mu.Unlock()
			// The provider was never dialled: no request has run on it, so
			// closing it cannot interrupt a stream.
			entry.retire()
			return existing, nil
		}
		cache.instances[key] = entry
	}
	cache.mu.Unlock()

	if err != nil {
		entry.retire()
		return nil, err
	}
	return entry, nil
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
	// closed records that the wrapped connection has been closed, so
	// retirement and the end of an active request close it at most once.
	closed bool
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

// end marks the request done. A retirement that arrived while it was active
// closes the wrapped connection here, after the wrapper lock is released.
func (p *CachingCodexWS) end() {
	p.mu.Lock()
	p.active = false
	closeNow := p.retired && !p.closed
	if closeNow {
		p.closed = true
	}
	close(p.idle)
	p.idle = make(chan struct{})
	p.mu.Unlock()

	if closeNow {
		p.closeInner()
	}
}

// retire rejects new requests and closes the wrapped connection once no request
// is in flight, so an active stream always runs to completion. It is idempotent
// and never interrupts an active stream: that entry closes on end instead.
// Callers must not hold cache.mu.
func (p *CachingCodexWS) retire() {
	p.mu.Lock()
	p.retired = true
	closeNow := !p.active && !p.closed
	if closeNow {
		p.closed = true
	}
	p.mu.Unlock()

	if closeNow {
		p.closeInner()
	}
}

// closeInner closes the wrapped provider's connection, if it has one. It runs
// outside the wrapper lock because closing joins the keepalive goroutine.
func (p *CachingCodexWS) closeInner() {
	inner, ok := p.inner.(*codexWSProvider)
	if !ok {
		return
	}
	inner.mu.Lock()
	inner.closeConn()
	inner.mu.Unlock()
}

// evict detaches p from the cache so the next lookup for this key builds a
// fresh provider, then retires it.
func (p *CachingCodexWS) evict() {
	p.cache.mu.Lock()
	if p.cache.instances[p.key] == p {
		delete(p.cache.instances, p.key)
	}
	p.cache.mu.Unlock()

	p.retire()
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
