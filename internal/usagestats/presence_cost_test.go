package usagestats

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// presenceFixtureHour is the hourly bucket every store fixture uses.
const presenceFixtureHour = int64(1718928000) // 2024-06-21T00:00:00Z

func presenceFixtureClock() func() time.Time {
	return fixedClock(time.Unix(presenceFixtureHour+3600, 0))
}

func floatPtr(v float64) *float64 { return &v }

// TestStoreDecodeLegacyPresence covers the schema 3 backfill rule: a file
// written before schema 3 carries no cost data and no cache-presence flag, so
// every request is unpriced and cache traffic is only claimed when the stored
// token sums prove the provider reported some.
func TestStoreDecodeLegacyPresence(t *testing.T) {
	cacheKey := func(source Source) bucketKey {
		return bucketKey{providerAlias: "anthropic", providerType: "anthropic", backendModelID: "m-cache", hourUnix: presenceFixtureHour, source: source}
	}
	plainKey := func(source Source) bucketKey {
		return bucketKey{providerAlias: "openai", providerType: "openai", backendModelID: "m-plain", hourUnix: presenceFixtureHour, source: source}
	}
	legacyBuckets := func(cacheSource, plainSource Source) map[bucketKey]*bucket {
		return map[bucketKey]*bucket{
			cacheKey(cacheSource): {
				Requests: 5, InputTokens: 1200, CacheReadTokens: 450, CacheCreateTokens: 100, CompletionTokens: 500,
				CacheReportedRequests: 5, CacheReportedInputTokens: 1750, UnpricedRequests: 5,
			},
			plainKey(plainSource): {
				Requests: 3, InputTokens: 800, CompletionTokens: 300,
				UnpricedRequests: 3,
			},
		}
	}

	tests := []struct {
		name    string
		fixture string
		want    map[bucketKey]*bucket
	}{
		{
			name:    "schema 1 backfills and keeps source unknown",
			fixture: "cache-stats-v1.json",
			want:    legacyBuckets(SourceUnknown, SourceUnknown),
		},
		{
			name:    "schema 2 keeps sources and backfills presence",
			fixture: "cache-stats-v2.json",
			want:    legacyBuckets(SourceParent, SourceAdvisor),
		},
		{
			name:    "schema 3 values are preserved without backfill",
			fixture: "cache-stats-v3.json",
			want: map[bucketKey]*bucket{
				cacheKey(SourceParent): {
					Requests: 2, InputTokens: 100, CacheReadTokens: 40, CacheCreateTokens: 10, CompletionTokens: 80,
					ReasoningTokens: 30, CacheReportedRequests: 2, CacheReportedInputTokens: 150,
					CostMicroUSD: 123456, ReportedCostRequests: 1, UnpricedRequests: 1,
				},
				plainKey(SourceSubAgent): {
					Requests: 4, InputTokens: 900, CompletionTokens: 50,
					CacheReportedRequests: 4, CacheReportedInputTokens: 900, CostMicroUSD: 5,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tc.fixture))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			s := newStore(presenceFixtureClock())
			got, err := s.decode(data)
			if err != nil {
				t.Fatalf("decode %s: %v", tc.fixture, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("decode %s = %+v, want %+v", tc.fixture, got, tc.want)
			}
		})
	}
}

// TestLegacyBucketHitRate proves a decoded schema 1 or 2 bucket feeds the
// CacheReportedInputTokens rule: reported cache traffic produces a rate, an
// entry that never proved cache reporting produces no rate at all.
func TestLegacyBucketHitRate(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "cache-stats-v2.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	buckets, err := newStore(presenceFixtureClock()).decode(data)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	tests := []struct {
		name     string
		key      bucketKey
		wantRate float64
		wantOK   bool
	}{
		{
			name:     "cache traffic reported",
			key:      bucketKey{providerAlias: "anthropic", providerType: "anthropic", backendModelID: "m-cache", hourUnix: presenceFixtureHour, source: SourceParent},
			wantRate: 450.0 / 1750.0,
			wantOK:   true,
		},
		{
			name: "no cache traffic to prove reporting",
			key:  bucketKey{providerAlias: "openai", providerType: "openai", backendModelID: "m-plain", hourUnix: presenceFixtureHour, source: SourceAdvisor},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var totals Totals
			totals.addBucket(buckets[tc.key])
			rate, ok := totals.HitRate()
			if ok != tc.wantOK {
				t.Errorf("HitRate ok = %v, want %v", ok, tc.wantOK)
			}
			if rate != tc.wantRate {
				t.Errorf("HitRate rate = %v, want %v", rate, tc.wantRate)
			}
		})
	}
}

