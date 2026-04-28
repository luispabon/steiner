package provider

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
)

// readSSEEvent tests

func TestOpenAIStreamReadSSEEvent_SingleLine(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("data: hello world\n\n"))
	event, err := readSSEEvent(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != "hello world" {
		t.Fatalf("got %q, want %q", event, "hello world")
	}
}

func TestOpenAIStreamReadSSEEvent_MultiLine(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("data: line1\ndata: line2\n\n"))
	event, err := readSSEEvent(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != "line1\nline2" {
		t.Fatalf("got %q, want %q", event, "line1\nline2")
	}
}

func TestOpenAIStreamReadSSEEvent_DoneEvent(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("data: [DONE]\n\n"))
	event, err := readSSEEvent(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != "[DONE]" {
		t.Fatalf("got %q, want %q", event, "[DONE]")
	}
}

func TestOpenAIStreamReadSSEEvent_EmptyStreamEOF(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(""))
	event, err := readSSEEvent(reader)
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
	if event != "" {
		t.Fatalf("expected empty event, got %q", event)
	}
}

func TestOpenAIStreamReadSSEEvent_IgnoresNonDataLines(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("event: ping\ndata: hello\n\n"))
	event, err := readSSEEvent(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if event != "hello" {
		t.Fatalf("got %q, want %q", event, "hello")
	}
}

// extractThinkingDelta tests

func TestOpenAIStreamExtractThinkingDelta_ThinkingType(t *testing.T) {
	value := []any{
		map[string]any{"type": "thinking", "thinking": "let me think..."},
	}
	result := extractThinkingDelta(value)
	if result != "let me think..." {
		t.Fatalf("got %q, want %q", result, "let me think...")
	}
}

func TestOpenAIStreamExtractThinkingDelta_ThinkingDeltaType(t *testing.T) {
	value := []any{
		map[string]any{"type": "thinking_delta", "thinking": "continuing..."},
	}
	result := extractThinkingDelta(value)
	if result != "continuing..." {
		t.Fatalf("got %q, want %q", result, "continuing...")
	}
}

func TestOpenAIStreamExtractThinkingDelta_MultipleBlocks(t *testing.T) {
	value := []any{
		map[string]any{"type": "thinking", "thinking": "first "},
		map[string]any{"type": "thinking_delta", "thinking": "second"},
	}
	result := extractThinkingDelta(value)
	if result != "first second" {
		t.Fatalf("got %q, want %q", result, "first second")
	}
}

func TestOpenAIStreamExtractThinkingDelta_ReturnsEmptyForPlainString(t *testing.T) {
	result := extractThinkingDelta("plain string")
	if result != "" {
		t.Fatalf("got %q, want empty", result)
	}
}

func TestOpenAIStreamExtractThinkingDelta_ReturnsEmptyForNonArray(t *testing.T) {
	result := extractThinkingDelta(42)
	if result != "" {
		t.Fatalf("got %q, want empty", result)
	}
}

func TestOpenAIStreamExtractThinkingDelta_SkipsNonThinkingTypes(t *testing.T) {
	value := []any{
		map[string]any{"type": "text", "text": "hello"},
	}
	result := extractThinkingDelta(value)
	if result != "" {
		t.Fatalf("got %q, want empty", result)
	}
}

func TestOpenAIStreamExtractThinkingDelta_SkipsNonMapItems(t *testing.T) {
	value := []any{
		"not a map",
	}
	result := extractThinkingDelta(value)
	if result != "" {
		t.Fatalf("got %q, want empty", result)
	}
}

// finalizeToolCalls tests

func TestOpenAIStreamFinalizeToolCalls_ValidJSONArguments(t *testing.T) {
	var args strings.Builder
	args.WriteString(`{"city":"London","units":"metric"}`)
	toolCalls := map[int]*openAIToolCallAccumulator{
		0: {ID: "call_1", Name: "get_weather", Arguments: args},
	}
	calls, err := finalizeToolCalls(toolCalls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].ID != "call_1" {
		t.Fatalf("ID = %q, want %q", calls[0].ID, "call_1")
	}
	if calls[0].Name != "get_weather" {
		t.Fatalf("Name = %q, want %q", calls[0].Name, "get_weather")
	}
	if city, ok := calls[0].Arguments["city"].(string); !ok || city != "London" {
		t.Fatalf("city = %v, want %q", calls[0].Arguments["city"], "London")
	}
}

func TestOpenAIStreamFinalizeToolCalls_MalformedJSON(t *testing.T) {
	var args strings.Builder
	args.WriteString(`{invalid}`)
	toolCalls := map[int]*openAIToolCallAccumulator{
		0: {ID: "call_1", Name: "get_weather", Arguments: args},
	}
	_, err := finalizeToolCalls(toolCalls)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestOpenAIStreamFinalizeToolCalls_EmptyArguments(t *testing.T) {
	toolCalls := map[int]*openAIToolCallAccumulator{
		0: {ID: "call_1", Name: "get_weather"},
	}
	calls, err := finalizeToolCalls(toolCalls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if len(calls[0].Arguments) != 0 {
		t.Fatalf("expected empty arguments, got %v", calls[0].Arguments)
	}
}

func TestOpenAIStreamFinalizeToolCalls_EmptyMap(t *testing.T) {
	toolCalls := map[int]*openAIToolCallAccumulator{}
	calls, err := finalizeToolCalls(toolCalls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != nil {
		t.Fatalf("expected nil, got %v", calls)
	}
}

// flushStreamState tests

func TestOpenAIStreamFlushStreamState_EmitsDoneChunk(t *testing.T) {
	ctx := context.Background()
	out := make(chan ChatChunk, 1)
	state := openAIStreamState{
		finishReason: "stop",
		usage:        &UsageStats{TotalTokens: 42},
	}

	err := flushStreamState(ctx, out, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunk := <-out
	if !chunk.Done {
		t.Fatal("expected Done=true")
	}
	if chunk.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want %q", chunk.FinishReason, "stop")
	}
	if chunk.Usage == nil {
		t.Fatal("expected non-nil Usage")
	}
	if chunk.Usage.TotalTokens != 42 {
		t.Fatalf("TotalTokens = %d, want %d", chunk.Usage.TotalTokens, 42)
	}
}

func TestOpenAIStreamFlushStreamState_ReturnsNilWhenNothingSeen(t *testing.T) {
	ctx := context.Background()
	out := make(chan ChatChunk)
	state := openAIStreamState{}

	err := flushStreamState(ctx, out, state)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestOpenAIStreamFlushStreamState_EmitsContent(t *testing.T) {
	ctx := context.Background()
	var content strings.Builder
	content.WriteString("Hello")
	out := make(chan ChatChunk, 1)
	state := openAIStreamState{
		content:    content,
		sawContent: true,
	}

	err := flushStreamState(ctx, out, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	chunk := <-out
	if !chunk.Done {
		t.Fatal("expected Done=true")
	}
	if chunk.Delta.Content != "Hello" {
		t.Fatalf("Content = %q, want %q", chunk.Delta.Content, "Hello")
	}
}
