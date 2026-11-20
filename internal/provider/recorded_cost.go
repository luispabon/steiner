package provider

import "github.com/luispabon/steiner/internal/config"

// RecordedCostUSD returns the cost to record in usage statistics for one call.
// Only OpenRouter reports a per-call dollar amount that can be attributed to
// the call itself; other providers either report nothing or report envelope or
// cumulative session totals, so their cost is dropped and the call is
// estimated from models.dev prices instead.
func RecordedCostUSD(usage *UsageStats, providerType config.ProviderType) *float64 {
	if usage == nil || usage.CostUSD == nil || providerType != config.ProviderTypeOpenRouter {
		return nil
	}
	return usage.CostUSD
}
