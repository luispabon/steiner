package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

// captureRecorder records every observation recordModelUsage hands it.
type captureRecorder struct {
	observations []usagestats.Observation
}

func (c *captureRecorder) Record(obs usagestats.Observation) {
	c.observations = append(c.observations, obs)
}

// TestRecordModelUsageCostAndProviderTypes pins what the agent bridge records:
// only an OpenRouter cost counts as provider-reported, and pricing receives the
// configured provider type rather than a transport-overridden effective one.
func TestRecordModelUsageCostAndProviderTypes(t *testing.T) {
	tests := []struct {
		name             string
		effectiveType    config.ProviderType
		configuredType   config.ProviderType
		wantReportedCost bool
	}{
		{
			name:             "openrouter records its own cost",
			effectiveType:    config.ProviderTypeOpenRouter,
			configuredType:   config.ProviderTypeOpenRouter,
			wantReportedCost: true,
		},
		{
			name:           "openai does not record its cost",
			effectiveType:  config.ProviderTypeOpenAI,
			configuredType: config.ProviderTypeOpenAI,
		},
		{
			name:           "openai_compat does not record its cost",
			effectiveType:  config.ProviderTypeOpenAICompat,
			configuredType: config.ProviderTypeOpenAICompat,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &captureRecorder{}
			req := RunRequest{
				UsageRecorder: recorder,
				ResolvedModel: provider.ResolvedModel{
					ProviderAlias:         "alias",
					BackendModelID:        "model",
					ProviderConfig:        config.ProviderConfig{Type: tt.configuredType},
					EffectiveProviderType: tt.effectiveType,
				},
			}
			usage := &provider.UsageStats{CostUSD: floatPtr(0.25)}

			recordModelUsage(req, usage, usagestats.CallKindNormal)

			if len(recorder.observations) != 1 {
				t.Fatalf("observations = %d, want 1", len(recorder.observations))
			}
			obs := recorder.observations[0]
			if got := obs.ReportedCostUSD != nil; got != tt.wantReportedCost {
				t.Fatalf("ReportedCostUSD set = %t, want %t", got, tt.wantReportedCost)
			}
			if obs.ProviderType != string(tt.effectiveType) {
				t.Fatalf("ProviderType = %q, want the effective type %q", obs.ProviderType, tt.effectiveType)
			}
			if obs.ConfiguredProviderType != string(tt.configuredType) {
				t.Fatalf("ConfiguredProviderType = %q, want the configured type %q", obs.ConfiguredProviderType, tt.configuredType)
			}
		})
	}
}

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
