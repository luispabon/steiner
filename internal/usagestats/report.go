package usagestats

import "time"

// Totals holds additive token counts, cost, and a request count for one
// aggregation group. Every field is a sum over the group's requests.
type Totals struct {
	// Requests is the total number of API calls in the group.
	Requests int
	// InputTokens is the non-cached portion of prompt tokens.
	InputTokens int
	// CacheReadTokens is the number of tokens served from the prompt cache.
	CacheReadTokens int
	// CacheCreateTokens is the number of tokens written into the prompt cache.
	CacheCreateTokens int
	// CompletionTokens is the number of output tokens generated.
	CompletionTokens int
	// ReasoningTokens is the part of CompletionTokens spent on reasoning. It is
	// a subset of CompletionTokens, never an addition to it.
	ReasoningTokens int
	// CacheReportedRequests counts the group's requests whose provider reported
	// cache data, even as zero tokens.
	CacheReportedRequests int
	// CacheReportedInputTokens sums the prompt-side input total (uncached plus
	// cache read plus cache create) over those requests. It is HitRate's
	// denominator, so requests whose provider said nothing about caching stay
	// out of it.
	CacheReportedInputTokens int
	// CostMicroUSD is the group's summed cost, rounded per call to the nearest
	// micro-USD.
	CostMicroUSD int64
	// ReportedCostRequests counts requests whose cost the provider reported.
	ReportedCostRequests int
	// UnpricedRequests counts requests with neither reported nor estimated cost.
	UnpricedRequests int
}

// Total returns the sum of all token fields: uncached input, cache reads,
// cache creates, and completion tokens.
func (t Totals) Total() int {
	return t.InputTokens + t.CacheReadTokens + t.CacheCreateTokens + t.CompletionTokens
}

// InputTotal returns the prompt-side token count: uncached input plus cache
// reads plus cache creates. HitRate's denominator is CacheReportedInputTokens,
// which sums this over the requests whose provider reported cache data.
func (t Totals) InputTotal() int {
	return t.InputTokens + t.CacheReadTokens + t.CacheCreateTokens
}

// HitRate returns the token-weighted cache hit rate over the requests whose
// provider reported cache data, defined as CacheReadTokens /
// CacheReportedInputTokens. When nothing reported cache data, ok is false and
// rate is 0; callers should render the no-data placeholder.
func (t Totals) HitRate() (rate float64, ok bool) {
	if t.CacheReportedInputTokens == 0 {
		return 0, false
	}
	return float64(t.CacheReadTokens) / float64(t.CacheReportedInputTokens), true
}

// UncachedPerRequest returns the average per-request tokens that did not
// come from a cache read (non-cached input plus cache-create tokens, the
// same grouping HitRate treats as "not a hit"). A single ratio can rise
// either because uncached tokens grew or because cached tokens shrank; this
// and CachedPerRequest decompose that ambiguity, which HitRate alone cannot.
// Both span every request in the group while HitRate covers only the requests
// whose provider reported cache data, so CachedPerRequest / (CachedPerRequest +
// UncachedPerRequest) reproduces HitRate only when every request reported.
// Zero requests returns (0, false).
func (t Totals) UncachedPerRequest() (float64, bool) {
	return perRequest(t.InputTokens+t.CacheCreateTokens, t.Requests)
}

// CachedPerRequest returns the average cache-read prompt tokens per
// request. Zero requests returns (0, false).
func (t Totals) CachedPerRequest() (float64, bool) {
	return perRequest(t.CacheReadTokens, t.Requests)
}

// SourceTotals is one call surface's share of an aggregation group.
type SourceTotals struct {
	// Source identifies the call surface that produced this traffic.
	Source Source
	// Totals is that surface's summed token usage and request count.
	Totals
}

// Row holds one aggregation group's totals. Report rows group by
// (ProviderAlias, ProviderType, BackendModelID) regardless of call surface;
// the group's per-source split is in Sources.
type Row struct {
	// ProviderAlias is the user-configured alias for the provider.
	ProviderAlias string
	// ProviderType is the provider driver type (e.g. "openai", "anthropic").
	ProviderType string
	// BackendModelID is the model identifier as reported by the backend.
	BackendModelID string
	// Totals is the group's usage summed across every call surface.
	Totals
	// Sources breaks the group's usage down by call surface, ordered parent,
	// sub-agent, advisor, unknown. Sources with no traffic in the queried
	// range are omitted.
	Sources []SourceTotals
}

