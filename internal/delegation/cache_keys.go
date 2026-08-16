package delegation

import "sync"

// CacheKeyStore caches one prompt-cache key per AgentType so repeated
// delegations to the same agent type reuse the same provider-side cache
// shard, instead of each spawn minting a fresh key. It is process-lifetime
// scoped (see doc comment on the zero-caller SessionStore.Reset in session.go
// for why this is a deliberate choice, not an oversight).
type CacheKeyStore struct {
	mu   sync.Mutex
	keys map[AgentType]string
}

// NewCacheKeyStore returns an initialized, empty CacheKeyStore.
func NewCacheKeyStore() *CacheKeyStore {
	return &CacheKeyStore{keys: make(map[AgentType]string)}
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
