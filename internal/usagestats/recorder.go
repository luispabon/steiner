package usagestats

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
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
	// ConfiguredProviderType is the provider type from the model's provider
	// configuration, before any transport override changes the effective type.
	// Pricing keys on it because models.dev lookup rules follow the configured
	// profile, the same source context-window resolution uses. It is not
	// persisted.
	ConfiguredProviderType string
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
	// ReasoningTokens is the part of CompletionTokens spent on reasoning. It is
	// a subset of CompletionTokens; Record clamps it to that range.
	ReasoningTokens int
	// CacheReported is true when the provider's usage object carried any cache
	// field, even one reporting zero tokens. Record also infers reported cache
	// data from positive cache token counts, so callers that predate this flag
	// keep counting their cache traffic.
	CacheReported bool
	// ReportedCostUSD is the provider-reported cost of this call in US dollars.
	// Nil means the provider reported no cost. Only OpenRouter's per-call cost
	// reaches this field; other providers' cost figures are envelope or
	// cumulative session totals.
	ReportedCostUSD *float64

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
	// ReasoningTokens is the reasoning portion of CompletionTokens, a subset
	// of it and never additive to any token total.
	ReasoningTokens int
	// CacheReportedRequests counts requests whose provider reported cache data.
	CacheReportedRequests int
	// CacheReportedInputTokens sums the prompt-side input total (uncached plus
	// cache read plus cache create) over those requests. It is the hit rate's
	// denominator, so requests whose provider said nothing about caching stay
	// out of it.
	CacheReportedInputTokens int
	// CostMicroUSD is the summed per-call cost, rounded to the nearest
	// micro-USD.
	CostMicroUSD int64
	// ReportedCostRequests counts requests whose cost the provider reported.
	ReportedCostRequests int
	// UnpricedRequests counts requests with neither reported nor estimated cost.
	UnpricedRequests int
}

// add sums o into b.
func (b *bucket) add(o bucket) {
	b.Requests += o.Requests
	b.InputTokens += o.InputTokens
	b.CacheReadTokens += o.CacheReadTokens
	b.CacheCreateTokens += o.CacheCreateTokens
	b.CompletionTokens += o.CompletionTokens
	b.ReasoningTokens += o.ReasoningTokens
	b.CacheReportedRequests += o.CacheReportedRequests
	b.CacheReportedInputTokens += o.CacheReportedInputTokens
	b.CostMicroUSD += o.CostMicroUSD
	b.ReportedCostRequests += o.ReportedCostRequests
	b.UnpricedRequests += o.UnpricedRequests
}

// sessionModelKey identifies one per-model, per-source session counter. It has
// no hour component: session aggregation spans the whole process lifetime.
type sessionModelKey struct {
	providerAlias  string
	providerType   string
	backendModelID string
	source         Source
}

// sessionCounters is the process-lifetime accumulation for one call surface.
// The cumulative fields feed SessionReport; the last fields describe the most
// recent request.
type sessionCounters struct {
	requests                 int64
	cacheReadTokens          int64
	inputTokens              int64
	reasoningTokens          int64
	cacheReportedRequests    int64
	cacheReportedInputTokens int64
	costMicroUSD             int64
	reportedCostRequests     int64
	unpricedRequests         int64
	lastCacheReadTokens      int64
	lastInputTokens          int64
	lastCacheReportedInput   int64
	lastCallKind             CallKind
}

// add accumulates delta and replaces the last-request fields with it.
func (c *sessionCounters) add(delta bucket) {
	inputTotal := int64(delta.InputTokens + delta.CacheReadTokens + delta.CacheCreateTokens)
	c.requests += int64(delta.Requests)
	c.cacheReadTokens += int64(delta.CacheReadTokens)
	c.inputTokens += inputTotal
	c.reasoningTokens += int64(delta.ReasoningTokens)
	c.cacheReportedRequests += int64(delta.CacheReportedRequests)
	c.cacheReportedInputTokens += int64(delta.CacheReportedInputTokens)
	c.costMicroUSD += delta.CostMicroUSD
	c.reportedCostRequests += int64(delta.ReportedCostRequests)
	c.unpricedRequests += int64(delta.UnpricedRequests)
	c.lastCacheReadTokens = int64(delta.CacheReadTokens)
	c.lastInputTokens = inputTotal
	c.lastCacheReportedInput = int64(delta.CacheReportedInputTokens)
}

