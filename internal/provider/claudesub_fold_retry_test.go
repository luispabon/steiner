package provider

import (
	"testing"
	"time"
)

// TestFoldClaudeSubChunksAPIRetrySummary proves a folded call reports the CLI's own
// API retries once: nothing without a notice, and one summary warning naming the
// count and the last failure while every other diagnostic stays one warning.
func TestFoldClaudeSubChunksAPIRetrySummary(t *testing.T) {
	notice := func(attempt int, category string, status int) ChatChunk {
		return claudeSubAPIRetryChunk(claudeSubAPIRetry{
			Attempt:     attempt,
			MaxRetries:  10,
			RetryDelay:  time.Second,
			Error:       category,
			ErrorStatus: status,
		})
	}
	done := ChatChunk{Done: true, Delta: Message{Role: MessageRoleAssistant, Content: "answer"}, FinishReason: "stop"}
	other := claudeSubWarningChunk("provider replaced its process")
	tests := []struct {
		name   string
		chunks []ChatChunk
		want   []string
	}{
		{
			name:   "no notices",
			chunks: []ChatChunk{other, done},
			want:   []string{"provider replaced its process"},
		},
		{
			name:   "one notice",
			chunks: []ChatChunk{notice(1, "overloaded", 529), done},
			want:   []string{"claude CLI retried the API request 1 time(s) (last: overloaded, status 529)"},
		},
		{
			name:   "three notices and another diagnostic",
			chunks: []ChatChunk{notice(1, "overloaded", 529), other, notice(2, "server_error", 500), notice(3, "rate_limit", 0), done},
			want: []string{
				"provider replaced its process",
				"claude CLI retried the API request 3 time(s) (last: rate_limit)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := foldClaudeSubChunks(tt.chunks)
			if err != nil {
				t.Fatalf("fold: %v", err)
			}
			if response.Message.Content != "answer" {
				t.Errorf("content = %q, want %q", response.Message.Content, "answer")
			}
			if len(response.Warnings) != len(tt.want) {
				t.Fatalf("warnings = %q, want %q", response.Warnings, tt.want)
			}
			for i := range tt.want {
				if response.Warnings[i] != tt.want[i] {
					t.Errorf("warning %d = %q, want %q", i, response.Warnings[i], tt.want[i])
				}
			}
		})
	}
}
