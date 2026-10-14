package quota

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"
)

// maxErrLen bounds the fetch error kept for display.
const maxErrLen = 80

// RefreshAll fetches every registered account that has a Fetcher, with
// AllowSpawn set and regardless of snapshot age. Fetches run one at a time, and
// RefreshAll returns after they all complete.
func (s *Service) RefreshAll(ctx context.Context) {
	for _, acct := range s.fetchableAccounts() {
		s.refresh(ctx, acct, FetchOptions{AllowSpawn: true})
	}
}

// RefreshUsed resolves the aliases marked by MarkUsed and fetches each resolved
// account whose latest snapshot is older than minAge, with AllowSpawn false. A
// resolved alias is cleared from the used set whether or not its account was
// fetched. Aliases not registered yet stay marked. RefreshUsed returns after
// its fetches complete.
func (s *Service) RefreshUsed(ctx context.Context, minAge time.Duration) {
	s.ensureLoaded()
	now := s.now()

	s.mu.Lock()
	due := make(map[string]Account)
	for alias := range s.usedAliases {
		key, ok := s.aliases[alias]
		if !ok {
			continue
		}
		delete(s.usedAliases, alias)
		acct := s.accounts[key]
		if acct.Fetcher == nil {
			continue
		}
		if snap, ok := s.latest[key]; ok && now.Sub(snap.FetchedAt) <= minAge {
			continue
		}
		due[key] = acct
	}
	s.mu.Unlock()

	for _, key := range slices.Sorted(maps.Keys(due)) {
		s.refresh(ctx, due[key], FetchOptions{})
	}
}

// fetchableAccounts returns the registered accounts that have a Fetcher, sorted
// by Key.
func (s *Service) fetchableAccounts() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Account
	for _, key := range slices.Sorted(maps.Keys(s.accounts)) {
		if acct := s.accounts[key]; acct.Fetcher != nil {
			out = append(out, acct)
		}
	}
	return out
}

// refresh fetches one account unless a fetch for it is already in flight, in
// which case it returns at once. A failed fetch keeps the previous snapshot and
// records a short error. A successful fetch replaces the latest snapshot.
func (s *Service) refresh(ctx context.Context, acct Account, opts FetchOptions) {
	s.mu.Lock()
	if s.inFlight[acct.Key] {
		s.mu.Unlock()
		return
	}
	s.inFlight[acct.Key] = true
	s.mu.Unlock()

	snap, err := s.fetch(ctx, acct, opts)

	s.mu.Lock()
	delete(s.inFlight, acct.Key)
	if err != nil {
		s.errs[acct.Key] = truncate(err.Error(), maxErrLen)
	} else {
		delete(s.errs, acct.Key)
	}
	s.mu.Unlock()

	if err != nil {
		s.logf("quota: refresh %s: %v", acct.Key, err)
		s.notify()
		return
	}
	s.commit(snap, true)
}

// fetch calls the account's Fetcher under a timeout. The Fetcher's own
// TimeoutFetcher value wins when positive, and the service timeout applies
// otherwise.
func (s *Service) fetch(ctx context.Context, acct Account, opts FetchOptions) (Snapshot, error) {
	timeout := s.fetchTimeout
	if tf, ok := acct.Fetcher.(TimeoutFetcher); ok {
		if d := tf.FetchTimeout(); d > 0 {
			timeout = d
		}
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	snap, err := acct.Fetcher.Fetch(fetchCtx, opts)
	if err != nil {
		return Snapshot{}, fmt.Errorf("fetch quota: %w", err)
	}
	snap.AccountKey = acct.Key
	if snap.ProviderType == "" {
		snap.ProviderType = acct.ProviderType
	}
	if snap.FetchedAt.IsZero() {
		snap.FetchedAt = s.now()
	}
	snap.Source = SourceFetch
	return snap, nil
}

// truncate returns the first n runes of s.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
