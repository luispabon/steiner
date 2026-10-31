package quota

import (
	"context"
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
// methods are safe for concurrent use. Store appends run on a dedicated writer
// goroutine; call Drain to wait for them and Close to stop the writer. Store I/O
// never runs while mu is held.
type Service struct {
	store        *Store
	writer       *persistWriter
	now          func() time.Time
	logf         func(string, ...any)
	fetchTimeout time.Duration
	loadMu       sync.Mutex // serializes ensureLoaded
	loaded       bool       // a store load has succeeded; guarded by loadMu
	updates      chan struct{}

	mu            sync.Mutex
	accounts      map[string]Account        // keyed by Account.Key
	aliases       map[string]string         // alias to Account.Key
	latest        map[string]Snapshot       // keyed by Snapshot.AccountKey
	inFlight      map[string]bool           // keyed by account key
	errs          map[string]string         // last fetch error per account key
	usedAliases   map[string]bool           // aliases marked by MarkUsed, not yet resolved
	lastPersisted map[string]persistedState // newest state this process wrote per account key
}

// persistedState is the newest snapshot this process wrote to the store, with
// the service clock reading it was appended at. Dedupe compares new state
// against it, never against the previous observation, so a chain of small drifts
// cannot hide a real change.
type persistedState struct {
	snap Snapshot
	at   time.Time
}

// NewService returns a Service configured by opts.
func NewService(opts ServiceOptions) *Service {
	s := &Service{
		store:         opts.Store,
		now:           opts.Now,
		logf:          opts.Logf,
		fetchTimeout:  opts.FetchTimeout,
		updates:       make(chan struct{}, 1),
		accounts:      make(map[string]Account),
		aliases:       make(map[string]string),
		latest:        make(map[string]Snapshot),
		inFlight:      make(map[string]bool),
		errs:          make(map[string]string),
		usedAliases:   make(map[string]bool),
		lastPersisted: make(map[string]persistedState),
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
	if s.store != nil {
		s.writer = newPersistWriter(s.store, s.logf, s.recordAppend)
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
// it is shown again if the key is registered later. The caller must hold s.mu.
func (s *Service) removeAliasLocked(old, alias string) {
	acct := s.accounts[old]
	acct.Aliases = slices.DeleteFunc(acct.Aliases, func(a string) bool { return a == alias })
	if len(acct.Aliases) == 0 {
		delete(s.accounts, old)
		return
	}
	s.accounts[old] = acct
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

// Load reads the stored history into memory until one load succeeds. Later calls
// do nothing, and a failed read is retried by the next call. Load neither builds
// projections nor notifies Updates.
func (s *Service) Load() {
	s.ensureLoaded()
}

// Updates returns a channel that receives a value after each change to the
// tracked state. The channel has capacity one, and notifications that arrive
// while one is pending are coalesced.
func (s *Service) Updates() <-chan struct{} {
	return s.updates
}

// commit records snap as the latest state for its account, queues it for
// persistence unless it repeats the last persisted state within an hour of that
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
	persisted, hadPersisted := s.lastPersisted[snap.AccountKey]
	unchanged := hadPersisted && sameState(persisted.snap, snap) && now.Sub(persisted.at) < dedupeWindow
	s.latest[snap.AccountKey] = snap
	var result enqueueResult
	if !unchanged {
		// Queued under s.mu so per-account write order matches commit order.
		// The lock order is s.mu then w.mu, and the writer never takes s.mu
		// while holding w.mu.
		result = s.persist(snap, now)
	}
	s.mu.Unlock()
	s.reportEnqueue(snap, result)
	s.notify()
}

// reportEnqueue logs a job the writer refused or displaced. It runs after s.mu is
// released, so logging never happens under the service lock.
func (s *Service) reportEnqueue(snap Snapshot, result enqueueResult) {
	if result.closed {
		s.logf("quota: dropped snapshot for %s fetched %s: persistence writer closed", snap.AccountKey, snap.FetchedAt)
		return
	}
	if result.dropped != nil {
		s.logf("quota: dropped queued snapshot for %s fetched %s: persist queue full", result.dropped.snap.AccountKey, result.dropped.snap.FetchedAt)
	}
}

// persist queues snap for the store writer and returns without waiting for it.
// The writer appends snapshots in commit order and logs failures; the in-memory
// state is already updated when persist runs. It reports what the writer did with
// the job so commit can log a drop after releasing mu. Code that reads the store
// in this process right after an observation must call Drain first.
func (s *Service) persist(snap Snapshot, now time.Time) enqueueResult {
	if s.writer == nil {
		return enqueueResult{}
	}
	return s.writer.enqueue(persistJob{snap: snap, at: now})
}

// recordAppend moves the persisted baseline to what the store now holds: job's
// snapshot after a write, or the stored snapshot a skipped file-level dedupe
// matched. It notifies Updates so projections are rebuilt from the new history.
// The writer calls it without holding its own mutex, so taking s.mu is safe.
func (s *Service) recordAppend(job persistJob, written bool, matched Snapshot) {
	s.mu.Lock()
	if written {
		s.lastPersisted[job.snap.AccountKey] = persistedState(job)
	} else if matched.AccountKey != "" {
		s.lastPersisted[matched.AccountKey] = persistedState{snap: matched, at: matched.FetchedAt}
	}
	s.mu.Unlock()
	s.notify()
}

// Drain waits until every snapshot this process queued for the store has been
// appended, or until ctx ends. Shutdown paths call it before the process exits,
// because persistence is asynchronous. A nil Service, and one without a store, is
// a no-op.
//
// A blocked append cannot be cancelled: when the deadline expires the writer may
// stay blocked on the store lock until the process exits, and the snapshots still
// queued or in flight are lost (the caller reports a warning). Observations
// arriving after a drain starts are still accepted and queued. Drain is safe to
// call repeatedly and concurrently, and a concurrent Close wakes a waiting Drain
// immediately instead of waiting for the deadline.
func (s *Service) Drain(ctx context.Context) error {
	if s == nil || s.writer == nil {
		return nil
	}
	return s.writer.drain(ctx)
}

// Close stops the store writer goroutine. Call it after Drain on the paths that
// own the service. An append already in flight still finishes, or stays blocked
// on the store lock until the lock is released or the process exits; jobs still
// queued are dropped and logged. Idempotent, and a nil Service or one without a
// store is a no-op.
func (s *Service) Close() {
	if s == nil || s.writer == nil {
		return
	}
	s.writer.close()
}

// notify sends a coalesced update notification without blocking.
func (s *Service) notify() {
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

// ensureLoaded fills the in-memory latest map and the persisted baseline from
// the store until one load succeeds. A failed load is retried by the next call.
// loadMu serializes loads, so concurrent callers wait for the load in flight
// instead of reading the store again. A stored snapshot replaces the in-memory
// one only when it is newer, so observed state is kept. Seeding the baseline is
// what makes dedupe survive a restart: without it every identical observation
// would queue an append that the store then skips. Store I/O runs with mu
// released.
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
		if cur, ok := s.lastPersisted[key]; !ok || snap.FetchedAt.After(cur.at) {
			s.lastPersisted[key] = persistedState{snap: snap, at: snap.FetchedAt}
		}
	}
	s.loaded = true
}