// TestStoreWriteBatchMergesPresenceFields proves the writeBatch read/merge/write
// cycle sums every schema 3 field instead of dropping the ones already on disk.
func TestStoreWriteBatchMergesPresenceFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "merge-presence.json")

	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	now := time.Unix(presenceFixtureHour+3600, 0)
	clock := newMockClock(now)
	key := bucketKey{providerAlias: "p1", providerType: "t1", backendModelID: "m1", hourUnix: presenceFixtureHour}

	s := newStore(clock.Now)
	first := &bucket{
		Requests: 1, InputTokens: 10, CacheReadTokens: 20, CacheCreateTokens: 30, CompletionTokens: 40,
		ReasoningTokens: 5, CacheReportedRequests: 1, CacheReportedInputTokens: 60,
		CostMicroUSD: 1000, ReportedCostRequests: 1, UnpricedRequests: 0,
	}
	if err := s.writeBatch(map[bucketKey]*bucket{key: first}); err != nil {
		t.Fatalf("first write: %v", err)
	}

	second := &bucket{
		Requests: 2, InputTokens: 1, CacheReadTokens: 2, CacheCreateTokens: 3, CompletionTokens: 4,
		ReasoningTokens: 1, CacheReportedRequests: 2, CacheReportedInputTokens: 6,
		CostMicroUSD: 250, ReportedCostRequests: 1, UnpricedRequests: 1,
	}
	if err := s.writeBatch(map[bucketKey]*bucket{key: second}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got := newStore(clock.Now).load()[key]
	want := &bucket{
		Requests: 3, InputTokens: 11, CacheReadTokens: 22, CacheCreateTokens: 33, CompletionTokens: 44,
		ReasoningTokens: 6, CacheReportedRequests: 3, CacheReportedInputTokens: 66,
		CostMicroUSD: 1250, ReportedCostRequests: 2, UnpricedRequests: 1,
	}
	if got == nil {
		t.Fatalf("bucket missing after merge, want %+v", want)
	}
	if !reflect.DeepEqual(*got, *want) {
		t.Errorf("merged bucket = %+v, want %+v", *got, *want)
	}
}

// TestRecordCachePresence covers which observations count as reported cache
// data: the explicit flag and the cache-token inference.
func TestRecordCachePresence(t *testing.T) {
	tests := []struct {
		name              string
		obs               Observation
		wantInput         int
		wantReported      int
		wantReportedInput int
	}{
		{
			name:      "explicit flag with zero cache tokens",
			obs:       Observation{PromptTokens: 100, CompletionTokens: 10, CacheReported: true},
			wantInput: 100, wantReported: 1, wantReportedInput: 100,
		},
		{
			name:      "inferred from cache read tokens",
			obs:       Observation{PromptTokens: 100, CacheReadTokens: 60, CompletionTokens: 20},
			wantInput: 40, wantReported: 1, wantReportedInput: 100,
		},
		{
			name:      "inferred from cache create tokens",
			obs:       Observation{PromptTokens: 100, CacheCreateTokens: 30},
			wantInput: 70, wantReported: 1, wantReportedInput: 100,
		},
		{
			name:      "floored non-cached input",
			obs:       Observation{PromptTokens: 50, CacheReadTokens: 40, CacheCreateTokens: 30},
			wantInput: 0, wantReported: 1, wantReportedInput: 70,
		},
		{
			name:      "no cache data at all",
			obs:       Observation{PromptTokens: 100},
			wantInput: 100,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateTest(t)
			r := newTestRecorder(t, fixedClock(baseTime))
			tc.obs.At = baseTime
			r.Record(tc.obs)

			b := r.buckets[bucketKey{hourUnix: hourStart.Unix()}]
			if b == nil {
				t.Fatal("expected bucket to exist")
			}
			if b.InputTokens != tc.wantInput {
				t.Errorf("InputTokens = %d, want %d", b.InputTokens, tc.wantInput)
			}
			if b.CacheReportedRequests != tc.wantReported {
				t.Errorf("CacheReportedRequests = %d, want %d", b.CacheReportedRequests, tc.wantReported)
			}
			if b.CacheReportedInputTokens != tc.wantReportedInput {
				t.Errorf("CacheReportedInputTokens = %d, want %d", b.CacheReportedInputTokens, tc.wantReportedInput)
			}
		})
	}
}

