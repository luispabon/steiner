package delegation

import "github.com/luispabon/steiner/internal/agent"

// TokenUsage holds cumulative token usage (input, cache, and output/completion) for a child agent across all runs in its life.
type TokenUsage struct {
	InputTokens       int // uncached prompt tokens
	OutputTokens      int // completion/output tokens
	CacheReadTokens   int
	CacheCreateTokens int
	// CacheReportedInputTokens is the prompt-side input total of the calls whose
	// provider reported cache data; zero means none did.
	CacheReportedInputTokens int
}

// Add returns u plus v's counters.
func (u TokenUsage) Add(v TokenUsage) TokenUsage {
	return TokenUsage{
		InputTokens:              u.InputTokens + v.InputTokens,
		CacheReadTokens:          u.CacheReadTokens + v.CacheReadTokens,
		OutputTokens:             u.OutputTokens + v.OutputTokens,
		CacheCreateTokens:        u.CacheCreateTokens + v.CacheCreateTokens,
		CacheReportedInputTokens: u.CacheReportedInputTokens + v.CacheReportedInputTokens,
	}
}

// tokenUsageOf extracts a run's token counters from its final state.
func tokenUsageOf(state agent.RunState) TokenUsage {
	return TokenUsage{
		InputTokens:              state.InputTokens,
		OutputTokens:             state.TokenCount,
		CacheReadTokens:          state.CacheReadTokens,
		CacheCreateTokens:        state.CacheCreateTokens,
		CacheReportedInputTokens: state.CacheReportedInputTokens,
	}
}
