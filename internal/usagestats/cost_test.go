package usagestats

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// TestEstimateMicroUSD is the pure estimator's table: base rates, cache-rate
// fallbacks, tier boundaries, field-by-field merging, and rounding.
func TestEstimateMicroUSD(t *testing.T) {
	twoTiers := []RateTier{
		{Size: 1000, Input: floatPtr(2)},
		{Size: 2000, Input: floatPtr(4)},
	}

	tests := []struct {
		name       string
		rates      Rates
		prompt     int
		cacheRead  int
		cacheWrite int
		completion int
		want       int64
	}{
		{
			name:       "base rates only",
			rates:      Rates{Input: 1.25, Output: 10},
			prompt:     1000,
			completion: 100,
			want:       1250 + 1000,
		},
		{
			name:       "cache rates fall back to input",
			rates:      Rates{Input: 2, Output: 3},
			prompt:     100,
			cacheRead:  40,
			cacheWrite: 10,
			completion: 5,
			// uncached 50*2 + read 40*2 + write 10*2 + completion 5*3
			want: 100 + 80 + 20 + 15,
		},
		{
			name:       "explicit cache rates",
			rates:      Rates{Input: 2, Output: 3, CacheRead: floatPtr(0.5), CacheWrite: floatPtr(0.25)},
			prompt:     100,
			cacheRead:  40,
			cacheWrite: 10,
			completion: 5,
			// 50*2 + 40*0.5 + 10*0.25 + 5*3 = 137.5 rounds to 138
			want: 138,
		},
		{
			name:   "tier equal to size is not applied",
			rates:  Rates{Input: 1, Tiers: []RateTier{{Size: 1000, Input: floatPtr(2)}}},
			prompt: 1000,
			want:   1000,
		},
		{
			name:   "tier strictly above size is applied",
			rates:  Rates{Input: 1, Tiers: []RateTier{{Size: 1000, Input: floatPtr(2)}}},
			prompt: 1001,
			want:   2002,
		},
		{
			name:   "below first of two tiers",
			rates:  Rates{Input: 1, Tiers: twoTiers},
			prompt: 500,
			want:   500,
		},
		{
			name:   "between two tiers",
			rates:  Rates{Input: 1, Tiers: twoTiers},
			prompt: 1500,
			want:   3000,
		},
		{
			name:   "above second tier",
			rates:  Rates{Input: 1, Tiers: twoTiers},
			prompt: 2500,
			want:   10000,
		},
		{
			name: "unsorted tiers are sorted before applying",
			rates: Rates{Input: 1, Tiers: []RateTier{
				{Size: 2000, Input: floatPtr(4)},
				{Size: 1000, Input: floatPtr(2)},
			}},
			prompt: 2500,
			want:   10000,
		},
		{
			name: "tier omitting keys inherits base cache rates",
			rates: Rates{
				Input: 1, Output: 2, CacheRead: floatPtr(0.1), CacheWrite: floatPtr(0.2),
				Tiers: []RateTier{{Size: 100, Input: floatPtr(1.5)}},
			},
			prompt: 200, cacheRead: 100, cacheWrite: 50, completion: 10,
			// uncached 50*1.5 + 100*0.1 + 50*0.2 + 10*2
			want: 75 + 10 + 10 + 20,
		},
		{
			name: "tier input carries the cache fallback too",
			rates: Rates{
				Input: 1, Output: 1,
				Tiers: []RateTier{{Size: 100, Input: floatPtr(2)}},
			},
			prompt: 200, cacheRead: 100, cacheWrite: 100,
			// uncached 0 + read 100*2 + write 100*2, both falling back to the
			// tier's input rate
			want: 400,
		},
		{
			name: "larger tier inherits a smaller tier's override",
			rates: Rates{Input: 1, Output: 1, Tiers: []RateTier{
				{Size: 1000, Input: floatPtr(2)},
				{Size: 2000, Output: floatPtr(5)},
			}},
			prompt: 2500, completion: 10,
			// input stays at the 1000 tier's 2, output comes from the 2000 tier
			want: 5000 + 50,
		},
		{
			name:      "cache tokens beyond the prompt floor uncached at zero",
			rates:     Rates{Input: 1, Output: 1, CacheRead: floatPtr(1), CacheWrite: floatPtr(1)},
			prompt:    100,
			cacheRead: 80, cacheWrite: 50,
			want: 80 + 50,
		},
		{
			name:       "negative inputs clamp to zero",
			rates:      Rates{Input: 1, Output: 1},
			prompt:     -100,
			cacheRead:  -50,
			cacheWrite: -20,
			completion: -10,
			want:       0,
		},
		{
			name:   "rounds half a micro up",
			rates:  Rates{Input: 1.5},
			prompt: 1,
			want:   2,
		},
		{
			name:   "rounds below half a micro down",
			rates:  Rates{Input: 0.46376812},
			prompt: 1,
			want:   0,
		},
		{
			name: "gpt-6-luna below its 272000 tier",
			rates: Rates{
				Input: 0.1, Output: 0.5, CacheRead: floatPtr(0.01), CacheWrite: floatPtr(0.125),
				Tiers: []RateTier{{
					Size: 272000, Input: floatPtr(0.2), Output: floatPtr(0.75),
					CacheRead: floatPtr(0.02), CacheWrite: floatPtr(0.25),
				}},
			},
			prompt: 100000, cacheRead: 90000, cacheWrite: 0, completion: 2000,
			// uncached 10000*0.1 = 1000
			// read 90000*0.01 = 900, write 0*0.125 = 0
			// completion 2000*0.5 = 1000
			want: 1000 + 900 + 0 + 1000,
		},
		{
			name: "gpt-6-luna above its 272000 tier",
			rates: Rates{
				Input: 0.1, Output: 0.5, CacheRead: floatPtr(0.01), CacheWrite: floatPtr(0.125),
				Tiers: []RateTier{{
					Size: 272000, Input: floatPtr(0.2), Output: floatPtr(0.75),
					CacheRead: floatPtr(0.02), CacheWrite: floatPtr(0.25),
				}},
			},
			prompt: 300000, cacheRead: 250000, cacheWrite: 10000, completion: 5000,
			// the tier replaces every base rate for the whole request
			// uncached 40000*0.2 = 8000
			// read 250000*0.02 = 5000, write 10000*0.25 = 2500
			// completion 5000*0.75 = 3750
			want: 8000 + 5000 + 2500 + 3750,
		},
		{
			name:       "all-zero rates are a free price",
			rates:      Rates{Input: 0, Output: 0, CacheRead: floatPtr(0)},
			prompt:     1000,
			cacheRead:  500,
			completion: 100,
			want:       0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.rates.EstimateMicroUSD(tc.prompt, tc.cacheRead, tc.cacheWrite, tc.completion)
			if got != tc.want {
				t.Errorf("EstimateMicroUSD(%d, %d, %d, %d) = %d, want %d", tc.prompt, tc.cacheRead, tc.cacheWrite, tc.completion, got, tc.want)
			}
		})
	}
}

