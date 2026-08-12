package usagestats

import "testing"

func TestHitRate_Direct(t *testing.T) {
	tests := []struct {
		name       string
		cacheRead  int
		input      int
		cacheWrite int
		wantRate   float64
		wantOK     bool
	}{
		{
			name:   "zero total",
			wantOK: false,
		},
		{
			name:       "normal mix",
			cacheRead:  80,
			input:      10,
			cacheWrite: 10,
			wantRate:   0.8,
			wantOK:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rate, ok := HitRate(tc.cacheRead, tc.input, tc.cacheWrite)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && rate != tc.wantRate {
				t.Fatalf("rate = %v, want %v", rate, tc.wantRate)
			}
		})
	}
}
