package delegation

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/usagestats"
)

func TestTokenUsageAdd(t *testing.T) {
	tests := []struct {
		name string
		base TokenUsage
		adds []TokenUsage
		want TokenUsage
	}{
		{
			name: "zero-value base sums first add",
			base: TokenUsage{},
			adds: []TokenUsage{{InputTokens: 10, CacheReadTokens: 20, CacheCreateTokens: 30, OutputTokens: 40}},
			want: TokenUsage{InputTokens: 10, CacheReadTokens: 20, CacheCreateTokens: 30, OutputTokens: 40},
		},
		{
			name: "two additions accumulate",
			base: TokenUsage{InputTokens: 100, CacheReadTokens: 200, CacheCreateTokens: 300, OutputTokens: 400},
			adds: []TokenUsage{
				{InputTokens: 1, CacheReadTokens: 2, CacheCreateTokens: 3, OutputTokens: 4},
				{InputTokens: 4, CacheReadTokens: 5, CacheCreateTokens: 6, OutputTokens: 7},
			},
			want: TokenUsage{InputTokens: 105, CacheReadTokens: 207, CacheCreateTokens: 309, OutputTokens: 411},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := tt.base
			got := base
			for _, add := range tt.adds {
				got = got.Add(add)
			}
			if got != tt.want {
				t.Fatalf("Add() = %+v, want %+v", got, tt.want)
			}
			if base != tt.base {
				t.Fatalf("receiver mutated: %+v -> %+v", tt.base, base)
			}
		})
	}
}

func TestTokenUsageOf(t *testing.T) {
	tests := []struct {
		name  string
		state agent.RunState
		want  TokenUsage
	}{
		{
			name:  "zero state",
			state: agent.RunState{},
			want:  TokenUsage{},
		},
		{
			name: "extracts cache counters",
			state: agent.RunState{
				InputTokens:       50,
				CacheReadTokens:   900,
				CacheCreateTokens: 10,
				TurnCount:         7,
				TokenCount:        5000,
			},
			want: TokenUsage{InputTokens: 50, CacheReadTokens: 900, CacheCreateTokens: 10, OutputTokens: 5000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenUsageOf(tt.state)
			if got != tt.want {
				t.Fatalf("tokenUsageOf() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestTokenUsageOf_HitRateRegression pins Fix 0 (issue #490): RunState.InputTokens
// must already be the non-cached portion, so tokenUsageOf must not double-count
// cache-read tokens when feeding usagestats.HitRate. For a turn with 100 total
// prompt tokens where 50 were served from cache, the hit rate must be 50/100 =
// 50%, not 50/150.
func TestTokenUsageOf_HitRateRegression(t *testing.T) {
	state := agent.RunState{
		InputTokens:     50, // non-cached portion: PromptTokens(100) - CacheReadInputTokens(50)
		CacheReadTokens: 50,
	}
	usage := tokenUsageOf(state)

	rate, ok := usagestats.HitRate(usage.CacheReadTokens, usage.InputTokens, usage.CacheCreateTokens)
	if !ok {
		t.Fatal("HitRate() ok = false, want true")
	}
	if rate != 0.5 {
		t.Fatalf("HitRate() = %v, want 0.5", rate)
	}
}
