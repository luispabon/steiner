package advisor

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
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

func TestStreamWithEvents(t *testing.T) {
	tests := []struct {
		name               string
		chunks             []provider.ChatChunk
		want               provider.ChatResponse
		wantErr            string
		wantThinkingEvents []string
	}{
		{
			name: "thinking content emits thinking chunk events",
			chunks: []provider.ChatChunk{
				{Delta: provider.Message{Content: ""}, Thinking: "Let me think about this "},
				{Delta: provider.Message{Content: ""}, Thinking: "more deeply"},
				{Done: true, Delta: provider.Message{Content: "Final answer"}, FinishReason: "stop"},
			},
			want: provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "Final answer"},
				FinishReason: "stop",
			},
			wantThinkingEvents: []string{"Let me think about this ", "more deeply"},
		},
		{
			name: "nil sink does not panic",
			chunks: []provider.ChatChunk{
				{Delta: provider.Message{Content: "Hello "}, Thinking: "thinking..."},
				{Done: true, Delta: provider.Message{Content: "Hello world"}, FinishReason: "stop"},
			},
			want: provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "Hello world"},
				FinishReason: "stop",
			},
		},
		{
			name: "content-only chunks emit no thinking events",
			chunks: []provider.ChatChunk{
				{Delta: provider.Message{Content: "Hello "}},
				{Delta: provider.Message{Content: "world"}},
				{Done: true, Delta: provider.Message{Content: "Hello world"}, FinishReason: "stop"},
			},
			want: provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "Hello world"},
				FinishReason: "stop",
			},
			wantThinkingEvents: nil,
		},
		{
			name: "RetryReset clears state but thinking events already emitted are fine",
			chunks: []provider.ChatChunk{
				{Delta: provider.Message{Content: "old "}, Thinking: "old thinking"},
				{RetryReset: true},
				{Delta: provider.Message{Content: "new "}, Thinking: "new thinking"},
				{Done: true, Delta: provider.Message{Content: "new content"}, FinishReason: "stop"},
			},
			want: provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "new content"},
				FinishReason: "stop",
			},
			wantThinkingEvents: []string{"old thinking", "new thinking"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch := make(chan provider.ChatChunk, len(tc.chunks))
			for _, c := range tc.chunks {
				ch <- c
			}
			close(ch)

			var sink output.EventSink
			if tc.wantThinkingEvents != nil {
				sink = &toolSink{}
			}

			got, err := streamWithEvents(ch, sink, output.ChunkSourceAdvisor)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("streamWithEvents() error = nil, want %q", tc.wantErr)
				}
				if gotErr := err.Error(); gotErr != tc.wantErr {
					t.Fatalf("streamWithEvents() error = %q, want %q", gotErr, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("streamWithEvents() error = %v, want nil", err)
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

			// Check thinking events
			if tc.wantThinkingEvents != nil {
				var gotThinking []string
				for _, e := range sink.(*toolSink).events {
					if p, ok := e.Payload.(output.ThinkingChunkEvent); ok {
						gotThinking = append(gotThinking, p.Content)
					}
				}
				if len(gotThinking) != len(tc.wantThinkingEvents) {
					t.Fatalf("thinking events = %v, want %v", gotThinking, tc.wantThinkingEvents)
				}
				for i := range gotThinking {
					if gotThinking[i] != tc.wantThinkingEvents[i] {
						t.Fatalf("thinking event[%d] = %q, want %q", i, gotThinking[i], tc.wantThinkingEvents[i])
					}
				}
			} else if ts, ok := sink.(*toolSink); ok && len(ts.events) > 0 {
				t.Fatalf("unexpected events: %v", ts.events)
			}
		})
	}
}

func TestStreamWithEventsNilSinkIdenticalToDrainStream(t *testing.T) {
	// Verify that streamWithEvents with nil sink produces identical results to drainStream.
	chunks := []provider.ChatChunk{
		{Delta: provider.Message{Content: "Hello "}},
		{Delta: provider.Message{Content: "world"}, Thinking: "thinking..."},
		{Done: true, Delta: provider.Message{Content: "Hello world"}, FinishReason: "stop"},
	}

	ch1 := make(chan provider.ChatChunk, len(chunks))
	ch2 := make(chan provider.ChatChunk, len(chunks))
	for _, c := range chunks {
		ch1 <- c
		ch2 <- c
	}
	close(ch1)
	close(ch2)

	got1, err1 := drainStream(ch1)
	got2, err2 := streamWithEvents(ch2, nil, output.ChunkSourceAdvisor)

	if err1 != nil || err2 != nil {
		t.Fatalf("errors: drainStream=%v, streamWithEvents=%v", err1, err2)
	}
	if got1.Message.Content != got2.Message.Content {
		t.Fatalf("content mismatch: drainStream=%q, streamWithEvents=%q", got1.Message.Content, got2.Message.Content)
	}
	if got1.FinishReason != got2.FinishReason {
		t.Fatalf("FinishReason mismatch: drainStream=%q, streamWithEvents=%q", got1.FinishReason, got2.FinishReason)
	}
}

func TestStreamWithEventsEmitsAdvisorThinkingChunksWithAdvisorSource(t *testing.T) {
	chunks := []provider.ChatChunk{
		{Delta: provider.Message{Content: ""}, Thinking: "advisor reasoning"},
		{Done: true, Delta: provider.Message{Content: "advisor answer"}, FinishReason: "stop"},
	}

	ch := make(chan provider.ChatChunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)

	sink := &toolSink{}
	resp, err := streamWithEvents(ch, sink, output.ChunkSourceAdvisor)

	if err != nil {
		t.Fatalf("streamWithEvents() error = %v, want nil", err)
	}
	if resp.Message.Content != "advisor answer" {
		t.Fatalf("response content = %q, want %q", resp.Message.Content, "advisor answer")
	}

	// Verify that the thinking chunk has ChunkSourceAdvisor.
	if len(sink.events) != 1 {
		t.Fatalf("event count = %d, want 1", len(sink.events))
	}
	if payload, ok := sink.events[0].Payload.(output.ThinkingChunkEvent); !ok {
		t.Fatalf("event payload type = %T, want ThinkingChunkEvent", sink.events[0].Payload)
	} else if payload.Source != output.ChunkSourceAdvisor {
		t.Fatalf("thinking chunk source = %q, want %q", payload.Source, output.ChunkSourceAdvisor)
	}
}
