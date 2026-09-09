package usagestats

// Row holds aggregated token counts and request count for a single
// (provider, providerType, model) group within a time window.
type Row struct {
	// ProviderAlias is the user-configured alias for the provider.
	ProviderAlias string
	// ProviderType is the provider driver type (e.g. "openai", "anthropic").
	ProviderType string
	// BackendModelID is the model identifier as reported by the backend.
	BackendModelID string

	// Requests is the total number of API calls in this group.
	Requests int
	// InputTokens is the non-cached portion of prompt tokens.
	InputTokens int
	// CacheReadTokens is the number of tokens served from the prompt cache.
	CacheReadTokens int
	// CacheCreateTokens is the number of tokens written into the prompt cache.
	CacheCreateTokens int
	// CompletionTokens is the number of output tokens generated.
	CompletionTokens int
}

// HitRate returns the token-weighted cache hit rate for this row.
// It is defined as CacheReadTokens / (InputTokens + CacheReadTokens + CacheCreateTokens).
// When total input is zero, ok is false and rate is 0; callers should render "—".
func (r Row) HitRate() (rate float64, ok bool) {
	return HitRate(r.CacheReadTokens, r.InputTokens, r.CacheCreateTokens)
}

// UncachedPerRequest returns the average per-request tokens that did not
// come from a cache read (non-cached input plus cache-create tokens, the
// same grouping HitRate treats as "not a hit"). A single ratio can rise
// either because uncached tokens grew or because cached tokens shrank; this
// and CachedPerRequest decompose that ambiguity, which HitRate alone cannot,
// while staying consistent with it: CachedPerRequest / (CachedPerRequest +
// UncachedPerRequest) reproduces HitRate. Zero requests returns (0, false).
func (r Row) UncachedPerRequest() (float64, bool) {
	return perRequest(r.InputTokens+r.CacheCreateTokens, r.Requests)
}

// CachedPerRequest returns the average cache-read prompt tokens per
// request. Zero requests returns (0, false).
func (r Row) CachedPerRequest() (float64, bool) {
	return perRequest(r.CacheReadTokens, r.Requests)
}

func perRequest(tokens, requests int) (float64, bool) {
	if requests == 0 {
		return 0, false
	}
	return float64(tokens) / float64(requests), true
}

// Report is the result of a Window query, containing one Row per
// (provider, providerType, model) combination found in the queried interval.
type Report struct {
	// Rows contains one aggregated entry per attribution group.
	Rows []Row
}

// SessionReport holds process-lifetime cache statistics derived from
// the dedicated session counters (independent of the bucket map).
type SessionReport struct {
	// CacheReadTokens is the total cache-read tokens seen this session.
	CacheReadTokens int64
	// TotalInputTokens is the total input tokens (prompt + cache read + cache create) this session.
	TotalInputTokens int64
	// Requests is the total number of API calls seen this session.
	Requests int64
}

// HitRate returns the token-weighted cache hit rate for the session.
// When TotalInputTokens is zero, ok is false and rate is 0; callers should render "—".
func (s SessionReport) HitRate() (rate float64, ok bool) {
	if s.TotalInputTokens == 0 {
		return 0, false
	}
	return float64(s.CacheReadTokens) / float64(s.TotalInputTokens), true
}

// CachedPerRequest returns the average cache-read tokens per request this
// session. Zero requests returns (0, false).
func (s SessionReport) CachedPerRequest() (float64, bool) {
	return perRequest(int(s.CacheReadTokens), int(s.Requests))
}

// UncachedPerRequest returns the average per-request tokens that did not
// come from a cache read (i.e. TotalInputTokens minus CacheReadTokens, which
// folds cache-create tokens in with genuinely uncached ones — the same
// grouping HitRate and Row.UncachedPerRequest use, so CachedPerRequest /
// (CachedPerRequest + UncachedPerRequest) reproduces HitRate here too).
// Zero requests returns (0, false).
func (s SessionReport) UncachedPerRequest() (float64, bool) {
	return perRequest(int(s.TotalInputTokens-s.CacheReadTokens), int(s.Requests))
}

// HitRate computes cacheRead / (input + cacheRead + cacheCreate).
// Returns (0, false) when total is zero to avoid division by zero.
func HitRate(cacheRead, input, cacheCreate int) (float64, bool) {
	total := input + cacheRead + cacheCreate
	if total == 0 {
		return 0, false
	}
	return float64(cacheRead) / float64(total), true
}
