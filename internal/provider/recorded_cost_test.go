package provider

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestRecordedCostUSD(t *testing.T) {
	cost := 0.25
	tests := []struct {
		name         string
		usage        *UsageStats
		providerType config.ProviderType
		want         *float64
	}{
		{
			name:         "openrouter cost is recorded as reported",
			usage:        &UsageStats{CostUSD: &cost},
			providerType: config.ProviderTypeOpenRouter,
			want:         &cost,
		},
		{
			name:         "openai cost is not recorded",
			usage:        &UsageStats{CostUSD: &cost},
			providerType: config.ProviderTypeOpenAI,
		},
		{
			name:         "openai_compat cost is not recorded",
			usage:        &UsageStats{CostUSD: &cost},
			providerType: config.ProviderTypeOpenAICompat,
		},
		{
			name:         "openrouter usage without a cost",
			usage:        &UsageStats{},
			providerType: config.ProviderTypeOpenRouter,
		},
		{
			name:         "nil usage",
			providerType: config.ProviderTypeOpenRouter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RecordedCostUSD(tt.usage, tt.providerType)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("RecordedCostUSD() = %v, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("RecordedCostUSD() = %v, want %v", got, *tt.want)
			}
		})
	}
}