// TestRecordReasoningClamp pins the reasoning subset clamp: reasoning tokens
// never leave [0, CompletionTokens] because they are a subset of it.
func TestRecordReasoningClamp(t *testing.T) {
	tests := []struct {
		name            string
		reasoningTokens int
		completion      int
		want            int
	}{
		{name: "within range", reasoningTokens: 40, completion: 100, want: 40},
		{name: "negative floors to zero", reasoningTokens: -5, completion: 100, want: 0},
		{name: "above completion clamps to completion", reasoningTokens: 150, completion: 100, want: 100},
		{name: "zero completion allows none", reasoningTokens: 10, completion: 0, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateTest(t)
			r := newTestRecorder(t, fixedClock(baseTime))
			r.Record(Observation{CompletionTokens: tc.completion, ReasoningTokens: tc.reasoningTokens, At: baseTime})

			b := r.buckets[bucketKey{hourUnix: hourStart.Unix()}]
			if b == nil {
				t.Fatal("expected bucket to exist")
			}
			if b.ReasoningTokens != tc.want {
				t.Errorf("ReasoningTokens = %d, want %d", b.ReasoningTokens, tc.want)
			}
		})
	}
}

// TestRecordCostRounding covers the reported-cost path: rounding to the nearest
// micro-USD, the negative clamp, and unpriced requests.
func TestRecordCostRounding(t *testing.T) {
	tests := []struct {
		name         string
		cost         *float64
		wantMicro    int64
		wantReported int
		wantUnpriced int
	}{
		{name: "no reported cost is unpriced", wantUnpriced: 1},
		{name: "below half a micro rounds to zero", cost: floatPtr(0.0000004), wantMicro: 0, wantReported: 1},
		{name: "half a micro rounds up", cost: floatPtr(0.0000005), wantMicro: 1, wantReported: 1},
		{name: "six decimals round exactly", cost: floatPtr(0.123456), wantMicro: 123456, wantReported: 1},
		{name: "negative clamps to zero but stays reported", cost: floatPtr(-1), wantMicro: 0, wantReported: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateTest(t)
			r := newTestRecorder(t, fixedClock(baseTime))
			r.Record(Observation{PromptTokens: 10, ReportedCostUSD: tc.cost, At: baseTime})

			b := r.buckets[bucketKey{hourUnix: hourStart.Unix()}]
			if b == nil {
				t.Fatal("expected bucket to exist")
			}
			if b.CostMicroUSD != tc.wantMicro {
				t.Errorf("CostMicroUSD = %d, want %d", b.CostMicroUSD, tc.wantMicro)
			}
			if b.ReportedCostRequests != tc.wantReported {
				t.Errorf("ReportedCostRequests = %d, want %d", b.ReportedCostRequests, tc.wantReported)
			}
			if b.UnpricedRequests != tc.wantUnpriced {
				t.Errorf("UnpricedRequests = %d, want %d", b.UnpricedRequests, tc.wantUnpriced)
			}
		})
	}
}

