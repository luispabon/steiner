package usagestats

import (
	"testing"
	"time"
)

var (
	baseTime  = time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	hourStart = time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	nextHour  = time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	prevHour  = time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestNew_nilClockDefaultsToTimeNow(t *testing.T) {
	r := New(nil)
	if r == nil {
		t.Fatal("expected non-nil recorder")
	}
	got := r.now()
	if got.IsZero() {
		t.Error("expected non-zero time from default clock")
	}
}

func TestRecord_bucketing(t *testing.T) {
	tests := []struct {
		name         string
		observations []Observation
		wantBuckets  int
	}{
		{
			name: "same hour and key merges",
			observations: []Observation{
				{ProviderAlias: "a", ProviderType: "openai", BackendModelID: "gpt-4", PromptTokens: 100, CompletionTokens: 20, At: baseTime},
				{ProviderAlias: "a", ProviderType: "openai", BackendModelID: "gpt-4", PromptTokens: 50, CompletionTokens: 10, At: baseTime.Add(15 * time.Minute)},
			},
			wantBuckets: 1,
		},
		{
			name: "different key splits",
			observations: []Observation{
				{ProviderAlias: "a", ProviderType: "openai", BackendModelID: "gpt-4", PromptTokens: 100, At: baseTime},
				{ProviderAlias: "b", ProviderType: "openai", BackendModelID: "gpt-4", PromptTokens: 100, At: baseTime},
			},
			wantBuckets: 2,
		},
		{
			name: "different hour splits",
			observations: []Observation{
				{ProviderAlias: "a", ProviderType: "openai", BackendModelID: "gpt-4", PromptTokens: 100, At: baseTime},
				{ProviderAlias: "a", ProviderType: "openai", BackendModelID: "gpt-4", PromptTokens: 100, At: nextHour},
			},
			wantBuckets: 2,
		},
		{
			name: "different model splits",
			observations: []Observation{
				{ProviderAlias: "a", ProviderType: "openai", BackendModelID: "gpt-4", PromptTokens: 100, At: baseTime},
				{ProviderAlias: "a", ProviderType: "openai", BackendModelID: "gpt-3.5", PromptTokens: 100, At: baseTime},
			},
			wantBuckets: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New(fixedClock(baseTime))
			for _, obs := range tc.observations {
				r.Record(obs)
			}
			if got := len(r.buckets); got != tc.wantBuckets {
				t.Errorf("got %d buckets, want %d", got, tc.wantBuckets)
			}
		})
	}
}

func TestRecord_tokenMath(t *testing.T) {
	tests := []struct {
		name            string
		obs             Observation
		wantInput       int
		wantCacheRead   int
		wantCacheCreate int
		wantCompletion  int
		wantRequests    int
	}{
		{
			name:           "no cache tokens",
			obs:            Observation{PromptTokens: 100, CompletionTokens: 20, At: baseTime},
			wantInput:      100,
			wantCompletion: 20,
			wantRequests:   1,
		},
		{
			name:           "with cache read",
			obs:            Observation{PromptTokens: 100, CacheReadTokens: 60, CompletionTokens: 20, At: baseTime},
			wantInput:      40,
			wantCacheRead:  60,
			wantCompletion: 20,
			wantRequests:   1,
		},
		{
			name:            "with cache create",
			obs:             Observation{PromptTokens: 100, CacheCreateTokens: 80, CompletionTokens: 10, At: baseTime},
			wantInput:       20,
			wantCacheCreate: 80,
			wantCompletion:  10,
			wantRequests:    1,
		},
		{
			name:            "negative non-cached clamped to zero",
			obs:             Observation{PromptTokens: 50, CacheReadTokens: 40, CacheCreateTokens: 30, At: baseTime},
			wantInput:       0,
			wantCacheRead:   40,
			wantCacheCreate: 30,
			wantRequests:    1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New(fixedClock(baseTime))
			r.Record(tc.obs)

			key := bucketKey{
				hourUnix: hourStart.Unix(),
			}
			b := r.buckets[key]
			if b == nil {
				t.Fatal("expected bucket to exist")
			}
			if b.InputTokens != tc.wantInput {
				t.Errorf("InputTokens: got %d, want %d", b.InputTokens, tc.wantInput)
			}
			if b.CacheReadTokens != tc.wantCacheRead {
				t.Errorf("CacheReadTokens: got %d, want %d", b.CacheReadTokens, tc.wantCacheRead)
			}
			if b.CacheCreateTokens != tc.wantCacheCreate {
				t.Errorf("CacheCreateTokens: got %d, want %d", b.CacheCreateTokens, tc.wantCacheCreate)
			}
			if b.CompletionTokens != tc.wantCompletion {
				t.Errorf("CompletionTokens: got %d, want %d", b.CompletionTokens, tc.wantCompletion)
			}
			if b.Requests != tc.wantRequests {
				t.Errorf("Requests: got %d, want %d", b.Requests, tc.wantRequests)
			}
		})
	}
}

