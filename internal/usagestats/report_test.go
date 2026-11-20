package usagestats

import "testing"

func TestReportedHitRate(t *testing.T) {
	tests := []struct {
		name          string
		cacheRead     int
		reportedInput int
		wantRate      float64
		wantOK        bool
	}{
		{
			name:   "no reported cache data",
			wantOK: false,
		},
		{
			name:          "reported with zero reads is a zero percent rate",
			reportedInput: 100,
			wantRate:      0,
			wantOK:        true,
		},
		{
			name:          "normal mix",
			cacheRead:     80,
			reportedInput: 100,
			wantRate:      0.8,
			wantOK:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rate, ok := ReportedHitRate(tc.cacheRead, tc.reportedInput)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && rate != tc.wantRate {
				t.Fatalf("rate = %v, want %v", rate, tc.wantRate)
			}
		})
	}
}

func TestCacheReportedInput(t *testing.T) {
	tests := []struct {
		name          string
		promptTokens  int
		cacheRead     int
		cacheCreate   int
		cacheReported bool
		wantTokens    int
		wantReported  bool
	}{
		{
			name:         "provider said nothing about caching",
			promptTokens: 100,
		},
		{
			name:          "flag set with zero cache tokens counts as reported",
			promptTokens:  100,
			cacheReported: true,
			wantTokens:    100,
			wantReported:  true,
		},
		{
			name:         "positive cache tokens infer reporting",
			promptTokens: 100,
			cacheRead:    60,
			cacheCreate:  10,
			wantTokens:   100,
			wantReported: true,
		},
		{
			name:          "cache tokens beyond the prompt total floor uncached at zero",
			promptTokens:  20,
			cacheRead:     30,
			cacheReported: true,
			wantTokens:    30,
			wantReported:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokens, reported := CacheReportedInput(tc.promptTokens, tc.cacheRead, tc.cacheCreate, tc.cacheReported)
			if reported != tc.wantReported {
				t.Fatalf("reported = %v, want %v", reported, tc.wantReported)
			}
			if tokens != tc.wantTokens {
				t.Fatalf("tokens = %d, want %d", tokens, tc.wantTokens)
			}
		})
	}
}

func TestTotals_PerRequest(t *testing.T) {
	totals := Totals{Requests: 4, InputTokens: 40, CacheReadTokens: 60, CacheCreateTokens: 20, CacheReportedInputTokens: 120}

	uncached, ok := totals.UncachedPerRequest()
	if !ok || uncached != 15 {
		t.Fatalf("UncachedPerRequest() = (%v, %v), want (15, true)", uncached, ok)
	}
	cached, ok := totals.CachedPerRequest()
	if !ok || cached != 15 {
		t.Fatalf("CachedPerRequest() = (%v, %v), want (15, true)", cached, ok)
	}

	// The two per-request figures must reproduce HitRate: cached / (cached + uncached).
	rate, rateOK := totals.HitRate()
	if !rateOK {
		t.Fatal("HitRate() ok = false, want true")
	}
	if got := cached / (cached + uncached); got != rate {
		t.Errorf("cached/(cached+uncached) = %v, want HitRate() = %v", got, rate)
	}

	zero := Totals{}
	if _, ok := zero.UncachedPerRequest(); ok {
		t.Error("UncachedPerRequest() on zero requests: ok = true, want false")
	}
	if _, ok := zero.CachedPerRequest(); ok {
		t.Error("CachedPerRequest() on zero requests: ok = true, want false")
	}
}

func TestTotals_TotalAndInputTotal(t *testing.T) {
	tests := []struct {
		name       string
		totals     Totals
		wantTotal  int
		wantInput  int
		wantRate   float64
		wantRateOK bool
	}{
		{
			name: "zero value",
		},
		{
			name:       "every field counted once",
			totals:     Totals{Requests: 2, InputTokens: 10, CacheReadTokens: 60, CacheCreateTokens: 20, CompletionTokens: 5, CacheReportedInputTokens: 90},
			wantTotal:  95,
			wantInput:  90,
			wantRate:   60.0 / 90.0,
			wantRateOK: true,
		},
		{
			name:       "completion tokens stay out of the hit-rate denominator",
			totals:     Totals{InputTokens: 30, CacheReadTokens: 30, CompletionTokens: 40, CacheReportedInputTokens: 60},
			wantTotal:  100,
			wantInput:  60,
			wantRate:   0.5,
			wantRateOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.totals.Total(); got != tc.wantTotal {
				t.Errorf("Total() = %d, want %d", got, tc.wantTotal)
			}
			if got := tc.totals.InputTotal(); got != tc.wantInput {
				t.Errorf("InputTotal() = %d, want %d", got, tc.wantInput)
			}
			rate, ok := tc.totals.HitRate()
			if ok != tc.wantRateOK {
				t.Errorf("HitRate() ok = %v, want %v", ok, tc.wantRateOK)
			}
			if rate != tc.wantRate {
				t.Errorf("HitRate() rate = %v, want %v", rate, tc.wantRate)
			}
		})
	}
}

func TestSessionReport_PerRequest(t *testing.T) {
	sr := SessionReport{Requests: 5, CacheReadTokens: 100, TotalInputTokens: 250}

	cached, ok := sr.CachedPerRequest()
	if !ok || cached != 20 {
		t.Fatalf("CachedPerRequest() = (%v, %v), want (20, true)", cached, ok)
	}
	uncached, ok := sr.UncachedPerRequest()
	if !ok || uncached != 30 {
		t.Fatalf("UncachedPerRequest() = (%v, %v), want (30, true)", uncached, ok)
	}

	zero := SessionReport{}
	if _, ok := zero.CachedPerRequest(); ok {
		t.Error("CachedPerRequest() on zero requests: ok = true, want false")
	}
	if _, ok := zero.UncachedPerRequest(); ok {
		t.Error("UncachedPerRequest() on zero requests: ok = true, want false")
	}
}
