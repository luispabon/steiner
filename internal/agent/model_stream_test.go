package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
	var streamed strings.Builder
	for _, event := range events {
		payload, ok := event.Payload.(output.AssistantChunkEvent)
		if !ok {
			continue
		}
		streamed.WriteString(payload.Content)
		if payload.Content == "ignored" {
			t.Fatal("chunk after the processing error was processed")
		}
	}
	if got, want := streamed.String(), "first"; got != want {
		t.Fatalf("emitted content = %q, want %q preserved through the retirement drain", got, want)
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

// TestConsumeModelStreamFinalChunkReconcilesStreamedContent proves the text
// accumulated across deltas is what final-chunk reconciliation compares against:
// a final chunk extending the streamed text emits only its unseen suffix and
// leaves the response carrying the full text.
func TestConsumeModelStreamFinalChunkReconcilesStreamedContent(t *testing.T) {
	chunks := make(chan provider.ChatChunk, 2)
	chunks <- provider.ChatChunk{Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "AB"}}
	chunks <- provider.ChatChunk{Done: true, Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "ABC"}}
	close(chunks)

	var emitted strings.Builder
	response, err := consumeModelStream(context.Background(), output.SinkFunc(func(event output.Event) {
		if chunk, ok := event.Payload.(output.AssistantChunkEvent); ok {
			emitted.WriteString(chunk.Content)
		}
	}), 1, chunks, output.ChunkSourceAssistant, nil)
	if err != nil {
		t.Fatalf("consumeModelStream() error = %v", err)
	}
	if got, want := response.Message.Content, "ABC"; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
	if got, want := emitted.String(), "ABC"; got != want {
		t.Fatalf("emitted content = %q, want %q (streamed prefix plus unseen suffix, each once)", got, want)
	}
}

// TestConsumeModelStreamDeltaAfterFinalChunkContinuesReconciledContent guards
// the accumulator hand-off at the final chunk: a delta arriving after it must
// continue the reconciled text rather than the pre-final streamed prefix. Both a
// final chunk that extends the streamed text and a final chunk carrying a
// content snapshot settle on "ABC"; without the builder re-seed a later delta
// appends to the stale "AB" prefix, so every case fails on the response text.
func TestConsumeModelStreamDeltaAfterFinalChunkContinuesReconciledContent(t *testing.T) {
	tests := []struct {
		name        string
		finalChunk  provider.ChatChunk
		tail        string
		wantContent string
		wantEmitted string
	}{
		{
			name:        "extending final chunk",
			finalChunk:  provider.ChatChunk{Done: true, Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "ABC"}},
			tail:        "DEF",
			wantContent: "ABCDEF",
			wantEmitted: "AB|C|DEF",
		},
		{
			name: "content snapshot final chunk",
			finalChunk: provider.ChatChunk{
				Done:            true,
				ContentSnapshot: true,
				Delta:           provider.Message{Role: provider.MessageRoleAssistant, Content: "ABC"},
			},
			tail:        "D",
			wantContent: "ABCD",
			wantEmitted: "AB|C|D",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := make(chan provider.ChatChunk, 3)
			chunks <- provider.ChatChunk{Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "AB"}}
			chunks <- tt.finalChunk
			chunks <- provider.ChatChunk{Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: tt.tail}}
			close(chunks)

			var emitted []string
			response, err := consumeModelStream(context.Background(), output.SinkFunc(func(event output.Event) {
				if chunk, ok := event.Payload.(output.AssistantChunkEvent); ok {
					emitted = append(emitted, chunk.Content)
				}
			}), 1, chunks, output.ChunkSourceAssistant, nil)
			if err != nil {
				t.Fatalf("consumeModelStream() error = %v", err)
			}
			if got, want := response.Message.Content, tt.wantContent; got != want {
				t.Fatalf("response content = %q, want %q (delta after the final chunk continues the reconciled text)", got, want)
			}
			if got, want := strings.Join(emitted, "|"), tt.wantEmitted; got != want {
				t.Fatalf("emitted assistant chunks = %q, want %q (each span emitted once, in order)", got, want)
			}
		})
	}
}

// TestConsumeModelStreamEmitsPartialContentOnError proves the zero-response
// contract survives a stream that fails after content streamed: the partial
// text still reaches the sink, and the caller gets the failure with an empty
// response.
func TestConsumeModelStreamEmitsPartialContentOnError(t *testing.T) {
	upstreamErr := errors.New("upstream failed")
	partialChunks := func(withErrorChunk bool) <-chan provider.ChatChunk {
		chunks := make(chan provider.ChatChunk, 3)
		chunks <- provider.ChatChunk{Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "partial "}}
		chunks <- provider.ChatChunk{Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "text"}}
		if withErrorChunk {
			chunks <- provider.ChatChunk{Done: true, Error: upstreamErr.Error(), OriginalError: upstreamErr}
		}
		close(chunks)
		return chunks
	}

	tests := []struct {
		name           string
		ctx            func() context.Context
		withErrorChunk bool
		want           error
	}{
		{
			name:           "error chunk after content",
			ctx:            context.Background,
			withErrorChunk: true,
			want:           upstreamErr,
		},
		{
			name: "closed stream without final chunk under canceled context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			want: context.Canceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var emitted strings.Builder
			response, err := consumeModelStream(tt.ctx(), output.SinkFunc(func(event output.Event) {
				if chunk, ok := event.Payload.(output.AssistantChunkEvent); ok {
					emitted.WriteString(chunk.Content)
				}
			}), 1, partialChunks(tt.withErrorChunk), output.ChunkSourceAssistant, nil)
			if !errors.Is(err, tt.want) {
				t.Fatalf("consumeModelStream() error = %v, want %v", err, tt.want)
			}
			if got, want := emitted.String(), "partial text"; got != want {
				t.Errorf("emitted content = %q, want partial content %q preserved", got, want)
			}
			if !reflect.DeepEqual(response, provider.ChatResponse{}) {
				t.Errorf("response = %+v, want zero response", response)
			}
		})
	}
}