// fakePricer returns fixed rates and counts how often Price ran.
type fakePricer struct {
	rates Rates
	ok    bool
	calls atomic.Int64
}

func (f *fakePricer) Price(Observation) (Rates, bool) {
	f.calls.Add(1)
	return f.rates, f.ok
}

// TestRecordEstimatedCost covers the pricer path: estimates count as priced but
// never as provider-reported, and a reported cost wins without asking the
// pricer.
func TestRecordEstimatedCost(t *testing.T) {
	tests := []struct {
		name            string
		rates           Rates
		ok              bool
		reportedCost    *float64
		wantMicro       int64
		wantReported    int
		wantUnpriced    int
		wantPricerCalls int64
	}{
		{
			name:  "estimate used when no reported cost",
			rates: Rates{Input: 1, Output: 2},
			ok:    true,
			// uncached 90*1 + read 10*1 (fallback) + completion 10*2 = 120
			wantMicro: 120, wantPricerCalls: 1,
		},
		{
			name:         "reported cost overrides pricer",
			rates:        Rates{Input: 1, Output: 2},
			ok:           true,
			reportedCost: floatPtr(0.0005),
			wantMicro:    500, wantReported: 1,
		},
		{
			name:         "pricer without a rate leaves the request unpriced",
			rates:        Rates{},
			ok:           false,
			wantUnpriced: 1, wantPricerCalls: 1,
		},
		{
			name:      "zero rates price the request as free",
			rates:     Rates{Input: 0, Output: 0},
			ok:        true,
			wantMicro: 0, wantPricerCalls: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolateTest(t)
			r := newTestRecorder(t, fixedClock(baseTime))
			pricer := &fakePricer{rates: tc.rates, ok: tc.ok}
			r.SetPricer(pricer)
			r.Record(Observation{
				PromptTokens: 100, CompletionTokens: 10, CacheReadTokens: 10,
				ReportedCostUSD: tc.reportedCost, At: baseTime,
			})

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
			if got := pricer.calls.Load(); got != tc.wantPricerCalls {
				t.Errorf("pricer calls = %d, want %d", got, tc.wantPricerCalls)
			}
		})
	}
}

