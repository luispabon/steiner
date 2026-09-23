package agent

import "sync"

// CacheBaselineKey identifies the cache scope under which a prior outbound
// message hash sequence is remembered. CacheKey must be nonempty: an empty key
// disables baseline tracking because the provider cannot route such a request
// to a stable cache shard. AgentID separates concurrent same-type sub-agent
// siblings, which intentionally share CacheKey (one prompt-cache key per
// AgentType, see delegation.CacheKeyStore.KeyFor): the parent run uses the
// zero value, and each child uses its own delegation AgentID, stable across
// that child's turns (including follow_up resumption) so a child's turn N
// compares against its own turn N-1, never a sibling's.
type CacheBaselineKey struct {
	CacheKey               string
	AgentID                string
	ConfiguredProviderType string
	EffectiveProviderType  string
	EffectiveTransport     string
	ProviderAlias          string
	BackendModelID         string
}

// CacheBaselineStore remembers the per-message hash sequence of the last
// outbound request accepted by the provider for each CacheBaselineKey. It is
// safe for concurrent use and is meant to be owned by a session and shared
// across its runs.
type CacheBaselineStore struct {
	mu      sync.Mutex
	entries map[CacheBaselineKey][]string
}

// NewCacheBaselineStore returns an empty baseline store.
func NewCacheBaselineStore() *CacheBaselineStore {
	return &CacheBaselineStore{entries: map[CacheBaselineKey][]string{}}
}

// Compare reports how many leading elements of messageHashes match the
// sequence stored for key, and whether a predecessor was stored at all. It
// does not modify the stored baseline; call Promote separately once the
// caller knows the request messageHashes was computed for actually reached
// the provider and was accepted. An empty key is a no-op returning (0,
// false) and never reads the store.
func (s *CacheBaselineStore) Compare(key CacheBaselineKey, messageHashes []string) (sharedPrefixMessages int, predecessorKnown bool) {
	if s == nil || key.CacheKey == "" {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.entries[key]
	if !ok {
		return 0, false
	}
	return longestCommonPrefixLen(prev, messageHashes), true
}

// Promote stores messageHashes as the new baseline for key, replacing
// whatever was stored previously. Call it only for a request that the
// provider actually accepted: a rejected attempt must never become the
// predecessor for the next comparison. An empty key is a no-op.
func (s *CacheBaselineStore) Promote(key CacheBaselineKey, messageHashes []string) {
	if s == nil || key.CacheKey == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[CacheBaselineKey][]string{}
	}
	s.entries[key] = append([]string(nil), messageHashes...)
}
