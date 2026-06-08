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
