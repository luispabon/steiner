package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

const modelStreamTestTimeout = 2 * time.Second

// TestConsumeModelStreamDrainsAfterProcessingError proves an early processing
// error is preserved but, on a request carrying a backend state retirement
// signal, the remaining chunks are drained until the producer closes the
// channel so producer-side terminal work finishes first.
func TestConsumeModelStreamDrainsAfterProcessingError(t *testing.T) {
	upstreamErr := errors.New("upstream failed")
	ctx := provider.WithBackendStateRetirement(context.Background())
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
		response, err := consumeModelStream(ctx, output.SinkFunc(func(event output.Event) {
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

	var got streamResult
	select {
	case got = <-results:
	case <-time.After(modelStreamTestTimeout):
		t.Fatal("consumeModelStream did not return after the producer closed the channel")
	}
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

// TestConsumeModelStreamProcessingErrorWithoutRetirementSignalReturnsImmediately
// proves a request that carries no backend state retirement signal keeps the
// original behavior: the first processing error returns without waiting for the
// producer, whether or not the request context is canceled.
func TestConsumeModelStreamProcessingErrorWithoutRetirementSignalReturnsImmediately(t *testing.T) {
	upstreamErr := errors.New("upstream failed")
	tests := []struct {
		name string
		ctx  func() context.Context
	}{
		{
			name: "live context",
			ctx:  context.Background,
		},
		{
			name: "canceled context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := make(chan provider.ChatChunk)
			release := make(chan struct{})
			defer close(release)

			go func() {
				chunks <- provider.ChatChunk{Done: true, Error: upstreamErr.Error(), OriginalError: upstreamErr}
				<-release
				close(chunks)
			}()

			results := make(chan error, 1)
			go func() {
				_, err := consumeModelStream(tt.ctx(), output.NoopSink{}, 1, chunks, output.ChunkSourceAssistant, nil)
				results <- err
			}()

			select {
			case err := <-results:
				if !errors.Is(err, upstreamErr) {
					t.Fatalf("consumeModelStream() error = %v, want first error %v", err, upstreamErr)
				}
			case <-time.After(modelStreamTestTimeout):
				t.Fatal("consumeModelStream waited for a producer it had no reason to drain")
			}
		})
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
