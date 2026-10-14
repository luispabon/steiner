package quota

import (
	"slices"
	"sync"
	"time"
)

// defaultFetchTimeout bounds a Fetcher call when neither the Fetcher nor
// ServiceOptions sets a timeout.
const defaultFetchTimeout = 10 * time.Second

// ServiceOptions configures NewService. Zero values select time.Now, a no-op
// Logf, and a 10s fetch timeout. A nil Store keeps snapshots in memory only.
type ServiceOptions struct {
	Store        *Store
	Now          func() time.Time
	Logf         func(string, ...any)
	FetchTimeout time.Duration
}

// Service keeps the latest snapshot per account, merges passive observations,
// refreshes accounts through their Fetchers, and persists every change. Its
// methods are safe for concurrent use. Store I/O never runs while mu is held.
type Service struct {
	store        *Store
	now          func() time.Time
	logf         func(string, ...any)
	fetchTimeout time.Duration
	loadMu       sync.Mutex // serializes ensureLoaded
	loaded       bool       // a store load has succeeded; guarded by loadMu
	updates      chan struct{}

	mu          sync.Mutex
	accounts    map[string]Account   // keyed by Account.Key
	aliases     map[string]string    // alias to Account.Key
	latest      map[string]Snapshot  // keyed by Snapshot.AccountKey
	inFlight    map[string]bool      // keyed by account key
	errs        map[string]string    // last fetch error per account key
	usedAliases map[string]bool      // aliases marked by MarkUsed, not yet resolved
	lastAppend  map[string]time.Time // time of the last successful Store.Append per account key
}

// NewService returns a Service configured by opts.
func NewService(opts ServiceOptions) *Service {
	s := &Service{
		store:        opts.Store,
		now:          opts.Now,
		logf:         opts.Logf,
		fetchTimeout: opts.FetchTimeout,
		updates:      make(chan struct{}, 1),
		accounts:     make(map[string]Account),
		aliases:      make(map[string]string),
		latest:       make(map[string]Snapshot),
		inFlight:     make(map[string]bool),
		errs:         make(map[string]string),
		usedAliases:  make(map[string]bool),
		lastAppend:   make(map[string]time.Time),
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.logf == nil {
		s.logf = func(string, ...any) {}
	}
	if s.fetchTimeout <= 0 {
		s.fetchTimeout = defaultFetchTimeout
	}
	return s
}

// Register adds acct. If an account with the same Key exists, its aliases are
// merged, and its Fetcher, ProviderType, and Unavailable are replaced by the
// non-empty values in acct. An alias bound to a different Key moves to acct and
// is removed from that Key's account, which is dropped once it has no aliases
// left. Register notifies Updates.
func (s *Service) Register(acct Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.accounts[acct.Key]
	cur.Key = acct.Key
	for _, alias := range acct.Aliases {
		if old, ok := s.aliases[alias]; ok && old != acct.Key {
			s.removeAliasLocked(old, alias)
		}
		if !slices.Contains(cur.Aliases, alias) {
			cur.Aliases = append(cur.Aliases, alias)
		}
		s.aliases[alias] = acct.Key
	}
	if acct.ProviderType != "" {
		cur.ProviderType = acct.ProviderType
	}
	if acct.Fetcher != nil {
		cur.Fetcher = acct.Fetcher
	}
	if acct.Unavailable != "" {
		cur.Unavailable = acct.Unavailable
	}
	s.accounts[acct.Key] = cur
	s.notify()
}

// removeAliasLocked unbinds alias from the account old. An account left with no
// aliases is removed from s.accounts. Its latest snapshot stays in s.latest, so
// it is listed like an unregistered stored snapshot. The caller must hold s.mu.
func (s *Service) removeAliasLocked(old, alias string) {
	acct := s.accounts[old]
	acct.Aliases = slices.DeleteFunc(acct.Aliases, func(a string) bool { return a == alias })
	if len(acct.Aliases) == 0 {
		delete(s.accounts, old)
		return
	}
	s.accounts[old] = acct
}

// AccountKeyForAlias returns the account key registered for alias.
func (s *Service) AccountKeyForAlias(alias string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.aliases[alias]
	return key, ok
}

// MarkUsed records that alias was used. The alias is resolved by RefreshUsed,
// so MarkUsed may be called before Register. A nil Service is a no-op.
func (s *Service) MarkUsed(alias string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usedAliases[alias] = true
}

// Observe merges a passive observation into the latest snapshot for
// snap.AccountKey and persists the result. A zero FetchedAt is stamped with the
// service clock before the merge. A nil Service or an empty AccountKey is a
// no-op.
func (s *Service) Observe(snap Snapshot) {
	if s == nil || snap.AccountKey == "" {
		return
	}
	snap.Source = SourcePassive
	if snap.FetchedAt.IsZero() {
		snap.FetchedAt = s.now()
	}
	s.commit(snap, false)
}

// Updates returns a channel that receives a value after each change to the
// tracked state. The channel has capacity one, and notifications that arrive
// while one is pending are coalesced.
func (s *Service) Updates() <-chan struct{} {
	return s.updates
}

// commit records snap as the latest state for its account, persists it unless
// it repeats the previous state within an hour of the account's last successful
// append, and notifies. When replace is false, snap is merged into the current
// state. The in-memory state and the notification always update.
func (s *Service) commit(snap Snapshot, replace bool) {
	s.ensureLoaded()
	now := s.now()
	s.mu.Lock()
	prev, hadPrev := s.latest[snap.AccountKey]
	if hadPrev && !replace {
		snap = mergePassive(prev, snap)
	}
	unchanged := hadPrev && sameState(prev, snap) && now.Sub(s.lastAppend[snap.AccountKey]) < time.Hour
	s.latest[snap.AccountKey] = snap
	s.mu.Unlock()
	if !unchanged {
		s.persist(snap, now)
	}
	s.notify()
}

// persist appends snap to the store and records now as the account's last
// successful append. Failures are logged; the in-memory state is already
// updated when persist runs.
func (s *Service) persist(snap Snapshot, now time.Time) {
	if s.store == nil {
		return
	}
	if err := s.store.Append(snap, now); err != nil {
		s.logf("quota: persist %s: %v", snap.AccountKey, err)
		return
	}
	s.mu.Lock()
	s.lastAppend[snap.AccountKey] = now
	s.mu.Unlock()
}

// notify sends a coalesced update notification without blocking.
func (s *Service) notify() {
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

// ensureLoaded fills the in-memory latest map from the store until one load
// succeeds. A failed load is retried by the next call. loadMu serializes loads,
// so concurrent callers wait for the load in flight instead of reading the store
// again. A stored snapshot replaces the in-memory one only when it is newer, so
// observed state is kept. Store I/O runs with mu released.
func (s *Service) ensureLoaded() {
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	if s.loaded || s.store == nil {
		return
	}
	latest, err := s.store.Latest()
	if err != nil {
		s.logf("quota: load history: %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, snap := range latest {
		if cur, ok := s.latest[key]; !ok || snap.FetchedAt.After(cur.FetchedAt) {
			s.latest[key] = snap
		}
	}
	s.loaded = true
}
