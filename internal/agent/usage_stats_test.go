package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func floatPtr(v float64) *float64 { return &v }

func TestSumUsageStats(t *testing.T) {
	tests := []struct {
		name string
		a    *provider.UsageStats
		b    *provider.UsageStats
		want *provider.UsageStats
	}{
		{
			name: "both nil stays nil",
		},
		{
			name: "nil side adds nothing",
			a:    &provider.UsageStats{PromptTokens: 10},
			want: &provider.UsageStats{PromptTokens: 10},
		},
		{
			name: "every token field sums and cache presence is or-ed",
			a: &provider.UsageStats{
				PromptTokens: 100, CompletionTokens: 40, TotalTokens: 140,
				ReasoningTokens: 10, CacheCreationInputTokens: 5, CacheReadInputTokens: 20,
				CacheReported: true, CostUSD: floatPtr(0.5),
			},
			b: &provider.UsageStats{
				PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3,
				ReasoningTokens: 1, CacheCreationInputTokens: 1, CacheReadInputTokens: 1,
				CostUSD: floatPtr(0.25),
			},
			want: &provider.UsageStats{
				PromptTokens: 101, CompletionTokens: 42, TotalTokens: 143,
				ReasoningTokens: 11, CacheCreationInputTokens: 6, CacheReadInputTokens: 21,
				CacheReported: true, CostUSD: floatPtr(0.75),
			},
		},
		{
			name: "cache reported on the right side alone",
			a:    &provider.UsageStats{},
			b:    &provider.UsageStats{CacheReported: true},
			want: &provider.UsageStats{CacheReported: true},
		},
		{
			name: "nil cost counts as zero",
			a:    &provider.UsageStats{CostUSD: floatPtr(0.5)},
			b:    &provider.UsageStats{},
			want: &provider.UsageStats{CostUSD: floatPtr(0.5)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sumUsageStats(tc.a, tc.b)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("sumUsageStats() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("sumUsageStats() = nil, want %+v", tc.want)
			}
			assertUsageStatsEqual(t, *got, *tc.want)
		})
	}
}

func TestCloneUsageStats(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		if got := cloneUsageStats(nil); got != nil {
			t.Fatalf("cloneUsageStats(nil) = %+v, want nil", got)
		}
	})

	t.Run("copy does not alias the cost", func(t *testing.T) {
		original := &provider.UsageStats{PromptTokens: 10, ReasoningTokens: 4, CacheReported: true, CostUSD: floatPtr(0.5)}
		clone := cloneUsageStats(original)
		if clone == original {
			t.Fatal("cloneUsageStats returned the same pointer")
		}
		assertUsageStatsEqual(t, *clone, *original)

		*clone.CostUSD = 9
		if *original.CostUSD != 0.5 {
			t.Errorf("mutating the clone changed the original cost to %v, want 0.5", *original.CostUSD)
		}
	})
}

func assertUsageStatsEqual(t *testing.T, got, want provider.UsageStats) {
	t.Helper()
	gotCost, wantCost := got.CostUSD, want.CostUSD
	got.CostUSD, want.CostUSD = nil, nil
	if got != want {
		t.Errorf("usage stats = %+v, want %+v", got, want)
	}
	switch {
	case gotCost == nil && wantCost == nil:
	case gotCost == nil || wantCost == nil:
		t.Errorf("cost = %v, want %v", gotCost, wantCost)
	case *gotCost != *wantCost:
		t.Errorf("cost = %v, want %v", *gotCost, *wantCost)
	}
}