// TestTotalsHitRateMixedReporting proves the denominator only counts requests
// whose provider reported cache data, so an unreported request cannot drag the
// rate down or make an otherwise undefined rate look defined.
func TestTotalsHitRateMixedReporting(t *testing.T) {
	tests := []struct {
		name         string
		observations []Observation
		wantRate     float64
		wantOK       bool
	}{
		{
			name: "all reporting",
			observations: []Observation{
				{PromptTokens: 100, CacheReadTokens: 50},
				{PromptTokens: 100, CacheReadTokens: 100},
			},
			wantRate: 0.75,
			wantOK:   true,
		},
		{
			name: "none reporting",
			observations: []Observation{
				{PromptTokens: 100},
				{PromptTokens: 900},
			},
			wantOK: false,
		},
		{
			name: "mixed reporting ignores the unreported request",
			observations: []Observation{
				{PromptTokens: 100, CacheReadTokens: 80},
				{PromptTokens: 900},
			},
			wantRate: 0.8,
			wantOK:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateTest(t)
			r := newTestRecorder(t, fixedClock(baseTime))
			for _, obs := range tc.observations {
				obs.At = baseTime
				r.Record(obs)
			}
			total := r.Window(time.Hour).Total
			rate, ok := total.HitRate()
			if ok != tc.wantOK {
				t.Errorf("HitRate ok = %v, want %v (total %+v)", ok, tc.wantOK, total)
			}
			if rate != tc.wantRate {
				t.Errorf("HitRate rate = %v, want %v (total %+v)", rate, tc.wantRate, total)
			}
		})
	}
}

// TestAggregationCarriesPresenceAndCostFields records one observation with every
// schema 3 field set and checks each aggregation surface reports all of them.
func TestAggregationCarriesPresenceAndCostFields(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.Record(Observation{
		ProviderAlias: "p", ProviderType: "t", BackendModelID: "m",
		PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 50, CacheCreateTokens: 10,
		ReasoningTokens: 25, CacheReported: true, ReportedCostUSD: floatPtr(0.5),
		Source: SourceParent, At: baseTime,
	})

	want := Totals{
		Requests: 1, InputTokens: 40, CacheReadTokens: 50, CacheCreateTokens: 10, CompletionTokens: 40,
		ReasoningTokens: 25, CacheReportedRequests: 1, CacheReportedInputTokens: 100,
		CostMicroUSD: 500000, ReportedCostRequests: 1,
	}

	report := r.Window(time.Hour)
	if len(report.Rows) != 1 {
		t.Fatalf("Window rows = %d, want 1", len(report.Rows))
	}
	if report.Rows[0].Totals != want {
		t.Errorf("Window row = %+v, want %+v", report.Rows[0].Totals, want)
	}
	if report.Total != want {
		t.Errorf("Window total = %+v, want %+v", report.Total, want)
	}

	bySource := r.BySource(baseTime)
	if len(bySource) != 1 || bySource[0].Totals != want {
		t.Errorf("BySource = %+v, want one source with %+v", bySource, want)
	}

	for _, period := range []struct {
		name string
		got  []PeriodTotals
	}{
		{"Daily", r.Daily(baseTime, baseTime)},
		{"Weekly", r.Weekly(baseTime, baseTime)},
	} {
		if len(period.got) != 1 {
			t.Fatalf("%s periods = %d, want 1", period.name, len(period.got))
		}
		if period.got[0].Totals != want {
			t.Errorf("%s totals = %+v, want %+v", period.name, period.got[0].Totals, want)
		}
	}

	session := r.SessionWindow()
	if len(session.Rows) != 1 || session.Rows[0].Totals != want {
		t.Fatalf("SessionWindow rows = %+v, want one row with %+v", session.Rows, want)
	}
	if len(session.Rows[0].Sources) != 1 || session.Rows[0].Sources[0].Totals != want {
		t.Errorf("SessionWindow sources = %+v, want one source with %+v", session.Rows[0].Sources, want)
	}

	sr := r.SessionReportFor(SourceParent)
	if sr.CacheReadTokens != 50 || sr.TotalInputTokens != 100 || sr.Requests != 1 {
		t.Errorf("session report totals = %+v, want 50 cache read, 100 input, 1 request", sr)
	}
	if sr.ReasoningTokens != 25 || sr.CacheReportedRequests != 1 || sr.CacheReportedInputTokens != 100 {
		t.Errorf("session report presence = %+v, want reasoning 25, one reported request, 100 reported input", sr)
	}
	if sr.CostMicroUSD != 500000 || sr.ReportedCostRequests != 1 || sr.UnpricedRequests != 0 {
		t.Errorf("session report cost = %+v, want 500000 micro-USD, one reported cost, no unpriced", sr)
	}
	if rate, ok := sr.HitRate(); !ok || rate != 0.5 {
		t.Errorf("session HitRate = %v, %v; want 0.5, true", rate, ok)
	}
	if rate, ok := sr.LastHitRate(); !ok || rate != 0.5 {
		t.Errorf("session LastHitRate = %v, %v; want 0.5, true", rate, ok)
	}

	blended := r.SessionReport()
	if blended.CostMicroUSD != 500000 || blended.ReasoningTokens != 25 || blended.CacheReportedInputTokens != 100 {
		t.Errorf("blended session report = %+v, want the same presence and cost fields", blended)
	}
}

