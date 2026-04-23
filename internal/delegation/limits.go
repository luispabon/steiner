package delegation

import (
	"github.com/luispabon/steiner/internal/config"
)

// DefaultLimits creates DelegationLimits from SubAgentConfig.
// MaxTurns defaults to 15, OutputLimitTokens from MaxTokens (default 100000),
// Timeout defaults to 0 (no timeout).
func DefaultLimits(cfg config.SubAgentConfig) DelegationLimits {
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 15
	}

	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 100000
	}

	return DelegationLimits{
		MaxTurns:          maxTurns,
		OutputLimitTokens: maxTokens,
		Timeout:           0,
	}
}

// ApplyOverrides merges overrides into base limits with tighten-only semantics.
// An override is applied only if it is more restrictive (lower/shorter).
// Zero values in overrides are treated as "no override".
func ApplyOverrides(base, overrides DelegationLimits) DelegationLimits {
	result := base

	// MaxTurns: override if it's positive and lower than base
	if overrides.MaxTurns > 0 && (base.MaxTurns <= 0 || overrides.MaxTurns < base.MaxTurns) {
		result.MaxTurns = overrides.MaxTurns
	}

	// OutputLimitTokens: override if it's positive and lower than base
	if overrides.OutputLimitTokens > 0 && (base.OutputLimitTokens <= 0 || overrides.OutputLimitTokens < base.OutputLimitTokens) {
		result.OutputLimitTokens = overrides.OutputLimitTokens
	}

	// Timeout: override if it's positive and lower than base (or base is zero)
	if overrides.Timeout > 0 && (base.Timeout == 0 || overrides.Timeout < base.Timeout) {
		result.Timeout = overrides.Timeout
	}

	return result
}
