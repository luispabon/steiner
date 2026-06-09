package provider

import (
	"context"
	"strings"
	"testing"
)

func TestDecodeAnthropicStreamWithHandler_EmitsTextThinkingToolUseAndFinalChunk(t *testing.T) {
	stream := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"role":"assistant","usage":{"input_tokens":9}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan "}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello "}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"weather\"}"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	var chunks []ChatChunk
	err := decodeAnthropicStreamWithHandler(context.Background(), strings.NewReader(stream), func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("decodeAnthropicStreamWithHandler() error = %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(chunks))
	}
	if got, want := chunks[0].Thinking, "plan "; got != want {
		t.Fatalf("thinking chunk = %q, want %q", got, want)
	}
	if got, want := chunks[1].Delta.Content, "hello "; got != want {
		t.Fatalf("text chunk = %q, want %q", got, want)
	}

	final := chunks[2]
	if !final.Done {
		t.Fatal("final chunk Done = false, want true")
	}
	if got, want := final.Delta.Content, "hello "; got != want {
		t.Fatalf("final content = %q, want %q", got, want)
	}
	if got, want := final.Delta.ReasoningContent, "plan "; got != want {
		t.Fatalf("final reasoning = %q, want %q", got, want)
	}
	if got, want := final.FinishReason, "tool_calls"; got != want {
		t.Fatalf("final finish reason = %q, want %q", got, want)
	}
	if len(final.Delta.ToolCalls) != 1 {
		t.Fatalf("final tool calls = %#v, want one call", final.Delta.ToolCalls)
	}
	if got, want := final.Delta.ToolCalls[0].ID, "toolu_1"; got != want {
		t.Fatalf("tool call id = %q, want %q", got, want)
	}
	if got, want := final.Delta.ToolCalls[0].Name, "lookup"; got != want {
		t.Fatalf("tool call name = %q, want %q", got, want)
	}
	if got, want := final.Delta.ToolCalls[0].Arguments["query"], "weather"; got != want {
		t.Fatalf("tool call query = %v, want %q", got, want)
	}
	if final.Usage == nil {
		t.Fatal("final usage = nil, want usage stats")
	}
	if got, want := final.Usage.PromptTokens, 9; got != want {
		t.Fatalf("prompt tokens = %d, want %d", got, want)
	}
	if got, want := final.Usage.CompletionTokens, 4; got != want {
		t.Fatalf("completion tokens = %d, want %d", got, want)
	}
}

func TestDecodeAnthropicStreamWithHandler_ErrorEventEmitsTerminalErrorChunk(t *testing.T) {
	stream := strings.Join([]string{
		"event: error",
		`data: {"type":"error","error":{"type":"api_error","message":"provider unavailable"}}`,
		"",
	}, "\n")

	var chunks []ChatChunk
	err := decodeAnthropicStreamWithHandler(context.Background(), strings.NewReader(stream), func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("decodeAnthropicStreamWithHandler() error = %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	if !chunks[0].Done {
		t.Fatal("Done = false, want true")
	}
	if got, want := chunks[0].Error, "provider unavailable"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
