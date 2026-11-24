package provider

import (
	"context"
	"errors"
	"io"
	"slices"
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

func TestResponsesStreamEmptyFinalTextKeepsStreamed(t *testing.T) {
	const deltas = `data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hel"}` + "\n\n" +
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"lo"}` + "\n\n"
	const done = "data: [DONE]\n\n"
	tests := []struct {
		name         string
		body         string
		wantStreamed string
		wantContent  string
		wantTool     string
	}{
		{
			name:         "completed message with empty content array",
			body:         deltas + `data: {"type":"response.completed","response":{"output":[{"type":"message","id":"msg_1","role":"assistant","content":[]}],"usage":{}}}` + "\n\n" + done,
			wantStreamed: "Hello",
			wantContent:  "Hello",
		},
		{
			name:         "completed message with empty output_text part",
			body:         deltas + `data: {"type":"response.completed","response":{"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":""}]}],"usage":{}}}` + "\n\n" + done,
			wantStreamed: "Hello",
			wantContent:  "Hello",
		},
		{
			name:         "completed call kept alongside streamed text",
			body:         deltas + `data: {"type":"response.completed","response":{"output":[{"type":"message","id":"msg_1","role":"assistant","content":[]},{"type":"function_call","id":"fc_1","call_id":"c1","name":"read","arguments":"{}"}],"usage":{}}}` + "\n\n" + done,
			wantStreamed: "Hello",
			wantContent:  "Hello",
			wantTool:     "read",
		},
		{
			name:         "output_item.done with empty content keeps streamed text",
			body:         deltas + `data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}` + "\n\n" + `data: {"type":"response.completed","response":{"output":[],"usage":{}}}` + "\n\n" + done,
			wantStreamed: "Hello",
			wantContent:  "Hello",
		},
		{
			name:         "output_text.done with empty text keeps streamed text",
			body:         deltas + `data: {"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":""}` + "\n\n" + `data: {"type":"response.completed","response":{"output":[],"usage":{}}}` + "\n\n" + done,
			wantStreamed: "Hello",
			wantContent:  "Hello",
		},
		{
			name:         "non-empty final text still wins over streamed text",
			body:         `data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hxl"}` + "\n\n" + `data: {"type":"response.completed","response":{"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}],"usage":{}}}` + "\n\n" + done,
			wantStreamed: "Hxl",
			wantContent:  "Hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, err := collectResponsesStreamChunks(t, strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("decodeResponsesStreamWithHandler() error = %v", err)
			}
			if len(chunks) == 0 {
				t.Fatal("no chunks emitted")
			}
			var streamed strings.Builder
			for _, chunk := range chunks[:len(chunks)-1] {
				streamed.WriteString(chunk.Delta.Content)
			}
			if streamed.String() != tt.wantStreamed {
				t.Fatalf("streamed content = %q, want %q", streamed.String(), tt.wantStreamed)
			}
			final := chunks[len(chunks)-1]
			if final.Delta.Content != tt.wantContent {
				t.Fatalf("final Content = %q, want %q", final.Delta.Content, tt.wantContent)
			}
			if tt.wantTool != "" && (len(final.Delta.ToolCalls) != 1 || final.Delta.ToolCalls[0].Name != tt.wantTool) {
				t.Fatalf("ToolCalls = %#v, want one call %q", final.Delta.ToolCalls, tt.wantTool)
			}
		})
	}
}

// Streamed text without an output index keeps its place before the final calls.
func TestResponsesStreamKeepsStreamedTextBeforeCalls(t *testing.T) {
	body := strings.NewReader(
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Let me check.\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[]},{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"c1\",\"name\":\"read\",\"arguments\":\"{}\"}],\"usage\":{}}}\n\n" +
			"data: [DONE]\n\n")
	chunks, err := collectResponsesStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeResponsesStreamWithHandler() error = %v", err)
	}
	final := chunks[len(chunks)-1].Delta
	if final.ProviderMetadata == nil || final.ProviderMetadata.Codex == nil {
		t.Fatalf("final ProviderMetadata = %#v, want Codex blocks", final.ProviderMetadata)
	}
	want := []CodexMessageBlock{{Kind: "message", Text: "Let me check."}, {Kind: "function_call", CallID: "c1"}}
	if got := final.ProviderMetadata.Codex.Blocks; !slices.Equal(got, want) {
		t.Fatalf("Blocks = %#v, want %#v", got, want)
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

func TestResponsesStreamReasoningCompletedOnly(t *testing.T) {
	// F386: reasoning only in completed payload, no reasoning deltas
	body := strings.NewReader(
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"reasoning\",\"id\":\"rs_456\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"thinking in completed\"}]}],\"usage\":{}}}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectResponsesStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeResponsesStreamWithHandler() error = %v", err)
	}
	final := chunks[len(chunks)-1]
	if final.Delta.ReasoningContent != "thinking in completed" {
		t.Fatalf("final ReasoningContent = %q, want %q", final.Delta.ReasoningContent, "thinking in completed")
	}
	if final.Delta.ProviderMetadata == nil || final.Delta.ProviderMetadata.Codex == nil {
		t.Fatal("expected ProviderMetadata.Codex to be set")
	}
	if final.Delta.ProviderMetadata.Codex.ReasoningID != "rs_456" {
		t.Fatalf("ReasoningID = %q, want %q", final.Delta.ProviderMetadata.Codex.ReasoningID, "rs_456")
	}
}

type errorAfterReader struct {
	reader io.Reader
	err    error
}

func (reader errorAfterReader) Read(p []byte) (int, error) {
	n, err := reader.reader.Read(p)
	if n > 0 {
		return n, nil
	}
	if errors.Is(err, io.EOF) {
		return 0, reader.err
	}
	if err != nil {
		return 0, err
	}
	return 0, reader.err
}

func TestResponsesStreamFailureFlushesPendingText(t *testing.T) {
	failure := errors.New("injected read failure")
	body := codexSSE(
		`{"type":"response.output_text.delta","item_id":"m1","output_index":0,"delta":"note"}`,
		`{"type":"response.output_text.delta","item_id":"m2","output_index":1,"delta":"\n"}`,
	)
	var emitted strings.Builder
	var chunks []ChatChunk
	err := decodeResponsesStreamWithHandler(context.Background(), errorAfterReader{reader: strings.NewReader(body), err: failure}, func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		if !chunk.Done {
			emitted.WriteString(chunk.Delta.Content)
		}
		return nil
	})
	if !errors.Is(err, failure) || emitted.String() != "note\n\n" {
		t.Fatalf("error = %v, emitted = %q", err, emitted.String())
	}

	emitErr := errors.New("emit failed")
	emitted.Reset()
	err = decodeResponsesStreamWithHandler(context.Background(), errorAfterReader{reader: strings.NewReader(body), err: failure}, func(chunk ChatChunk) error {
		emitted.WriteString(chunk.Delta.Content)
		if chunk.Delta.Content != "note" {
			return emitErr
		}
		return nil
	})
	if !errors.Is(err, emitErr) || errors.Is(err, failure) {
		t.Fatalf("error = %v, want emit error over read error", err)
	}
}

func TestResponsesFailedEventFlushesPendingText(t *testing.T) {
	frames := codexSSE(
		`{"type":"response.output_text.delta","item_id":"m1","output_index":0,"delta":"note"}`,
		`{"type":"response.output_text.delta","item_id":"m2","output_index":1,"delta":"\n"}`,
		`{"type":"response.failed","response":{"error":{"message":"provider failed"}}}`,
	)
	var emitted strings.Builder
	err := decodeResponsesStreamWithHandler(context.Background(), strings.NewReader(frames), func(chunk ChatChunk) error {
		emitted.WriteString(chunk.Delta.Content)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "provider failed") || emitted.String() != "note\n\n" {
		t.Fatalf("error = %v, emitted = %q", err, emitted.String())
	}
	emitErr := errors.New("emit failed")
	err = decodeResponsesStreamWithHandler(context.Background(), strings.NewReader(frames), func(chunk ChatChunk) error {
		if chunk.Delta.Content != "note" {
			return emitErr
		}
		return nil
	})
	if !errors.Is(err, emitErr) {
		t.Fatalf("error = %v, want emit error", err)
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
