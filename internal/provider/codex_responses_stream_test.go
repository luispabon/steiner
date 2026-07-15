package provider

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestResponsesStreamReasoningSummaryDelta(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking...\"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{}}}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectResponsesStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeResponsesStreamWithHandler() error = %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Thinking != "thinking..." {
		t.Fatalf("chunks[0].Thinking = %q, want %q", chunks[0].Thinking, "thinking...")
	}
	if chunks[1].Delta.Content != "answer" {
		t.Fatalf("chunks[1].Delta.Content = %q, want %q", chunks[1].Delta.Content, "answer")
	}
	final := chunks[len(chunks)-1]
	if !final.Done {
		t.Fatal("expected final chunk Done = true")
	}
	if final.Delta.ReasoningContent != "thinking..." {
		t.Fatalf("final ReasoningContent = %q, want %q", final.Delta.ReasoningContent, "thinking...")
	}
}

func TestResponsesStreamReasoningTextDelta(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"raw thinking\"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{}}}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectResponsesStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeResponsesStreamWithHandler() error = %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Thinking != "raw thinking" {
		t.Fatalf("chunks[0].Thinking = %q, want %q", chunks[0].Thinking, "raw thinking")
	}
	final := chunks[len(chunks)-1]
	if final.Delta.ReasoningContent != "raw thinking" {
		t.Fatalf("final ReasoningContent = %q, want %q", final.Delta.ReasoningContent, "raw thinking")
	}
}

func TestResponsesStreamReasoningItemID(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_123\"}}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{}}}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectResponsesStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeResponsesStreamWithHandler() error = %v", err)
	}
	final := chunks[len(chunks)-1]
	if final.Delta.ProviderMetadata == nil || final.Delta.ProviderMetadata.Codex == nil {
		t.Fatal("expected ProviderMetadata.Codex to be set")
	}
	if final.Delta.ProviderMetadata.Codex.ReasoningID != "rs_123" {
		t.Fatalf("ReasoningID = %q, want %q", final.Delta.ProviderMetadata.Codex.ReasoningID, "rs_123")
	}
}

func TestResponsesStreamNoReasoning(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{}}}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectResponsesStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeResponsesStreamWithHandler() error = %v", err)
	}
	final := chunks[len(chunks)-1]
	if final.Delta.ReasoningContent != "" {
		t.Fatalf("expected empty ReasoningContent, got %q", final.Delta.ReasoningContent)
	}
	if final.Delta.ProviderMetadata != nil {
		t.Fatalf("expected nil ProviderMetadata, got %+v", final.Delta.ProviderMetadata)
	}
}

func collectResponsesStreamChunks(t *testing.T, body io.Reader) ([]ChatChunk, error) {
	t.Helper()

	var chunks []ChatChunk
	err := decodeResponsesStreamWithHandler(context.Background(), body, func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	return chunks, err
}
