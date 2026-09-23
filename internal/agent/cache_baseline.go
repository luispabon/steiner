package agent

import "sync"

// CacheBaselineKey identifies the cache scope under which a prior outbound
// message hash sequence is remembered. CacheKey must be nonempty: an empty key
// disables baseline tracking because the provider cannot route such a request
// to a stable cache shard.
type CacheBaselineKey struct {
	CacheKey               string
	ConfiguredProviderType string
	EffectiveProviderType  string
	EffectiveTransport     string
	ProviderAlias          string
	BackendModelID         string
}

// CacheBaselineStore remembers the per-message hash sequence of the last
// outbound request issued for each CacheBaselineKey. It is safe for concurrent
// use and is meant to be owned by a session and shared across its runs.
type CacheBaselineStore struct {
	mu      sync.Mutex
	entries map[CacheBaselineKey][]string
}

// NewCacheBaselineStore returns an empty baseline store.
func NewCacheBaselineStore() *CacheBaselineStore {
	return &CacheBaselineStore{entries: map[CacheBaselineKey][]string{}}
}

// CompareAndPromote atomically compares messageHashes against the sequence
// stored for key, promotes messageHashes to the new baseline, and reports the
// number of leading messages shared with the predecessor plus whether a
// predecessor existed. An empty key is a no-op returning (0, false) and never
// stores or compares anything.
func (s *CacheBaselineStore) CompareAndPromote(key CacheBaselineKey, messageHashes []string) (sharedPrefixMessages int, predecessorKnown bool) {
	if s == nil || key.CacheKey == "" {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[CacheBaselineKey][]string{}
	}
	if prev, ok := s.entries[key]; ok {
		sharedPrefixMessages = longestCommonPrefixLen(prev, messageHashes)
		predecessorKnown = true
	}
	s.entries[key] = append([]string(nil), messageHashes...)
	return sharedPrefixMessages, predecessorKnown
}
