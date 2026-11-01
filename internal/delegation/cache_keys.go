package delegation

import (
	"context"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

const dispatchGateTimeout = 10 * time.Second

type dispatchGate struct {
	ready chan struct{}
	once  sync.Once
	// cause is written once, before ready closes, so a receiver of ready reads
	// the release cause without further synchronisation.
	cause output.DelegationCacheReleaseReason
}

func (g *dispatchGate) release(cause output.DelegationCacheReleaseReason) {
	g.once.Do(func() {
		g.cause = cause
		close(g.ready)
	})
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
	// testWaitTimeout overrides dispatchGateTimeout for same-package tests.
	testWaitTimeout time.Duration
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

func cacheKeyOrMint(store *CacheKeyStore, agentType AgentType) string {
	var key string
	if store != nil {
		// Intentionally ignore the error: an empty key falls through to a fresh mint.
		key, _ = store.KeyFor(agentType, provider.NewPromptCacheKey)
	}
	if key == "" {
		key, _ = provider.NewPromptCacheKey()
	}
	return key
}

// beginDispatch coordinates same-cache-key sibling delegations. The first caller for
// cacheKey is the leader: it returns release and wait functions that proceed immediately
// so it can dispatch at once; followers are told isLeader=false and must call the
// returned wait before spawning. The first release call sets the gate's release cause
// and clears a follower wait for this cacheKey; later calls are no-ops. An empty cacheKey
// is ungated: it always returns isLeader=true with a no-op release and wait.
//
// A follower's wait returns why it ended. Leaders never call wait, so their wait
// returns the zero reason.
func (s *CacheKeyStore) beginDispatch(cacheKey string) (isLeader bool, release func(output.DelegationCacheReleaseReason), wait func(ctx context.Context) output.DelegationCacheReleaseReason) {
	if cacheKey == "" {
		return true, func(output.DelegationCacheReleaseReason) {}, noWait
	}

	s.mu.Lock()
	if g, ok := s.gates[cacheKey]; ok {
		s.mu.Unlock()
		return false, func(output.DelegationCacheReleaseReason) {}, s.waitFor(g)
	}
	g := &dispatchGate{ready: make(chan struct{})}
	s.gates[cacheKey] = g
	s.mu.Unlock()
	return true, s.releaseFor(cacheKey, g), noWait
}

func noWait(context.Context) output.DelegationCacheReleaseReason {
	return ""
}

func (s *CacheKeyStore) releaseFor(cacheKey string, g *dispatchGate) func(output.DelegationCacheReleaseReason) {
	return func(cause output.DelegationCacheReleaseReason) {
		g.release(cause)
		s.mu.Lock()
		if s.gates[cacheKey] == g {
			delete(s.gates, cacheKey)
		}
		s.mu.Unlock()
	}
}

func (s *CacheKeyStore) waitFor(g *dispatchGate) func(ctx context.Context) output.DelegationCacheReleaseReason {
	timeout := dispatchGateTimeout
	if s.testWaitTimeout > 0 {
		timeout = s.testWaitTimeout
	}
	return waitForTimeout(g, timeout)
}

func waitForTimeout(g *dispatchGate, timeout time.Duration) func(ctx context.Context) output.DelegationCacheReleaseReason {
	return func(ctx context.Context) output.DelegationCacheReleaseReason {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		return g.await(ctx, timer.C)
	}
}

// await blocks until the gate is released, expired fires, or ctx ends, and
// reports why the wait ended. expired is the gate timer's channel.
func (g *dispatchGate) await(ctx context.Context, expired <-chan time.Time) output.DelegationCacheReleaseReason {
	select {
	case <-g.ready:
		return g.cause
	case <-expired:
		return g.releasedCauseOr(output.DelegationCacheReleaseTimeout)
	case <-ctx.Done():
		return g.releasedCauseOr(output.DelegationCacheReleaseCancelled)
	}
}

// releasedCauseOr returns the gate's release cause when ready is already closed.
// A select that observes ready and the timer or cancellation at the same instant
// picks between them at random, so without this check a released gate could be
// labelled as timed out or cancelled.
func (g *dispatchGate) releasedCauseOr(fallback output.DelegationCacheReleaseReason) output.DelegationCacheReleaseReason {
	select {
	case <-g.ready:
		return g.cause
	default:
		return fallback
	}
}