// TestSetPricerAfterConstruction proves SetPricer applies to later recordings
// and that a nil Pricer restores the unpriced behaviour.
func TestSetPricerAfterConstruction(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.Record(Observation{PromptTokens: 100, At: baseTime})
	r.SetPricer(&fakePricer{rates: Rates{Input: 1}, ok: true})
	r.Record(Observation{PromptTokens: 10, At: baseTime})
	r.SetPricer(nil)
	r.Record(Observation{PromptTokens: 1000, At: baseTime})

	b := r.buckets[bucketKey{hourUnix: hourStart.Unix()}]
	if b == nil {
		t.Fatal("expected bucket to exist")
	}
	if b.Requests != 3 {
		t.Errorf("Requests = %d, want 3", b.Requests)
	}
	if b.CostMicroUSD != 10 {
		t.Errorf("CostMicroUSD = %d, want 10", b.CostMicroUSD)
	}
	if b.UnpricedRequests != 2 {
		t.Errorf("UnpricedRequests = %d, want 2", b.UnpricedRequests)
	}
	if b.ReportedCostRequests != 0 {
		t.Errorf("ReportedCostRequests = %d, want 0", b.ReportedCostRequests)
	}
}

// TestRecordEstimatedCostSumsAcrossRequests pins that tier-priced estimates add
// up in the bucket.
func TestRecordEstimatedCostSumsAcrossRequests(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	r.SetPricer(&fakePricer{
		rates: Rates{
			Input: 0.1, Output: 0.5, CacheRead: floatPtr(0.01), CacheWrite: floatPtr(0.125),
			Tiers: []RateTier{{
				Size: 272000, Input: floatPtr(0.2), Output: floatPtr(0.75),
				CacheRead: floatPtr(0.02), CacheWrite: floatPtr(0.25),
			}},
		},
		ok: true,
	})
	r.Record(Observation{PromptTokens: 100000, CacheReadTokens: 90000, CompletionTokens: 2000, At: baseTime})
	r.Record(Observation{PromptTokens: 300000, CacheReadTokens: 250000, CacheCreateTokens: 10000, CompletionTokens: 5000, At: baseTime})

	b := r.buckets[bucketKey{hourUnix: hourStart.Unix()}]
	if b == nil {
		t.Fatal("expected bucket to exist")
	}
	if b.CostMicroUSD != 2900+19250 {
		t.Errorf("CostMicroUSD = %d, want %d", b.CostMicroUSD, 2900+19250)
	}
	if b.UnpricedRequests != 0 || b.ReportedCostRequests != 0 {
		t.Errorf("UnpricedRequests = %d, ReportedCostRequests = %d, want 0 and 0", b.UnpricedRequests, b.ReportedCostRequests)
	}
}

// TestTelemetryCarriesEstimatedCost proves the telemetry line reports the
// estimator's cost, not just a provider-reported one.
func TestTelemetryCarriesEstimatedCost(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "telemetry-cost.jsonl")
	t.Setenv(TelemetryEnvVar, path)
	t.Setenv(TelemetryRunEnvVar, "")

	rec := newTestRecorder(t, fixedClock(baseTime))
	rec.SetPricer(&fakePricer{rates: Rates{Input: 1, Output: 2}, ok: true})
	rec.Record(Observation{ProviderAlias: "p", PromptTokens: 100, CompletionTokens: 10, At: baseTime})
	rec.Record(Observation{ProviderAlias: "p", PromptTokens: 100, At: baseTime})
	rec.SetPricer(nil)
	rec.Record(Observation{ProviderAlias: "p", PromptTokens: 100, At: baseTime})

	lines := readTelemetryLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("got %d telemetry lines, want 3", len(lines))
	}
	// uncached 100*1 + completion 10*2 = 120
	if lines[0].CostMicroUSD != 120 {
		t.Errorf("cost_micro_usd = %d, want 120", lines[0].CostMicroUSD)
	}
	if lines[1].CostMicroUSD != 100 {
		t.Errorf("cost_micro_usd = %d, want 100", lines[1].CostMicroUSD)
	}
	if lines[2].CostMicroUSD != 0 {
		t.Errorf("cost_micro_usd = %d, want 0", lines[2].CostMicroUSD)
	}
}

// TestSetPricerConcurrentWithRecord exercises SetPricer's concurrency contract;
// the race detector is the assertion.
func TestSetPricerConcurrentWithRecord(t *testing.T) {
	isolateTest(t)
	r := newTestRecorder(t, fixedClock(baseTime))
	pricer := &fakePricer{rates: Rates{Input: 1}, ok: true}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				r.Record(Observation{PromptTokens: 10, At: baseTime})
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 20 {
			if i%2 == 0 {
				r.SetPricer(pricer)
				continue
			}
			r.SetPricer(nil)
		}
	}()
	wg.Wait()

	b := r.buckets[bucketKey{hourUnix: hourStart.Unix()}]
	if b == nil {
		t.Fatal("expected bucket to exist")
	}
	if b.Requests != 80 {
		t.Errorf("Requests = %d, want 80", b.Requests)
	}
	if b.ReportedCostRequests != 0 {
		t.Errorf("ReportedCostRequests = %d, want 0", b.ReportedCostRequests)
	}
}
