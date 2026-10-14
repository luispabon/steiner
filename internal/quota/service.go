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
	loadOnce     sync.Once
	updates      chan struct{}

	mu          sync.Mutex
	accounts    map[string]Account  // keyed by Account.Key
	aliases     map[string]string   // alias to Account.Key
	latest      map[string]Snapshot // keyed by Snapshot.AccountKey
	inFlight    map[string]bool     // keyed by account key
	errs        map[string]string   // last fetch error per account key
	usedAliases map[string]bool     // aliases marked by MarkUsed, not yet resolved
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
// non-empty values in acct.
func (s *Service) Register(acct Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.accounts[acct.Key]
	cur.Key = acct.Key
	for _, alias := range acct.Aliases {
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
	cur.Unavailable = acct.Unavailable
	s.accounts[acct.Key] = cur
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
// snap.AccountKey and persists the result. A nil Service or an empty
// AccountKey is a no-op.
func (s *Service) Observe(snap Snapshot) {
	if s == nil || snap.AccountKey == "" {
		return
	}
	snap.Source = SourcePassive
	s.commit(snap, false)
}

// Updates returns a channel that receives a value after each change to the
// tracked state. The channel has capacity one, and notifications that arrive
// while one is pending are coalesced.
func (s *Service) Updates() <-chan struct{} {
	return s.updates
}

// commit records snap as the latest state for its account, persists the result,
// and notifies. When replace is false, snap is merged into the current state.
func (s *Service) commit(snap Snapshot, replace bool) {
	s.ensureLoaded()
	s.mu.Lock()
	if prev, ok := s.latest[snap.AccountKey]; ok && !replace {
		snap = mergePassive(prev, snap)
	}
	s.latest[snap.AccountKey] = snap
	s.mu.Unlock()
	s.persist(snap)
	s.notify()
}

// persist appends snap to the store. Failures are logged and do not stop the
// in-memory state from updating.
func (s *Service) persist(snap Snapshot) {
	if s.store == nil {
		return
	}
	if err := s.store.Append(snap, s.now()); err != nil {
		s.logf("quota: persist %s: %v", snap.AccountKey, err)
	}
}

// notify sends a coalesced update notification without blocking.
func (s *Service) notify() {
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

// ensureLoaded fills the in-memory latest map from the store on first use.
// Entries already observed in memory take precedence over stored ones. Store
// I/O runs with mu released.
func (s *Service) ensureLoaded() {
	s.loadOnce.Do(func() {
		if s.store == nil {
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
			if _, ok := s.latest[key]; !ok {
				s.latest[key] = snap
			}
		}
	})
}