func perRequest(tokens, requests int) (float64, bool) {
	if requests == 0 {
		return 0, false
	}
	return float64(tokens) / float64(requests), true
}

// Report is the result of a window or session aggregation, containing one Row
// per (provider, providerType, model) combination.
type Report struct {
	// Rows contains one entry per model group, sorted by provider alias, then
	// model identifier.
	Rows []Row
	// Total is the sum of every row.
	Total Totals
	// Models is the number of distinct BackendModelID values across rows.
	Models int
	// Providers is the number of distinct ProviderAlias values across rows.
	Providers int
}

// PeriodTotals is one UTC day or week of aggregated usage. Daily and Weekly
// reports return these in ascending Start order, zero-filled for periods with
// no recorded data.
type PeriodTotals struct {
	// Start is the period's inclusive start: midnight UTC for a day, Monday
	// 00:00 UTC for a week.
	Start time.Time
	// Totals is the period's summed token usage and request count.
	Totals
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
	// ReasoningTokens is the part of this session's completion tokens spent on
	// reasoning. It is a subset of them, never an addition.
	ReasoningTokens int64
	// CacheReportedRequests counts the session's requests whose provider
	// reported cache data.
	CacheReportedRequests int64
	// CacheReportedInputTokens sums the prompt-side input total over those
	// requests. It is HitRate's denominator.
	CacheReportedInputTokens int64
	// CostMicroUSD is the session's summed cost in micro-USD.
	CostMicroUSD int64
	// ReportedCostRequests counts requests whose cost the provider reported.
	ReportedCostRequests int64
	// UnpricedRequests counts requests with neither reported nor estimated cost.
	UnpricedRequests int64
	// LastCacheReadTokens and LastTotalInputTokens describe the latest request
	// for a source-scoped report; blended reports leave them zero.
	LastCacheReadTokens  int64
	LastTotalInputTokens int64
	// LastCacheReportedInputTokens is the latest request's reported-cache input
	// total. It is zero when that request's provider reported no cache data.
	LastCacheReportedInputTokens int64
	// LastCallKind is the kind of the latest request for a source-scoped
	// report. Blended reports leave it at the zero value, CallKindNormal.
	LastCallKind CallKind
}

// addCumulative sums c's process-lifetime counters into s, leaving the
// last-request fields alone.
func (s *SessionReport) addCumulative(c sessionCounters) {
	s.CacheReadTokens += c.cacheReadTokens
	s.TotalInputTokens += c.inputTokens
	s.Requests += c.requests
	s.ReasoningTokens += c.reasoningTokens
	s.CacheReportedRequests += c.cacheReportedRequests
	s.CacheReportedInputTokens += c.cacheReportedInputTokens
	s.CostMicroUSD += c.costMicroUSD
	s.ReportedCostRequests += c.reportedCostRequests
	s.UnpricedRequests += c.unpricedRequests
}

// LastHitRate returns the latest request's hit rate, over its reported-cache
// input total. When that request's provider reported no cache data, ok is
// false and rate is 0.
func (s SessionReport) LastHitRate() (rate float64, ok bool) {
	if s.LastCacheReportedInputTokens == 0 {
		return 0, false
	}
	return float64(s.LastCacheReadTokens) / float64(s.LastCacheReportedInputTokens), true
}

// HitRate returns the token-weighted cache hit rate for the session, over the
// requests whose provider reported cache data. When none did,
// CacheReportedInputTokens is zero, ok is false and rate is 0; callers should
// render the no-data placeholder.
func (s SessionReport) HitRate() (rate float64, ok bool) {
	if s.CacheReportedInputTokens == 0 {
		return 0, false
	}
	return float64(s.CacheReadTokens) / float64(s.CacheReportedInputTokens), true
}

// CachedPerRequest returns the average cache-read tokens per request this
// session. Zero requests returns (0, false).
func (s SessionReport) CachedPerRequest() (float64, bool) {
	return perRequest(int(s.CacheReadTokens), int(s.Requests))
}

// UncachedPerRequest returns the average per-request tokens that did not
// come from a cache read (i.e. TotalInputTokens minus CacheReadTokens, which
// folds cache-create tokens in with genuinely uncached ones, the same
// grouping HitRate and Row.UncachedPerRequest use). It spans every request
// while HitRate covers only those whose provider reported cache data, so the
// per-request ratio reproduces HitRate only when every request reported.
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
