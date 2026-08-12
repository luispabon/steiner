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
}

// HitRate returns the token-weighted cache hit rate for the session.
// When TotalInputTokens is zero, ok is false and rate is 0; callers should render "—".
func (s SessionReport) HitRate() (rate float64, ok bool) {
	if s.TotalInputTokens == 0 {
		return 0, false
	}
	return float64(s.CacheReadTokens) / float64(s.TotalInputTokens), true
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
