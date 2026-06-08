package agent

import (
	"context"
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
		RetryReset: true,
		Diagnostic: "retrying attempt 2/2",
		Severity:   "warning",
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
	var firstChunkTime time.Time
	resp, err := consumeModelStream(context.Background(), output.SinkFunc(func(event output.Event) {
		events = append(events, event)
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

	// Simulate a content chunk from the first (discarded) attempt.
	contentChunk := provider.ChatChunk{
		Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "first"},
	}
	var firstChunkTime time.Time
	if err := consumeModelChunk(sink, 1, output.ChunkSourceAssistant, contentChunk, &response, &message, &sawFinal, &firstChunkTime); err != nil {
		t.Fatalf("consumeModelChunk(content) error = %v", err)
	}
	if firstChunkTime.IsZero() {
		t.Fatal("firstChunkTime not set after first content chunk")
	}

	// Now send the retry reset chunk — firstChunkTime must be cleared.
	retryChunk := provider.ChatChunk{RetryReset: true}
	if err := consumeModelChunk(sink, 1, output.ChunkSourceAssistant, retryChunk, &response, &message, &sawFinal, &firstChunkTime); err != nil {
		t.Fatalf("consumeModelChunk(retry reset) error = %v", err)
	}
	if !firstChunkTime.IsZero() {
		t.Fatal("firstChunkTime not reset to zero after RetryReset chunk")
	}

	// Send a content chunk from the new attempt — firstChunkTime must be set again.
	secondChunk := provider.ChatChunk{
		Delta: provider.Message{Role: provider.MessageRoleAssistant, Content: "second"},
	}
	if err := consumeModelChunk(sink, 1, output.ChunkSourceAssistant, secondChunk, &response, &message, &sawFinal, &firstChunkTime); err != nil {
		t.Fatalf("consumeModelChunk(second content) error = %v", err)
	}
	if firstChunkTime.IsZero() {
		t.Fatal("firstChunkTime not set after post-retry content chunk")
	}
}
