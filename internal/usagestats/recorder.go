package usagestats

import (
	"context"
	"sync"
	"time"
)

// Source identifies which call surface produced an Observation, for
// session-scoped attribution (e.g. sidebar orchestrator-only reporting) and,
// since schema version 2, for per-source breakdown in the persisted store's
// buckets. Window splits advisor traffic into separate rows while combining
// parent, sub-agent, and unknown-source traffic by provider and model.
type Source int

const (
	// SourceParent is the top-level orchestrator run. It is the zero value
	// so call sites that don't set Source default to it.
	SourceParent Source = iota
	// SourceSubAgent is a delegated sub-agent run.
	SourceSubAgent
	// SourceAdvisor is the advisor tool.
	SourceAdvisor
	// SourceUnknown marks a persisted bucket decoded from a pre-schema-v2
	// store file, which recorded no source at all. Attributing that history
	// to SourceParent would misrepresent it as current parent-run traffic in
	// a per-source breakdown; SourceUnknown keeps it visibly separate
	// instead. Never set on a freshly recorded Observation.
	SourceUnknown
)

// Observation is the consumer-shaped input describing one API call's token usage.
type Observation struct {
	// ProviderAlias is the user-configured alias for the provider.
	ProviderAlias string
	// ProviderType is the provider driver type (e.g. "openai", "anthropic").
	ProviderType string
	// BackendModelID is the model identifier as reported by the backend.
	BackendModelID string

	// PromptTokens is the raw total prompt token count from the provider response.
	PromptTokens int
	// CompletionTokens is the number of output tokens generated.
	CompletionTokens int
	// CacheReadTokens is the number of tokens served from the prompt cache.
	CacheReadTokens int
	// CacheCreateTokens is the number of tokens written into the prompt cache.
	CacheCreateTokens int

	// Source identifies which call surface produced this observation.
	Source Source

	// At is the time of the observation; used for hour-bucketing. Zero means use the recorder clock.
	At time.Time
}

type bucketKey struct {
	providerAlias  string
	providerType   string
	backendModelID string
	hourUnix       int64
	source         Source
}

type bucket struct {
	Requests          int
	InputTokens       int
	CacheReadTokens   int
	CacheCreateTokens int
	CompletionTokens  int
}

// Recorder accumulates per-hour token usage observations in memory.
// It is safe for concurrent use.
type Recorder struct {
	mu      sync.Mutex
	buckets map[bucketKey]*bucket

	sessionCacheRead  map[Source]int64
	sessionTotalInput map[Source]int64
	sessionRequests   map[Source]int64
	lastCacheRead     map[Source]int64
	lastTotalInput    map[Source]int64

	now   func() time.Time
	store *store
	// writer persists coalesced deltas off the recording goroutine, so Record
	// never waits on the cross-process store lock.
	writer *persistWriter

	// telemetry is nil unless TelemetryEnvVar is set; a nil value records nothing.
	telemetry *telemetry
}

// New returns a new Recorder. If now is nil, time.Now is used as the clock.
func New(now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	st := newStore(now)
	r := &Recorder{
		buckets:           st.load(),
		sessionCacheRead:  make(map[Source]int64),
		sessionTotalInput: make(map[Source]int64),
		sessionRequests:   make(map[Source]int64),
		lastCacheRead:     make(map[Source]int64),
		lastTotalInput:    make(map[Source]int64),
		now:               now,
		store:             st,
		telemetry:         newTelemetryFromEnv(),
	}
	r.writer = newPersistWriter(st, nil)
	return r
}

// Record incorporates one observation into the in-memory buckets and session counters.
func (r *Recorder) Record(obs Observation) {
	at := obs.At
	if at.IsZero() {
		at = r.now()
	}
	hour := at.UTC().Truncate(time.Hour)

	nonCached := obs.PromptTokens - obs.CacheReadTokens - obs.CacheCreateTokens
	if nonCached < 0 {
		nonCached = 0
	}

	key := bucketKey{
		providerAlias:  obs.ProviderAlias,
		providerType:   obs.ProviderType,
		backendModelID: obs.BackendModelID,
		hourUnix:       hour.Unix(),
		source:         obs.Source,
	}

	r.mu.Lock()
	b, ok := r.buckets[key]
	if !ok {
		b = &bucket{}
		r.buckets[key] = b
	}
	b.Requests++
	b.InputTokens += nonCached
	b.CacheReadTokens += obs.CacheReadTokens
	b.CacheCreateTokens += obs.CacheCreateTokens
	b.CompletionTokens += obs.CompletionTokens

	r.sessionCacheRead[obs.Source] += int64(obs.CacheReadTokens)
	r.sessionTotalInput[obs.Source] += int64(nonCached + obs.CacheReadTokens + obs.CacheCreateTokens)
	r.sessionRequests[obs.Source]++
	r.lastCacheRead[obs.Source] = int64(obs.CacheReadTokens)
	r.lastTotalInput[obs.Source] = int64(nonCached + obs.CacheReadTokens + obs.CacheCreateTokens)
	r.mu.Unlock()

	// Hand the delta to the background writer: the delta fields are additive, so
	// queued observations coalesce into one locked read/merge/write instead of
	// blocking this goroutine on the cross-process store lock.
	delta := bucket{
		Requests:          1,
		InputTokens:       nonCached,
		CacheReadTokens:   obs.CacheReadTokens,
		CacheCreateTokens: obs.CacheCreateTokens,
		CompletionTokens:  obs.CompletionTokens,
	}
	r.writer.add(key, delta)

	r.telemetry.record(obs, at)
}

