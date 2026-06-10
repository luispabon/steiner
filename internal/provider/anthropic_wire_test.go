package provider

import "testing"

func TestAnthropicUsageToUsageStats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		usage *anthropicUsage
		want  *UsageStats
	}{
		{
			name:  "nil",
			usage: nil,
			want:  nil,
		},
		{
			name: "no cache",
			usage: &anthropicUsage{
				InputTokens:  11,
				OutputTokens: 7,
			},
			want: &UsageStats{
				PromptTokens:     11,
				CompletionTokens: 7,
				TotalTokens:      18,
			},
		},
		{
			name: "cache creation",
			usage: &anthropicUsage{
				InputTokens:              11,
				OutputTokens:             7,
				CacheCreationInputTokens: 3,
			},
			want: &UsageStats{
				PromptTokens:             14,
				CompletionTokens:         7,
				TotalTokens:              21,
				CacheCreationInputTokens: 3,
			},
		},
		{
			name: "cache read",
			usage: &anthropicUsage{
				InputTokens:          11,
				OutputTokens:         7,
				CacheReadInputTokens: 4,
			},
			want: &UsageStats{
				PromptTokens:         15,
				CompletionTokens:     7,
				TotalTokens:          22,
				CacheReadInputTokens: 4,
			},
		},
		{
			name: "both cache fields",
			usage: &anthropicUsage{
				InputTokens:              11,
				OutputTokens:             7,
				CacheCreationInputTokens: 3,
				CacheReadInputTokens:     4,
			},
			want: &UsageStats{
				PromptTokens:             18,
				CompletionTokens:         7,
				TotalTokens:              25,
				CacheCreationInputTokens: 3,
				CacheReadInputTokens:     4,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.usage.toUsageStats()
			if tt.want == nil {
				if got != nil {
					t.Fatalf("toUsageStats() = %#v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("toUsageStats() = nil, want usage stats")
			}
			if got.PromptTokens != tt.want.PromptTokens {
				t.Fatalf("PromptTokens = %d, want %d", got.PromptTokens, tt.want.PromptTokens)
			}
			if got.CompletionTokens != tt.want.CompletionTokens {
				t.Fatalf("CompletionTokens = %d, want %d", got.CompletionTokens, tt.want.CompletionTokens)
			}
			if got.TotalTokens != tt.want.TotalTokens {
				t.Fatalf("TotalTokens = %d, want %d", got.TotalTokens, tt.want.TotalTokens)
			}
			if got.CacheCreationInputTokens != tt.want.CacheCreationInputTokens {
				t.Fatalf("CacheCreationInputTokens = %d, want %d", got.CacheCreationInputTokens, tt.want.CacheCreationInputTokens)
			}
			if got.CacheReadInputTokens != tt.want.CacheReadInputTokens {
				t.Fatalf("CacheReadInputTokens = %d, want %d", got.CacheReadInputTokens, tt.want.CacheReadInputTokens)
			}
		})
	}
}