// Recorder accumulates per-hour token usage observations in memory.
// It is safe for concurrent use.
type Recorder struct {
	mu      sync.Mutex
	buckets map[bucketKey]*bucket
	// sessionModels aggregates the same observations per model and source for
	// SessionWindow; ResetSession clears it.
	sessionModels map[sessionModelKey]*bucket

	// session holds the process-lifetime counters per call surface, which feed
	// SessionReport and SessionReportFor. ResetSession clears it.
	session map[Source]*sessionCounters

	now   func() time.Time
	store *store
	// pricer, when set, estimates cost for observations the provider did not
	// price. SetPricer swaps it atomically and Price runs with no lock held.
	pricer atomic.Pointer[Pricer]
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
		buckets:       st.load(),
		sessionModels: make(map[sessionModelKey]*bucket),
		session:       make(map[Source]*sessionCounters),
		now:           now,
		store:         st,
		telemetry:     newTelemetryFromEnv(),
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
	reasoning := min(max(obs.ReasoningTokens, 0), obs.CompletionTokens)

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
		ReasoningTokens:   reasoning,
	}
	// Positive cache token counts prove the provider reported cache data, so
	// they stand in for CacheReported on callers that predate the flag.
	cacheReported := obs.CacheReported || obs.CacheReadTokens+obs.CacheCreateTokens > 0
	if cacheReported {
		delta.CacheReportedRequests = 1
		delta.CacheReportedInputTokens = nonCached + obs.CacheReadTokens + obs.CacheCreateTokens
	}
	micro, reported, priced := r.costFor(obs)
	delta.CostMicroUSD = micro
	if reported {
		delta.ReportedCostRequests = 1
	}
	if !priced {
		delta.UnpricedRequests = 1
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

	sc, ok := r.session[obs.Source]
	if !ok {
		sc = &sessionCounters{}
		r.session[obs.Source] = sc
	}
	sc.add(delta)
	sc.lastCallKind = obs.CallKind
	r.mu.Unlock()

	// Hand the delta to the background writer: the delta fields are additive, so
	// queued observations coalesce into one locked read/merge/write instead of
	// blocking this goroutine on the cross-process store lock.
	r.writer.add(key, delta)

	r.telemetry.record(obs, at, micro, reasoning, cacheReported)
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
	clear(r.session)
}

// SessionReport returns the process-lifetime cache statistics derived from
// the dedicated session counters, blended across all sources, independent
// of the bucket map.
func (r *Recorder) SessionReport() SessionReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	var sr SessionReport
	for _, c := range r.session {
		sr.addCumulative(*c)
	}
	return sr
}

// SessionReportFor returns the process-lifetime cache statistics derived
// from the dedicated session counters, scoped to observations recorded
// with the given source.
func (r *Recorder) SessionReportFor(source Source) SessionReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.session[source]
	if !ok {
		return SessionReport{}
	}
	var sr SessionReport
	sr.addCumulative(*c)
	sr.LastCacheReadTokens = c.lastCacheReadTokens
	sr.LastTotalInputTokens = c.lastInputTokens
	sr.LastCacheReportedInputTokens = c.lastCacheReportedInput
	sr.LastCallKind = c.lastCallKind
	return sr
}

// costFor returns the micro-USD cost to count for one observation, and whether
// that cost was provider-reported and whether the request is priced at all. A
// provider-reported cost wins; otherwise an installed Pricer's estimate counts
// as priced without incrementing ReportedCostRequests. The pricer runs outside
// any Recorder lock because it may do its own lookups.
func (r *Recorder) costFor(obs Observation) (micro int64, reported bool, priced bool) {
	if obs.ReportedCostUSD != nil {
		micro = int64(math.Round(*obs.ReportedCostUSD * 1e6))
		if micro < 0 {
			micro = 0
		}
		return micro, true, true
	}
	if pricer := r.pricerOrNil(); pricer != nil {
		if rates, ok := pricer.Price(obs); ok {
			return rates.EstimateMicroUSD(obs.PromptTokens, obs.CacheReadTokens, obs.CacheCreateTokens, obs.CompletionTokens), false, true
		}
	}
	return 0, false, false
}