func TestRecord_zeroAtUsesClockHour(t *testing.T) {
	r := New(fixedClock(baseTime))
	r.Record(Observation{PromptTokens: 10})

	key := bucketKey{hourUnix: hourStart.Unix()}
	if r.buckets[key] == nil {
		t.Error("expected bucket keyed to clock hour")
	}
}

func TestRecord_mergeAccumulates(t *testing.T) {
	r := New(fixedClock(baseTime))
	r.Record(Observation{
		ProviderAlias: "x", ProviderType: "openai", BackendModelID: "m",
		PromptTokens: 100, CacheReadTokens: 10, CompletionTokens: 5, At: baseTime,
	})
	r.Record(Observation{
		ProviderAlias: "x", ProviderType: "openai", BackendModelID: "m",
		PromptTokens: 200, CacheReadTokens: 20, CompletionTokens: 8, At: baseTime.Add(20 * time.Minute),
	})

	key := bucketKey{providerAlias: "x", providerType: "openai", backendModelID: "m", hourUnix: hourStart.Unix()}
	b := r.buckets[key]
	if b == nil {
		t.Fatal("expected merged bucket")
	}
	if b.Requests != 2 {
		t.Errorf("Requests: got %d, want 2", b.Requests)
	}
	if b.InputTokens != (90 + 180) {
		t.Errorf("InputTokens: got %d, want 270", b.InputTokens)
	}
	if b.CacheReadTokens != 30 {
		t.Errorf("CacheReadTokens: got %d, want 30", b.CacheReadTokens)
	}
	if b.CompletionTokens != 13 {
		t.Errorf("CompletionTokens: got %d, want 13", b.CompletionTokens)
	}
}

func TestWindow(t *testing.T) {
	tests := []struct {
		name          string
		observations  []Observation
		window        time.Duration
		clockTime     time.Time
		wantRows      int
		wantRequests  int
		wantInput     int
		wantCacheRead int
	}{
		{
			name: "observations within window included",
			observations: []Observation{
				{ProviderAlias: "a", ProviderType: "t", BackendModelID: "m", PromptTokens: 100, CacheReadTokens: 20, At: baseTime},
			},
			window:        2 * time.Hour,
			clockTime:     baseTime,
			wantRows:      1,
			wantRequests:  1,
			wantInput:     80,
			wantCacheRead: 20,
		},
		{
			name: "observation outside window excluded",
			observations: []Observation{
				{ProviderAlias: "a", ProviderType: "t", BackendModelID: "m", PromptTokens: 100, At: prevHour.Add(-time.Hour)},
				{ProviderAlias: "a", ProviderType: "t", BackendModelID: "m", PromptTokens: 50, At: baseTime},
			},
			window:       1 * time.Hour,
			clockTime:    baseTime,
			wantRows:     1,
			wantRequests: 1,
			wantInput:    50,
		},
		{
			name: "multiple groups in window produce multiple rows",
			observations: []Observation{
				{ProviderAlias: "a", ProviderType: "t", BackendModelID: "m1", PromptTokens: 100, At: baseTime},
				{ProviderAlias: "a", ProviderType: "t", BackendModelID: "m2", PromptTokens: 200, At: baseTime},
			},
			window:    2 * time.Hour,
			clockTime: baseTime,
			wantRows:  2,
		},
		{
			name:         "empty recorder returns empty report",
			observations: nil,
			window:       1 * time.Hour,
			clockTime:    baseTime,
			wantRows:     0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New(fixedClock(tc.clockTime))
			for _, obs := range tc.observations {
				r.Record(obs)
			}
			report := r.Window(tc.window)
			if got := len(report.Rows); got != tc.wantRows {
				t.Errorf("rows: got %d, want %d", got, tc.wantRows)
			}
			if tc.wantRows == 1 {
				row := report.Rows[0]
				if row.Requests != tc.wantRequests {
					t.Errorf("Requests: got %d, want %d", row.Requests, tc.wantRequests)
				}
				if row.InputTokens != tc.wantInput {
					t.Errorf("InputTokens: got %d, want %d", row.InputTokens, tc.wantInput)
				}
				if row.CacheReadTokens != tc.wantCacheRead {
					t.Errorf("CacheReadTokens: got %d, want %d", row.CacheReadTokens, tc.wantCacheRead)
				}
			}
		})
	}
}

