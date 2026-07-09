package advisor

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestDrainStream(t *testing.T) {
	tests := []struct {
		name    string
		chunks  []provider.ChatChunk
		want    provider.ChatResponse
		wantErr string
	}{
		{
			name: "delta accumulation then final content appended",
			chunks: []provider.ChatChunk{
				{Delta: provider.Message{Content: "Hello "}},
				{Delta: provider.Message{Content: "world"}},
				{Done: true, Delta: provider.Message{Content: "Hello world"}, FinishReason: "stop"},
			},
			want: provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "Hello world"},
				FinishReason: "stop",
			},
		},
		{
			name: "final chunk full content when message empty",
			chunks: []provider.ChatChunk{
				{Done: true, Delta: provider.Message{Content: "Only final"}, FinishReason: "stop"},
			},
			want: provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "Only final"},
				FinishReason: "stop",
			},
		},
		{
			name: "error chunk with OriginalError returns that error",
			chunks: []provider.ChatChunk{
				{Delta: provider.Message{Content: "some"}},
				{Error: "oops", OriginalError: errors.New("original error")},
			},
			wantErr: "original error",
		},
		{
			name: "error chunk with only Error text returns that text",
			chunks: []provider.ChatChunk{
				{Error: "stream error occurred"},
			},
			wantErr: "stream error occurred",
		},
		{
			name: "RetryReset mid-stream resets prior accumulated content",
			chunks: []provider.ChatChunk{
				{Delta: provider.Message{Content: "old content "}},
				{RetryReset: true},
				{Delta: provider.Message{Content: "new content "}},
				{Done: true, Delta: provider.Message{Content: "new content final"}, FinishReason: "stop"},
			},
			want: provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "new content final"},
				FinishReason: "stop",
			},
		},
		{
			name: "stream ends without Done",
			chunks: []provider.ChatChunk{
				{Delta: provider.Message{Content: "partial"}},
			},
			wantErr: "stream completed without a final chunk",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch := make(chan provider.ChatChunk, len(tc.chunks))
			for _, c := range tc.chunks {
				ch <- c
			}
			close(ch)

			got, err := drainStream(ch)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("drainStream() error = nil, want %q", tc.wantErr)
				}
				if gotErr := err.Error(); gotErr != tc.wantErr {
					t.Fatalf("drainStream() error = %q, want %q", gotErr, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("drainStream() error = %v, want nil", err)
			}
			if got.Message.Content != tc.want.Message.Content {
				t.Fatalf("content = %q, want %q", got.Message.Content, tc.want.Message.Content)
			}
			if got.Message.Role != tc.want.Message.Role {
				t.Fatalf("role = %q, want %q", got.Message.Role, tc.want.Message.Role)
			}
			if got.FinishReason != tc.want.FinishReason {
				t.Fatalf("FinishReason = %q, want %q", got.FinishReason, tc.want.FinishReason)
			}
		})
	}
}
