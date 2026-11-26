package agent

import "github.com/luispabon/steiner/internal/provider"

// sumUsageStats returns the token accounting of two summary calls. A nil record
// adds nothing, and two nil records stay nil, so a stage that issued no call is
// not reported as usage.
func sumUsageStats(a, b *provider.UsageStats) *provider.UsageStats {
	if a == nil && b == nil {
		return nil
	}
	out := provider.UsageStats{}
	if a != nil {
		out = *a
	}
	if b != nil {
		out.PromptTokens += b.PromptTokens
		out.CompletionTokens += b.CompletionTokens
		out.TotalTokens += b.TotalTokens
		out.CacheCreationInputTokens += b.CacheCreationInputTokens
		out.CacheReadInputTokens += b.CacheReadInputTokens
		out.ReasoningTokens += b.ReasoningTokens
		out.CacheReported = out.CacheReported || b.CacheReported
		out.CostUSD = sumCostUSD(out.CostUSD, b.CostUSD)
	}
	return &out
}

// sumCompactionUsage returns the token accounting and reported-cache input
// total of two stage outcomes. A nil usage record adds nothing, matching
// sumUsageStats.
func sumCompactionUsage(a, b CompactionOutcome) (*provider.UsageStats, int) {
	return sumUsageStats(a.Usage, b.Usage), a.CacheReportedInputTokens + b.CacheReportedInputTokens
}

// sumCostUSD adds two provider-reported costs, treating a nil cost as zero and
// keeping nil only when neither side reported a cost.
func sumCostUSD(a, b *float64) *float64 {
	if a == nil && b == nil {
		return nil
	}
	var sum float64
	if a != nil {
		sum += *a
	}
	if b != nil {
		sum += *b
	}
	return &sum
}