func TestHitRate(t *testing.T) {
	tests := []struct {
		name     string
		row      Row
		wantRate float64
		wantOK   bool
	}{
		{
			name:     "zero cache read, positive input → rate 0.0, ok true",
			row:      Row{InputTokens: 100, CacheReadTokens: 0, CacheCreateTokens: 0},
			wantRate: 0.0,
			wantOK:   true,
		},
		{
			name:     "all cache read → rate 1.0",
			row:      Row{InputTokens: 0, CacheReadTokens: 100, CacheCreateTokens: 0},
			wantRate: 1.0,
			wantOK:   true,
		},
		{
			name:     "mixed cache and input",
			row:      Row{InputTokens: 75, CacheReadTokens: 25, CacheCreateTokens: 0},
			wantRate: 0.25,
			wantOK:   true,
		},
		{
			name:     "total input zero → ok false, no NaN",
			row:      Row{InputTokens: 0, CacheReadTokens: 0, CacheCreateTokens: 0},
			wantRate: 0.0,
			wantOK:   false,
		},
		{
			name:     "cache create counts in denominator",
			row:      Row{InputTokens: 0, CacheReadTokens: 50, CacheCreateTokens: 50},
			wantRate: 0.5,
			wantOK:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rate, ok := tc.row.HitRate()
			if ok != tc.wantOK {
				t.Errorf("ok: got %v, want %v", ok, tc.wantOK)
			}
			if rate != tc.wantRate {
				t.Errorf("rate: got %v, want %v", rate, tc.wantRate)
			}
		})
	}
}

func TestSessionReport(t *testing.T) {
	tests := []struct {
		name           string
		observations   []Observation
		wantCacheRead  int64
		wantTotalInput int64
		wantHitRateOK  bool
		wantHitRate    float64
	}{
		{
			name:          "no observations",
			wantHitRateOK: false,
		},
		{
			name: "only non-cached tokens",
			observations: []Observation{
				{PromptTokens: 100, CompletionTokens: 20, At: baseTime},
			},
			wantCacheRead:  0,
			wantTotalInput: 100,
			wantHitRateOK:  true,
			wantHitRate:    0.0,
		},
		{
			name: "mixed cache read across multiple obs",
			observations: []Observation{
				{PromptTokens: 100, CacheReadTokens: 60, At: baseTime},
				{PromptTokens: 200, CacheReadTokens: 100, At: nextHour},
			},
			// obs1: nonCached=40, obs2: nonCached=100
			// sessionCacheRead = 60+100=160
			// sessionTotalInput = (40+60+0)+(100+100+0) = 100+200 = 300
			wantCacheRead:  160,
			wantTotalInput: 300,
			wantHitRateOK:  true,
			wantHitRate:    float64(160) / float64(300),
		},
		{
			name: "negative non-cached clamped; session counters still correct",
			observations: []Observation{
				{PromptTokens: 10, CacheReadTokens: 40, CacheCreateTokens: 30, At: baseTime},
			},
			// nonCached = 10-40-30 = -60 → clamped to 0
			// sessionCacheRead = 40
			// sessionTotalInput = 0 + 40 + 30 = 70
			wantCacheRead:  40,
			wantTotalInput: 70,
			wantHitRateOK:  true,
			wantHitRate:    float64(40) / float64(70),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := New(fixedClock(baseTime))
			for _, obs := range tc.observations {
				r.Record(obs)
			}
			sr := r.SessionReport()
			if sr.CacheReadTokens != tc.wantCacheRead {
				t.Errorf("CacheReadTokens: got %d, want %d", sr.CacheReadTokens, tc.wantCacheRead)
			}
			if sr.TotalInputTokens != tc.wantTotalInput {
				t.Errorf("TotalInputTokens: got %d, want %d", sr.TotalInputTokens, tc.wantTotalInput)
			}
			rate, ok := sr.HitRate()
			if ok != tc.wantHitRateOK {
				t.Errorf("HitRate ok: got %v, want %v", ok, tc.wantHitRateOK)
			}
			if ok && rate != tc.wantHitRate {
				t.Errorf("HitRate rate: got %v, want %v", rate, tc.wantHitRate)
			}
		})
	}
}

func TestSessionReport_independentOfBuckets(t *testing.T) {
	// Record observations that span two hours (two buckets) and one observation in the future.
	// SessionReport must reflect ALL observations, while Window(1h) will exclude old ones.
	r := New(fixedClock(baseTime))
	r.Record(Observation{PromptTokens: 100, CacheReadTokens: 50, At: prevHour})
	r.Record(Observation{PromptTokens: 200, CacheReadTokens: 100, At: baseTime})

	sr := r.SessionReport()
	// Both observations counted in session
	if sr.CacheReadTokens != 150 {
		t.Errorf("CacheReadTokens: got %d, want 150", sr.CacheReadTokens)
	}
	if sr.TotalInputTokens != 300 {
		t.Errorf("TotalInputTokens: got %d, want 300", sr.TotalInputTokens)
	}

	// Window only covers recent hour; old observation should be excluded
	report := r.Window(30 * time.Minute)
	for _, row := range report.Rows {
		if row.CacheReadTokens == 150 {
			t.Error("Window should not include the old observation's tokens in a narrow window")
		}
	}
}
