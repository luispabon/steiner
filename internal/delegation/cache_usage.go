package delegation

import "github.com/luispabon/steiner/internal/agent"

// CacheUsage holds cumulative prompt-cache token counts for a child agent.
type CacheUsage struct {
	InputTokens       int // uncached prompt tokens
	CacheReadTokens   int
	CacheCreateTokens int
}

// Add returns u plus v's counters.
func (u CacheUsage) Add(v CacheUsage) CacheUsage {
	return CacheUsage{
		InputTokens:       u.InputTokens + v.InputTokens,
		CacheReadTokens:   u.CacheReadTokens + v.CacheReadTokens,
		CacheCreateTokens: u.CacheCreateTokens + v.CacheCreateTokens,
	}
}

// cacheUsageOf extracts a run's cache counters from its final state.
func cacheUsageOf(state agent.RunState) CacheUsage {
	return CacheUsage{
		InputTokens:       state.InputTokens,
		CacheReadTokens:   state.CacheReadTokens,
		CacheCreateTokens: state.CacheCreateTokens,
	}
}
