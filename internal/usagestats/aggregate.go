package usagestats

import (
	"cmp"
	"slices"
	"time"
)

// modelKey identifies one report row: a model served by one provider
// configuration. Source is deliberately absent; a row merges every call
// surface and breaks the result down in Row.Sources.
type modelKey struct {
	providerAlias  string
	providerType   string
	backendModelID string
}

// modelAgg accumulates one modelKey's totals and its per-source split.
type modelAgg struct {
	totals  Totals
	sources map[Source]Totals
}

// addBucket adds one stored bucket to t.
func (t *Totals) addBucket(b *bucket) {
	t.Requests += b.Requests
	t.InputTokens += b.InputTokens
	t.CacheReadTokens += b.CacheReadTokens
	t.CacheCreateTokens += b.CacheCreateTokens
	t.CompletionTokens += b.CompletionTokens
	t.ReasoningTokens += b.ReasoningTokens
	t.CacheReportedRequests += b.CacheReportedRequests
	t.CacheReportedInputTokens += b.CacheReportedInputTokens
	t.CostMicroUSD += b.CostMicroUSD
	t.ReportedCostRequests += b.ReportedCostRequests
	t.UnpricedRequests += b.UnpricedRequests
}

// merge adds o to t.
func (t *Totals) merge(o Totals) {
	t.Requests += o.Requests
	t.InputTokens += o.InputTokens
	t.CacheReadTokens += o.CacheReadTokens
	t.CacheCreateTokens += o.CacheCreateTokens
	t.CompletionTokens += o.CompletionTokens
	t.ReasoningTokens += o.ReasoningTokens
	t.CacheReportedRequests += o.CacheReportedRequests
	t.CacheReportedInputTokens += o.CacheReportedInputTokens
	t.CostMicroUSD += o.CostMicroUSD
	t.ReportedCostRequests += o.ReportedCostRequests
	t.UnpricedRequests += o.UnpricedRequests
}

// Window sums all buckets whose hour intersects the interval [now-d, now] and
// returns a Report with one Row per (ProviderAlias, ProviderType, BackendModelID)
// group. It uses hour-granularity buckets and may include up to one hour of
// extra data at the lower boundary (e.g. a 1-hour query at 10:30 includes all
// of the 09:00 bucket, not just data from 09:30-10:30).
func (r *Recorder) Window(d time.Duration) Report {
	return r.WindowSince(r.now().UTC().Add(-d))
}

// WindowSince sums every bucket from the hour containing from through the
// recorder's current time, in the same shape as Window. Buckets are hourly, so
// data from the hour containing from is included in full even when from falls
// later in that hour.
func (r *Recorder) WindowSince(from time.Time) Report {
	return r.aggregateRange(from.UTC().Truncate(time.Hour), r.now().UTC())
}

// BySource sums every bucket from the hour containing from through the
// recorder's current time, per call surface. Results are ordered parent,
// sub-agent, advisor, unknown; surfaces with no traffic in the range are
// omitted. Data from the hour containing from is included in full, as in
// Window.
func (r *Recorder) BySource(from time.Time) []SourceTotals {
	lo := from.UTC().Truncate(time.Hour)
	hi := r.now().UTC()
	bySource := make(map[Source]Totals)

	r.mu.Lock()
	defer r.mu.Unlock()
	for k, b := range r.buckets {
		t := time.Unix(k.hourUnix, 0).UTC()
		if t.Before(lo) || t.After(hi) {
			continue
		}
		totals := bySource[k.source]
		totals.addBucket(b)
		bySource[k.source] = totals
	}
	return sortedSourceTotals(bySource)
}

// Daily returns one PeriodTotals per UTC day from the day containing from
// through the day containing to, inclusive, in ascending order and
// zero-filled. Days are summed from hourly buckets, so the first and last days
// are included in full: up to one hour of data before from or after to can be
// counted. A to earlier than from returns nil.
func (r *Recorder) Daily(from, to time.Time) []PeriodTotals {
	return r.periods(dayStart(from), dayStart(to), 1)
}

// Weekly returns one PeriodTotals per Monday-to-Sunday UTC week from the week
// containing from through the week containing to, inclusive, in ascending
// order and zero-filled. Start is the week's Monday 00:00 UTC. Weeks are
// summed from hourly buckets, so the first and last weeks are included in full:
// up to one hour of data before from or after to can be counted. A to earlier
// than from returns nil.
func (r *Recorder) Weekly(from, to time.Time) []PeriodTotals {
	return r.periods(weekStart(from), weekStart(to), 7)
}

// SessionWindow returns this process's usage as a Report shaped exactly like a
// window query: one Row per model, with the per-source split in Row.Sources.
// It reads process-lifetime counters, so it ignores buckets loaded from earlier
// runs, and ResetSession clears it.
func (r *Recorder) SessionWindow() Report {
	groups := make(map[modelKey]*modelAgg)

	r.mu.Lock()
	defer r.mu.Unlock()
	for k, b := range r.sessionModels {
		mk := modelKey{
			providerAlias:  k.providerAlias,
			providerType:   k.providerType,
			backendModelID: k.backendModelID,
		}
		aggregateInto(groups, mk, k.source, b)
	}
	return buildReport(groups)
}