// Drain waits until every usage delta this process queued for the store has
// been written, or until ctx ends. Shutdown paths call it before the process
// exits, because persistence is asynchronous. A nil Recorder is a no-op.
//
// A write blocked on the store lock cannot be cancelled: when the deadline
// expires the writer may stay blocked until the lock is released or the process
// exits, and the deltas still pending are lost (the caller reports a warning).
// Observations arriving after a drain starts are still accepted and queued.
// Drain is safe to call repeatedly and concurrently, and a concurrent Close
// wakes a waiting Drain instead of letting it wait for the deadline.
func (r *Recorder) Drain(ctx context.Context) error {
	if r == nil || r.writer == nil {
		return nil
	}
	return r.writer.drain(ctx)
}

// Close stops the store writer goroutine. Call it after Drain on the paths that
// own the Recorder. A write already in flight still finishes, or stays blocked
// on the store lock until the lock is released or the process exits; deltas
// still pending are dropped and logged. Idempotent, and a nil Recorder is a
// no-op.
func (r *Recorder) Close() {
	if r == nil || r.writer == nil {
		return
	}
	r.writer.close()
}

// ResetSession clears in-memory session counters while preserving persisted
// hourly buckets.
func (r *Recorder) ResetSession() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.sessionCacheRead)
	clear(r.sessionTotalInput)
	clear(r.sessionRequests)
	clear(r.lastCacheRead)
	clear(r.lastTotalInput)
}

// Window sums all buckets whose hour intersects the interval [now-d, now] and
// returns a Report with one Row per (ProviderAlias, ProviderType, BackendModelID)
// group. It uses hour-granularity buckets and may include up to one hour of
// extra data at the lower boundary (e.g. a 1-hour query at 10:30 includes all
// of the 09:00 bucket, not just data from 09:30-10:30).
func (r *Recorder) Window(d time.Duration) Report {
	now := r.now().UTC()
	cutoff := now.Add(-d)

	type groupKey struct {
		providerAlias  string
		providerType   string
		backendModelID string
		advisor        bool
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	groups := make(map[groupKey]*bucket)
	for k, b := range r.buckets {
		t := time.Unix(k.hourUnix, 0).UTC()
		if !t.Before(cutoff.Truncate(time.Hour)) && !t.After(now) {
			gk := groupKey{
				providerAlias:  k.providerAlias,
				providerType:   k.providerType,
				backendModelID: k.backendModelID,
				advisor:        k.source == SourceAdvisor,
			}
			g, ok := groups[gk]
			if !ok {
				g = &bucket{}
				groups[gk] = g
			}
			g.Requests += b.Requests
			g.InputTokens += b.InputTokens
			g.CacheReadTokens += b.CacheReadTokens
			g.CacheCreateTokens += b.CacheCreateTokens
			g.CompletionTokens += b.CompletionTokens
		}
	}

	rows := make([]Row, 0, len(groups))
	for gk, g := range groups {
		rows = append(rows, Row{
			ProviderAlias:     gk.providerAlias,
			ProviderType:      gk.providerType,
			BackendModelID:    gk.backendModelID,
			Advisor:           gk.advisor,
			Requests:          g.Requests,
			InputTokens:       g.InputTokens,
			CacheReadTokens:   g.CacheReadTokens,
			CacheCreateTokens: g.CacheCreateTokens,
			CompletionTokens:  g.CompletionTokens,
		})
	}

	return Report{Rows: rows}
}

// SessionReport returns the process-lifetime cache statistics derived from
// the dedicated session counters, blended across all sources, independent
// of the bucket map.
func (r *Recorder) SessionReport() SessionReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	var sr SessionReport
	for source := range r.sessionCacheRead {
		sr.CacheReadTokens += r.sessionCacheRead[source]
		sr.TotalInputTokens += r.sessionTotalInput[source]
		sr.Requests += r.sessionRequests[source]
	}
	return sr
}

// SessionReportFor returns the process-lifetime cache statistics derived
// from the dedicated session counters, scoped to observations recorded
// with the given source.
func (r *Recorder) SessionReportFor(source Source) SessionReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return SessionReport{
		CacheReadTokens:      r.sessionCacheRead[source],
		TotalInputTokens:     r.sessionTotalInput[source],
		Requests:             r.sessionRequests[source],
		LastCacheReadTokens:  r.lastCacheRead[source],
		LastTotalInputTokens: r.lastTotalInput[source],
	}
}
