package delegation

import (
	"context"
	"sync"
	"time"
)

const dispatchGateTimeout = 10 * time.Second

type dispatchGate struct {
	ready chan struct{}
	once  sync.Once
}

func (g *dispatchGate) release() {
	g.once.Do(func() { close(g.ready) })
}

// CacheKeyStore caches one prompt-cache key per AgentType so repeated
// delegations to the same agent type reuse the same provider-side cache
// shard, instead of each spawn minting a fresh key. It is process-lifetime
// scoped (see doc comment on the zero-caller SessionStore.Reset in session.go
// for why this is a deliberate choice, not an oversight).
type CacheKeyStore struct {
	mu    sync.Mutex
	keys  map[AgentType]string
	gates map[string]*dispatchGate
}

// NewCacheKeyStore returns an initialized, empty CacheKeyStore.
func NewCacheKeyStore() *CacheKeyStore {
	return &CacheKeyStore{
		keys:  make(map[AgentType]string),
		gates: make(map[string]*dispatchGate),
	}
}

// KeyFor returns the cache key for agentType, minting and caching a new one
// via mintKey on first use. A mintKey error is returned to the caller and not
// cached, so a subsequent call for the same agentType retries minting rather
// than replaying the failure. Safe for concurrent use.
func (s *CacheKeyStore) KeyFor(agentType AgentType, mintKey func() (string, error)) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key, ok := s.keys[agentType]; ok {
		return key, nil
	}
	key, err := mintKey()
	if err != nil {
		return "", err
	}
	s.keys[agentType] = key
	return key, nil
}

func (s *CacheKeyStore) BeginDispatch(cacheKey string) (isLeader bool, release func(), wait func(ctx context.Context)) {
	if cacheKey == "" {
		return true, func() {}, func(context.Context) {}
	}

	s.mu.Lock()
	if g, ok := s.gates[cacheKey]; ok {
		s.mu.Unlock()
		return false, func() {}, s.waitFor(g)
	}
	g := &dispatchGate{ready: make(chan struct{})}
	s.gates[cacheKey] = g
	s.mu.Unlock()
	return true, s.releaseFor(cacheKey, g), func(context.Context) {}
}

func (s *CacheKeyStore) releaseFor(cacheKey string, g *dispatchGate) func() {
	return func() {
		g.release()
		s.mu.Lock()
		if s.gates[cacheKey] == g {
			delete(s.gates, cacheKey)
		}
		s.mu.Unlock()
	}
}

func (s *CacheKeyStore) waitFor(g *dispatchGate) func(ctx context.Context) {
	return func(ctx context.Context) {
		select {
		case <-g.ready:
		case <-time.After(dispatchGateTimeout):
		case <-ctx.Done():
		}
	}
}