// MonthAgo returns the same day-of-month one calendar month earlier in UTC at
// the same time of day, clamped to the last day of that month: Mar 31 becomes
// Feb 28 (Feb 29 in a leap year) and Jan 31 becomes Dec 31. AddDate
// normalisation is not used because it would roll Mar 31 into March.
func MonthAgo(now time.Time) time.Time {
	now = now.UTC()
	y, m, d := now.Date()
	firstOfPrev := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	if last := firstOfPrev.AddDate(0, 1, -1).Day(); d > last {
		d = last
	}
	return time.Date(firstOfPrev.Year(), firstOfPrev.Month(), d,
		now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.UTC)
}

// aggregateRange builds a Report from every bucket whose hour is in [lo, hi].
func (r *Recorder) aggregateRange(lo, hi time.Time) Report {
	groups := make(map[modelKey]*modelAgg)

	r.mu.Lock()
	defer r.mu.Unlock()
	for k, b := range r.buckets {
		t := time.Unix(k.hourUnix, 0).UTC()
		if t.Before(lo) || t.After(hi) {
			continue
		}
		mk := modelKey{
			providerAlias:  k.providerAlias,
			providerType:   k.providerType,
			backendModelID: k.backendModelID,
		}
		aggregateInto(groups, mk, k.source, b)
	}
	return buildReport(groups)
}

// periods returns one zero-filled PeriodTotals per period from the period
// starting at start through the one starting at end, spaced stepDays apart.
// Each bucket lands in the period containing its hour.
func (r *Recorder) periods(start, end time.Time, stepDays int) []PeriodTotals {
	if end.Before(start) {
		return nil
	}
	step := time.Duration(stepDays) * 24 * time.Hour
	n := int(end.Sub(start)/step) + 1
	out := make([]PeriodTotals, n)
	for i := range out {
		out[i].Start = start.AddDate(0, 0, i*stepDays)
	}
	limit := start.AddDate(0, 0, n*stepDays)

	r.mu.Lock()
	defer r.mu.Unlock()
	for k, b := range r.buckets {
		t := time.Unix(k.hourUnix, 0).UTC()
		if t.Before(start) || !t.Before(limit) {
			continue
		}
		out[int(t.Sub(start)/step)].addBucket(b)
	}
	return out
}

// aggregateInto merges bucket b, recorded for source, into the model group.
func aggregateInto(groups map[modelKey]*modelAgg, mk modelKey, source Source, b *bucket) {
	g, ok := groups[mk]
	if !ok {
		g = &modelAgg{sources: make(map[Source]Totals)}
		groups[mk] = g
	}
	g.totals.addBucket(b)
	totals := g.sources[source]
	totals.addBucket(b)
	g.sources[source] = totals
}

// buildReport turns aggregated model groups into a Report sorted by provider
// alias, then model, then provider type.
func buildReport(groups map[modelKey]*modelAgg) Report {
	rows := make([]Row, 0, len(groups))
	models := make(map[string]struct{}, len(groups))
	providers := make(map[string]struct{}, len(groups))
	var total Totals

	for mk, g := range groups {
		rows = append(rows, Row{
			ProviderAlias:  mk.providerAlias,
			ProviderType:   mk.providerType,
			BackendModelID: mk.backendModelID,
			Totals:         g.totals,
			Sources:        sortedSourceTotals(g.sources),
		})
		total.merge(g.totals)
		models[mk.backendModelID] = struct{}{}
		providers[mk.providerAlias] = struct{}{}
	}

	slices.SortFunc(rows, func(a, b Row) int {
		if c := cmp.Compare(a.ProviderAlias, b.ProviderAlias); c != 0 {
			return c
		}
		if c := cmp.Compare(a.BackendModelID, b.BackendModelID); c != 0 {
			return c
		}
		return cmp.Compare(a.ProviderType, b.ProviderType)
	})

	return Report{Rows: rows, Total: total, Models: len(models), Providers: len(providers)}
}

// sortedSourceTotals orders a per-source map by Source value: parent,
// sub-agent, advisor, unknown.
func sortedSourceTotals(bySource map[Source]Totals) []SourceTotals {
	if len(bySource) == 0 {
		return nil
	}
	out := make([]SourceTotals, 0, len(bySource))
	for source, totals := range bySource {
		out = append(out, SourceTotals{Source: source, Totals: totals})
	}
	slices.SortFunc(out, func(a, b SourceTotals) int { return cmp.Compare(a.Source, b.Source) })
	return out
}

// dayStart returns the midnight UTC that starts t's UTC day.
func dayStart(t time.Time) time.Time {
	return t.UTC().Truncate(24 * time.Hour)
}

// weekStart returns the Monday 00:00 UTC that starts t's UTC week.
func weekStart(t time.Time) time.Time {
	d := dayStart(t)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}
