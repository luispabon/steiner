package delegation

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestCacheUsageAdd(t *testing.T) {
	tests := []struct {
		name string
		base CacheUsage
		adds []CacheUsage
		want CacheUsage
	}{
		{
			name: "zero-value base sums first add",
			base: CacheUsage{},
			adds: []CacheUsage{{InputTokens: 10, CacheReadTokens: 20, CacheCreateTokens: 30}},
			want: CacheUsage{InputTokens: 10, CacheReadTokens: 20, CacheCreateTokens: 30},
		},
		{
			name: "two additions accumulate",
			base: CacheUsage{InputTokens: 100, CacheReadTokens: 200, CacheCreateTokens: 300},
			adds: []CacheUsage{
				{InputTokens: 1, CacheReadTokens: 2, CacheCreateTokens: 3},
				{InputTokens: 4, CacheReadTokens: 5, CacheCreateTokens: 6},
			},
			want: CacheUsage{InputTokens: 105, CacheReadTokens: 207, CacheCreateTokens: 309},
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

func TestCacheUsageOf(t *testing.T) {
	tests := []struct {
		name  string
		state agent.RunState
		want  CacheUsage
	}{
		{
			name:  "zero state",
			state: agent.RunState{},
			want:  CacheUsage{},
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
			want: CacheUsage{InputTokens: 50, CacheReadTokens: 900, CacheCreateTokens: 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cacheUsageOf(tt.state)
			if got != tt.want {
				t.Fatalf("cacheUsageOf() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
