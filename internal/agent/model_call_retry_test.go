package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func TestConsumeModelStreamResetsStateOnRetryReset(t *testing.T) {
	chunks := make(chan provider.ChatChunk, 4)
	chunks <- provider.ChatChunk{
		Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "first"},
	}
	chunks <- provider.ChatChunk{
		RetryReset:       true,
		Diagnostic:       "retrying attempt 2/2",
		Severity:         "warning",
		RetryAttempt:     2,
		RetryMaxAttempts: 2,
		RetryDelay:       time.Second,
	}
	chunks <- provider.ChatChunk{
		Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "second"},
	}
	chunks <- provider.ChatChunk{
		Done:         true,
		FinishReason: "stop",
		Delta:        provider.Message{Role: provider.MessageRoleAssistant, Content: "second"},
	}
	close(chunks)

	var events []output.Event
	var streamedChunks []string
	var firstChunkTime time.Time
	resp, err := consumeModelStream(context.Background(), output.SinkFunc(func(event output.Event) {
		events = append(events, event)
		if chunk, ok := event.Payload.(output.AssistantChunkEvent); ok {
			streamedChunks = append(streamedChunks, chunk.Content)
		}
	}), 1, chunks, output.ChunkSourceAssistant, &firstChunkTime)
	if err != nil {
		t.Fatalf("consumeModelStream() error = %v", err)
	}
	if got, want := resp.Message.Content, "second"; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
	if got, want := resp.FinishReason, "stop"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}
	// firstChunkTime must be set by the post-retry "second" content chunk.
	if firstChunkTime.IsZero() {
		t.Fatal("firstChunkTime is zero after stream; want non-zero from post-retry content chunk")
	}
	// The discarded attempt's text streamed before the reset, and the retained
	// attempt's text streamed once more: the final chunk must not re-emit it.
	if got, want := strings.Join(streamedChunks, "|"), "first|second"; got != want {
		t.Fatalf("streamed assistant chunks = %q, want %q", got, want)
	}

	var sawDiagnostic bool
	for _, event := range events {
		payload, ok := event.Payload.(output.ProviderDiagnosticEvent)
		if !ok {
			continue
		}
		sawDiagnostic = true
		if payload.Message != "retrying attempt 2/2" {
			t.Fatalf("diagnostic message = %q, want %q", payload.Message, "retrying attempt 2/2")
		}
		if payload.Kind != output.DiagnosticKindRetry || payload.Attempt != 2 || payload.MaxAttempts != 2 || payload.Delay != "1s" {
			t.Fatalf("retry diagnostic = %+v, want kind retry, attempt 2, max attempts 2, delay 1s", payload)
		}
	}
	if !sawDiagnostic {
		t.Fatal("expected provider diagnostic event")
	}
}

// TestConsumeModelChunkResetsFirstChunkTimeOnRetryReset verifies that a
// RetryReset chunk zeroes out firstChunkOut so that TTFT reflects the
// post-retry attempt, not the discarded one.
func TestConsumeModelChunkResetsFirstChunkTimeOnRetryReset(t *testing.T) {
	sink := output.NoopSink{}
	response := provider.ChatResponse{}
	message := provider.Message{Role: provider.MessageRoleAssistant}
	sawFinal := false
	var streamed strings.Builder

	// Simulate a content chunk from the first (discarded) attempt.
	contentChunk := provider.ChatChunk{
		Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "first"},
	}
	var firstChunkTime time.Time
	if err := consumeModelChunk(sink, 1, output.ChunkSourceAssistant, contentChunk, &response, &message, &streamed, &sawFinal, &firstChunkTime); err != nil {
		t.Fatalf("consumeModelChunk(content) error = %v", err)
	}
	if firstChunkTime.IsZero() {
		t.Fatal("firstChunkTime not set after first content chunk")
	}

	// Now send the retry reset chunk — firstChunkTime must be cleared.
	retryChunk := provider.ChatChunk{RetryReset: true}
	if err := consumeModelChunk(sink, 1, output.ChunkSourceAssistant, retryChunk, &response, &message, &streamed, &sawFinal, &firstChunkTime); err != nil {
		t.Fatalf("consumeModelChunk(retry reset) error = %v", err)
	}
	if !firstChunkTime.IsZero() {
		t.Fatal("firstChunkTime not reset to zero after RetryReset chunk")
	}

	// Send a content chunk from the new attempt — firstChunkTime must be set again.
	secondChunk := provider.ChatChunk{
		Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "second"},
	}
	if err := consumeModelChunk(sink, 1, output.ChunkSourceAssistant, secondChunk, &response, &message, &streamed, &sawFinal, &firstChunkTime); err != nil {
		t.Fatalf("consumeModelChunk(second content) error = %v", err)
	}
	if firstChunkTime.IsZero() {
		t.Fatal("firstChunkTime not set after post-retry content chunk")
	}
}

// TestConsumeModelStreamReportsNonResetRetry proves a provider retry that
// discarded nothing, such as the claude CLI retrying its own API request,
// reaches the sink as one retry diagnostic and keeps the streamed content,
// unlike a RetryReset chunk.
func TestConsumeModelStreamReportsNonResetRetry(t *testing.T) {
	chunks := make(chan provider.ChatChunk, 4)
	chunks <- provider.ChatChunk{
		Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "first"},
	}
	chunks <- provider.ChatChunk{
		Diagnostic:       "claude CLI retrying API request after overloaded, attempt 1/10, next try in 2s",
		Severity:         "warning",
		RetryAttempt:     1,
		RetryMaxAttempts: 10,
		RetryDelay:       2 * time.Second,
	}
	chunks <- provider.ChatChunk{
		Done:            true,
		FinishReason:    "stop",
		Delta:           provider.Message{Role: provider.MessageRoleAssistant, Content: "first second"},
		ContentSnapshot: true,
	}
	close(chunks)

	var events []output.Event
	resp, err := consumeModelStream(context.Background(), output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	}), 1, chunks, output.ChunkSourceAssistant, nil)
	if err != nil {
		t.Fatalf("consumeModelStream() error = %v", err)
	}
	// The pre-retry content survives: the CLI retried before it produced any
	// output, so nothing was discarded.
	if got, want := resp.Message.Content, "first second"; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
	var retries []output.ProviderDiagnosticEvent
	for _, event := range events {
		payload, ok := event.Payload.(output.ProviderDiagnosticEvent)
		if !ok {
			continue
		}
		retries = append(retries, payload)
	}
	if len(retries) != 1 {
		t.Fatalf("diagostics = %+v, want exactly one retry event", retries)
	}
	if got := retries[0]; got.Kind != output.DiagnosticKindRetry || got.Attempt != 1 || got.MaxAttempts != 10 || got.Delay != "2s" {
		t.Fatalf("retry diagnostic = %+v, want kind retry, attempt 1, max attempts 10, delay 2s", got)
	}
	if got, want := retries[0].Message, "claude CLI retrying API request after overloaded, attempt 1/10, next try in 2s"; got != want {
		t.Fatalf("retry diagnostic message = %q, want %q", got, want)
	}
}
