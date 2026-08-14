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

func TestResponsesStreamReasoningPartBoundaries(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.reasoning_summary_part.added\"}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"**First part fragment\"}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\" one**\"}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_text.done\"}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_part.done\"}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_part.added\"}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"\"}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"**Second part**\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"output\":[],\"usage\":{}}}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectResponsesStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeResponsesStreamWithHandler() error = %v", err)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}
	if chunks[0].Thinking != "**First part fragment" {
		t.Fatalf("chunks[0].Thinking = %q, want %q", chunks[0].Thinking, "**First part fragment")
	}
	if chunks[1].Thinking != " one**" {
		t.Fatalf("chunks[1].Thinking = %q, want %q", chunks[1].Thinking, " one**")
	}
	if chunks[2].Thinking != "\n**Second part**" {
		t.Fatalf("chunks[2].Thinking = %q, want %q", chunks[2].Thinking, "\n**Second part**")
	}
	final := chunks[len(chunks)-1]
	if final.Delta.ReasoningContent != "**First part fragment one**\n**Second part**" {
		t.Fatalf("final ReasoningContent = %q, want %q", final.Delta.ReasoningContent, "**First part fragment one**\n**Second part**")
	}
}

func TestResponsesStreamReasoningNoDoubleNewline(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"**First**\\n\"}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_part.added\"}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"**Second**\"}\n\n" +
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
	if chunks[0].Thinking != "**First**\n" {
		t.Fatalf("chunks[0].Thinking = %q, want %q", chunks[0].Thinking, "**First**\n")
	}
	if chunks[1].Thinking != "**Second**" {
		t.Fatalf("chunks[1].Thinking = %q, want %q", chunks[1].Thinking, "**Second**")
	}
}

func TestResponsesStreamReasoningItemDoneBoundary(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"**one**\"}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_123\"}}\n\n" +
			"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"**two**\"}\n\n" +
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
	if chunks[1].Thinking != "\n**two**" {
		t.Fatalf("chunks[1].Thinking = %q, want %q", chunks[1].Thinking, "\n**two**")
	}
}

func TestResponsesStreamReasoningTextItemBoundary(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"raw part one\"}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"id\":\"rs_1\"}}\n\n" +
			"data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"raw part two\"}\n\n" +
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
	if chunks[0].Thinking != "raw part one" {
		t.Fatalf("chunks[0].Thinking = %q, want %q", chunks[0].Thinking, "raw part one")
	}
	if chunks[1].Thinking != "\nraw part two" {
		t.Fatalf("chunks[1].Thinking = %q, want %q", chunks[1].Thinking, "\nraw part two")
	}
	final := chunks[len(chunks)-1]
	if final.Delta.ReasoningContent != "raw part one\nraw part two" {
		t.Fatalf("final ReasoningContent = %q, want %q", final.Delta.ReasoningContent, "raw part one\nraw part two")
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
