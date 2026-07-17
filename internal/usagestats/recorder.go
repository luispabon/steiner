package usagestats

import (
	"log/slog"
	"sync"
	"time"
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

	// At is the time of the observation; used for hour-bucketing. Zero means use the recorder clock.
	At time.Time
}

type bucketKey struct {
	providerAlias  string
	providerType   string
	backendModelID string
	hourUnix       int64
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

	sessionCacheRead  int64
	sessionTotalInput int64

	now   func() time.Time
	store *store
}

// New returns a new Recorder. If now is nil, time.Now is used as the clock.
func New(now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	st := newStore(now)
	return &Recorder{
		buckets: st.load(),
		now:     now,
		store:   st,
	}
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

	r.sessionCacheRead += int64(obs.CacheReadTokens)
	r.sessionTotalInput += int64(nonCached + obs.CacheReadTokens + obs.CacheCreateTokens)
	r.mu.Unlock()

	// Persist this observation's delta. Wrap in a bucket for write path.
	delta := &bucket{
		Requests:          1,
		InputTokens:       nonCached,
		CacheReadTokens:   obs.CacheReadTokens,
		CacheCreateTokens: obs.CacheCreateTokens,
		CompletionTokens:  obs.CompletionTokens,
	}
	if err := r.store.write(delta, key); err != nil {
		slog.Warn("persist usage stats delta", "error", err)
	}
}

// Window sums all buckets whose hour falls within [now-d, now] and returns
// a Report with one Row per (ProviderAlias, ProviderType, BackendModelID) group.
func (r *Recorder) Window(d time.Duration) Report {
	now := r.now().UTC()
	cutoff := now.Add(-d)

	type groupKey struct {
		providerAlias  string
		providerType   string
		backendModelID string
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
// the dedicated session counters, independent of the bucket map.
func (r *Recorder) SessionReport() SessionReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return SessionReport{
		CacheReadTokens:  r.sessionCacheRead,
		TotalInputTokens: r.sessionTotalInput,
	}
}
