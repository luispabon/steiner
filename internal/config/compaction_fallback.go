package config

import "time"

// PromptCacheTTL returns the prompt cache expiry estimate for the provider
// alias. A nil config or an unknown alias returns 0. An explicit
// prompt_cache_ttl, including 0, is returned as configured. An unset value
// uses the default for the provider type.
func (c *Config) PromptCacheTTL(providerAlias string) time.Duration {
	if c == nil {
		return 0
	}
	provider, ok := c.Providers[providerAlias]
	if !ok {
		return 0
	}
	if !provider.PromptCacheTTL.IsUnset() {
		return time.Duration(provider.PromptCacheTTL.Duration())
	}
	return defaultPromptCacheTTL(provider.Type)
}

// CompactionFallbackAfter returns the compaction fallback delay for the
// provider alias. A nil config or an unknown alias returns 0. An explicit
// compaction_fallback_after, including 0, is returned as configured. An unset
// value inherits the provider's prompt cache expiry estimate.
func (c *Config) CompactionFallbackAfter(providerAlias string) time.Duration {
	if c == nil {
		return 0
	}
	provider, ok := c.Providers[providerAlias]
	if !ok {
		return 0
	}
	if !provider.CompactionFallbackAfter.IsUnset() {
		return time.Duration(provider.CompactionFallbackAfter.Duration())
	}
	return c.PromptCacheTTL(providerAlias)
}

// defaultPromptCacheTTL returns the prompt cache expiry estimate used when a
// provider of the given type leaves prompt_cache_ttl unset.
func defaultPromptCacheTTL(providerType ProviderType) time.Duration {
	switch providerType {
	case ProviderTypeAnthropic:
		return 5 * time.Minute
	case ProviderTypeClaudeSubscription:
		return 60 * time.Minute
	case ProviderTypeOpenAI, ProviderTypeCodex:
		return 30 * time.Minute
	default:
		return 0
	}
}
