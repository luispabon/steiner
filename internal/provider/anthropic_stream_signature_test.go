package provider

import (
	"context"
	"strings"
	"testing"
)

func TestDecodeAnthropicStreamWithHandler_SignatureDeltaPopulatesThinkingSignature(t *testing.T) {
	stream := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"role":"assistant","usage":{"input_tokens":9}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me "}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_real"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":1}"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":1}`,
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
	final := chunks[len(chunks)-1]
	if !final.Done {
		t.Fatal("final chunk Done = false, want true")
	}
	if got, want := final.Delta.ReasoningContent, "let me think"; got != want {
		t.Fatalf("reasoning = %q, want %q", got, want)
	}
	meta := final.Delta.ProviderMetadata
	if meta == nil || meta.Anthropic == nil {
		t.Fatal("ProviderMetadata.Anthropic = nil, want thinking signature")
	}
	if got, want := meta.Anthropic.ThinkingSignature, "sig_real"; got != want {
		t.Fatalf("ThinkingSignature = %q, want %q", got, want)
	}
	if len(final.Delta.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(final.Delta.ToolCalls))
	}
}
