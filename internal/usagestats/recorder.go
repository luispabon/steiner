package usagestats

import (
	"context"
	"sync"
	"time"
)

// Source identifies which call surface produced an Observation, for
// session-scoped attribution (e.g. sidebar orchestrator-only reporting) and,
// since schema version 2, for per-source breakdown in the persisted store's
// buckets. Reports group rows by provider and model, then break each row down
// by source in Row.Sources.
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

// CallKind labels the kind of provider call an Observation came from. It is
// session-only attribution: it never reaches the persisted store or telemetry
// lines, only the in-memory report used to label the latest request.
type CallKind int

const (
	// CallKindNormal is an ordinary conversation request. It is the zero value
	// so observations that don't set CallKind default to it.
	CallKindNormal CallKind = iota
	// CallKindCompaction is a request issued to summarize context.
	CallKindCompaction
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
	// CallKind labels whether this was a normal or compaction request. It is
	// not persisted.
	CallKind CallKind

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

// add sums o into b.
func (b *bucket) add(o bucket) {
	b.Requests += o.Requests
	b.InputTokens += o.InputTokens
	b.CacheReadTokens += o.CacheReadTokens
	b.CacheCreateTokens += o.CacheCreateTokens
	b.CompletionTokens += o.CompletionTokens
}

// sessionModelKey identifies one per-model, per-source session counter. It has
// no hour component: session aggregation spans the whole process lifetime.
type sessionModelKey struct {
	providerAlias  string
	providerType   string
	backendModelID string
	source         Source
}

// Recorder accumulates per-hour token usage observations in memory.
// It is safe for concurrent use.
type Recorder struct {
	mu      sync.Mutex
	buckets map[bucketKey]*bucket
	// sessionModels aggregates the same observations per model and source for
	// SessionWindow; ResetSession clears it.
	sessionModels map[sessionModelKey]*bucket

	sessionCacheRead  map[Source]int64
	sessionTotalInput map[Source]int64
	sessionRequests   map[Source]int64
	lastCacheRead     map[Source]int64
	lastTotalInput    map[Source]int64
	lastCallKind      map[Source]CallKind

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
		sessionModels:     make(map[sessionModelKey]*bucket),
		sessionCacheRead:  make(map[Source]int64),
		sessionTotalInput: make(map[Source]int64),
		sessionRequests:   make(map[Source]int64),
		lastCacheRead:     make(map[Source]int64),
		lastTotalInput:    make(map[Source]int64),
		lastCallKind:      make(map[Source]CallKind),
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

	delta := bucket{
		Requests:          1,
		InputTokens:       nonCached,
		CacheReadTokens:   obs.CacheReadTokens,
		CacheCreateTokens: obs.CacheCreateTokens,
		CompletionTokens:  obs.CompletionTokens,
	}
	sessionKey := sessionModelKey{
		providerAlias:  obs.ProviderAlias,
		providerType:   obs.ProviderType,
		backendModelID: obs.BackendModelID,
		source:         obs.Source,
	}

	r.mu.Lock()
	b, ok := r.buckets[key]
	if !ok {
		b = &bucket{}
		r.buckets[key] = b
	}
	b.add(delta)
	if sm, ok := r.sessionModels[sessionKey]; ok {
		sm.add(delta)
	} else {
		counted := delta
		r.sessionModels[sessionKey] = &counted
	}

	r.sessionCacheRead[obs.Source] += int64(obs.CacheReadTokens)
	r.sessionTotalInput[obs.Source] += int64(nonCached + obs.CacheReadTokens + obs.CacheCreateTokens)
	r.sessionRequests[obs.Source]++
	r.lastCacheRead[obs.Source] = int64(obs.CacheReadTokens)
	r.lastTotalInput[obs.Source] = int64(nonCached + obs.CacheReadTokens + obs.CacheCreateTokens)
	r.lastCallKind[obs.Source] = obs.CallKind
	r.mu.Unlock()

	// Hand the delta to the background writer: the delta fields are additive, so
	// queued observations coalesce into one locked read/merge/write instead of
	// blocking this goroutine on the cross-process store lock.
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
	clear(r.sessionModels)
	clear(r.sessionCacheRead)
	clear(r.sessionTotalInput)
	clear(r.sessionRequests)
	clear(r.lastCacheRead)
	clear(r.lastTotalInput)
	clear(r.lastCallKind)
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
		LastCallKind:         r.lastCallKind[source],
	}
}
