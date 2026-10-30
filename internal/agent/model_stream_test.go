package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// TestConsumeModelStreamDrainsAfterProcessingError proves an early processing
// error is preserved but the remaining chunks are still drained until the
// producer closes the channel, so producer-side terminal work finishes first.
func TestConsumeModelStreamDrainsAfterProcessingError(t *testing.T) {
	upstreamErr := errors.New("upstream failed")
	chunks := make(chan provider.ChatChunk)
	delivered := make(chan struct{})
	proceed := make(chan struct{})

	go func() {
		chunks <- provider.ChatChunk{Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "first"}}
		chunks <- provider.ChatChunk{Done: true, Error: upstreamErr.Error(), OriginalError: upstreamErr}
		close(delivered)
		<-proceed
		// Received only if the consumer drains after the error, which is what
		// lets the producer reach close.
		chunks <- provider.ChatChunk{Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "ignored"}}
		close(chunks)
	}()

	type streamResult struct {
		response provider.ChatResponse
		err      error
	}
	results := make(chan streamResult, 1)
	var events []output.Event
	go func() {
		response, err := consumeModelStream(context.Background(), output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}), 1, chunks, output.ChunkSourceAssistant, nil)
		results <- streamResult{response: response, err: err}
	}()

	<-delivered
	select {
	case got := <-results:
		t.Fatalf("consumeModelStream returned before the producer closed the channel: response=%+v err=%v", got.response, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(proceed)

	got := <-results
	if !errors.Is(got.err, upstreamErr) {
		t.Fatalf("consumeModelStream() error = %v, want first error %v", got.err, upstreamErr)
	}
	if got.response.Message.Content != "" {
		t.Fatalf("response content = %q, want empty on error", got.response.Message.Content)
	}
	for _, event := range events {
		payload, ok := event.Payload.(output.AssistantChunkEvent)
		if !ok {
			continue
		}
		if payload.Content == "ignored" {
			t.Fatal("chunk after the processing error was processed")
		}
	}
}

// TestConsumeModelStreamMissingFinalChunkError proves a stream that closes
// without a final chunk reports the context error when the request context is
// done, and the generic missing-final error otherwise.
func TestConsumeModelStreamMissingFinalChunkError(t *testing.T) {
	partialStream := func() <-chan provider.ChatChunk {
		chunks := make(chan provider.ChatChunk, 1)
		chunks <- provider.ChatChunk{Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "partial"}}
		close(chunks)
		return chunks
	}

	tests := []struct {
		name     string
		ctx      func() context.Context
		want     error
		wantText string
	}{
		{
			name: "canceled context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			want: context.Canceled,
		},
		{
			name: "expired context",
			ctx: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				cancel()
				return ctx
			},
			want: context.DeadlineExceeded,
		},
		{
			name:     "live context",
			ctx:      context.Background,
			wantText: "stream completed without a final chunk",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := consumeModelStream(tt.ctx(), output.NoopSink{}, 1, partialStream(), output.ChunkSourceAssistant, nil)
			if tt.want != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("consumeModelStream() error = %v, want %v", err, tt.want)
				}
				return
			}
			if err == nil || err.Error() != tt.wantText {
				t.Fatalf("consumeModelStream() error = %v, want %q", err, tt.wantText)
			}
		})
	}
}