// TestRecordPersistRoundTripPresenceFields proves the new fields survive the
// recorder's asynchronous store write and a fresh load.
func TestRecordPersistRoundTripPresenceFields(t *testing.T) {
	isolateTest(t)
	t.Setenv(TelemetryEnvVar, "")
	path := filepath.Join(t.TempDir(), "roundtrip-presence.json")
	oldPath := storePath
	defer func() { storePath = oldPath }()
	storePath = func() string { return path }

	now := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
	rec := newTestRecorder(t, fixedClock(now))
	rec.Record(Observation{
		ProviderAlias: "p", ProviderType: "openai", BackendModelID: "m", Source: SourceParent,
		PromptTokens: 100, CompletionTokens: 40, CacheReadTokens: 20, CacheCreateTokens: 10,
		ReasoningTokens: 15, CacheReported: true, ReportedCostUSD: floatPtr(0.123456), At: now,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rec.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	key := bucketKey{providerAlias: "p", providerType: "openai", backendModelID: "m", hourUnix: now.Unix(), source: SourceParent}
	got := newStore(fixedClock(now)).load()[key]
	want := &bucket{
		Requests: 1, InputTokens: 70, CacheReadTokens: 20, CacheCreateTokens: 10, CompletionTokens: 40,
		ReasoningTokens: 15, CacheReportedRequests: 1, CacheReportedInputTokens: 100,
		CostMicroUSD: 123456, ReportedCostRequests: 1,
	}
	if got == nil {
		t.Fatalf("bucket missing after persist, want %+v", want)
	}
	if !reflect.DeepEqual(*got, *want) {
		t.Errorf("persisted bucket = %+v, want %+v", *got, *want)
	}
}

// TestTelemetryPresenceAndCostFields checks the new telemetry line fields and
// that an unpriced call omits the cost field entirely.
func TestTelemetryPresenceAndCostFields(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "telemetry-presence.jsonl")
	t.Setenv(TelemetryEnvVar, path)
	t.Setenv(TelemetryRunEnvVar, "")

	rec := newTestRecorder(t, fixedClock(baseTime))
	rec.Record(Observation{
		ProviderAlias: "p", PromptTokens: 100, CompletionTokens: 40,
		ReasoningTokens: 7, CacheReported: true, ReportedCostUSD: floatPtr(0.000002), At: baseTime,
	})
	rec.Record(Observation{ProviderAlias: "p", PromptTokens: 10, At: baseTime})

	lines := readTelemetryLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d telemetry lines, want 2", len(lines))
	}
	if lines[0].ReasoningTokens != 7 {
		t.Errorf("reasoning_tokens = %d, want 7", lines[0].ReasoningTokens)
	}
	if !lines[0].CacheReported {
		t.Error("cache_reported = false, want true")
	}
	if lines[0].CostMicroUSD != 2 {
		t.Errorf("cost_micro_usd = %d, want 2", lines[0].CostMicroUSD)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry file: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var raw []map[string]any
	for decoder.More() {
		var line map[string]any
		if err := decoder.Decode(&line); err != nil {
			t.Fatalf("decode telemetry line: %v", err)
		}
		raw = append(raw, line)
	}
	if len(raw) != 2 {
		t.Fatalf("decoded %d raw lines, want 2", len(raw))
	}
	for _, field := range []string{"reasoning_tokens", "cache_reported", "cost_micro_usd"} {
		if _, ok := raw[1][field]; ok {
			t.Errorf("second line carried %q = %v, want the field omitted", field, raw[1][field])
		}
	}
}
