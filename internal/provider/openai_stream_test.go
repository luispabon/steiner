package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestOpenAIStreamCacheUsageFallbacks(t *testing.T) {
	payload := `{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":7},"prompt_cache_hit_tokens":12}}`
	var response openAIResponse
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		t.Fatal(err)
	}
	if response.Usage == nil || response.Usage.CacheReadInputTokens != 0 || response.Usage.CacheCreationInputTokens != 7 || response.Usage.PromptTokens != 100 {
		t.Fatalf("usage = %+v", response.Usage)
	}
}

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
	if !errors.Is(err, io.EOF) {
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

// extractTextDelta tests

func TestOpenAIStreamExtractTextDelta_PlainString(t *testing.T) {
	result := extractTextDelta("plain text")
	if result != "plain text" {
		t.Fatalf("got %q, want %q", result, "plain text")
	}
}

func TestOpenAIStreamExtractTextDelta_StructuredTextBlock(t *testing.T) {
	value := []any{
		map[string]any{"type": "text", "text": "hello world"},
	}
	result := extractTextDelta(value)
	if result != "hello world" {
		t.Fatalf("got %q, want %q", result, "hello world")
	}
}

func TestOpenAIStreamExtractTextDelta_MixedThinkingAndText(t *testing.T) {
	// F403: structured content with both thinking and text blocks
	value := []any{
		map[string]any{"type": "thinking", "thinking": "let me think..."},
		map[string]any{"type": "text", "text": "answer text"},
	}
	result := extractTextDelta(value)
	if result != "answer text" {
		t.Fatalf("got %q, want %q", result, "answer text")
	}
}

func TestOpenAIStreamExtractTextDelta_SkipsThinkingBlocks(t *testing.T) {
	value := []any{
		map[string]any{"type": "thinking", "thinking": "thinking only"},
	}
	result := extractTextDelta(value)
	if result != "" {
		t.Fatalf("got %q, want empty", result)
	}
}

func TestOpenAIStreamExtractTextDelta_MultipleTextBlocks(t *testing.T) {
	value := []any{
		map[string]any{"type": "text", "text": "hello "},
		map[string]any{"type": "text", "text": "world"},
	}
	result := extractTextDelta(value)
	if result != "hello world" {
		t.Fatalf("got %q, want %q", result, "hello world")
	}
}

func TestOpenAIStreamDecodeChatStreamWithHandler_MixedThinkingAndTextStructuredContent(t *testing.T) {
	// F403: structured content with both thinking and text blocks
	body := strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":[{\"type\":\"thinking\",\"thinking\":\"thinking text\"},{\"type\":\"text\",\"text\":\"answer text\"}]},\"finish_reason\":\"\"}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("chunks len = %d, want at least 2", len(chunks))
	}
	if chunks[0].Thinking != "thinking text" {
		t.Fatalf("first chunk Thinking = %q, want %q", chunks[0].Thinking, "thinking text")
	}
	if chunks[1].Delta.Content != "answer text" {
		t.Fatalf("second chunk Content = %q, want %q", chunks[1].Delta.Content, "answer text")
	}
	final := chunks[len(chunks)-1]
	if !final.Done {
		t.Fatal("final chunk Done = false, want true")
	}
}

func TestOpenAIStreamDecodeChatStreamWithHandler_UsesReasoningDetailsAndEOFWithFinalChunk(t *testing.T) {
	reasoning := "prefix "
	body := strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"prefix \",\"reasoning_details\":[{\"type\":\"text\",\"text\":\"suffix\"}]},\"finish_reason\":\"\"}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks len = %d, want 3", len(chunks))
	}
	if chunks[0].Thinking != reasoning+"suffix" {
		t.Fatalf("first chunk Thinking = %q, want %q", chunks[0].Thinking, reasoning+"suffix")
	}
	if chunks[1].Delta.Content != "answer" {
		t.Fatalf("second chunk content = %q, want %q", chunks[1].Delta.Content, "answer")
	}
	final := chunks[2]
	if !final.Done {
		t.Fatal("final chunk Done = false, want true")
	}
	if final.FinishReason != "stop" {
		t.Fatalf("final chunk FinishReason = %q, want %q", final.FinishReason, "stop")
	}
	if final.Delta.ReasoningContent != reasoning+"suffix" {
		t.Fatalf("final chunk ReasoningContent = %q, want %q", final.Delta.ReasoningContent, reasoning+"suffix")
	}
}

func TestOpenAIStreamDecodeChatStreamWithHandler_SeparatesIndexlessToolCalls(t *testing.T) {
	body := strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_first\",\"type\":\"function\",\"function\":{\"name\":\"first\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_second\",\"type\":\"function\",\"function\":{\"name\":\"second\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"2}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	final := chunks[len(chunks)-1]
	if got, want := len(final.Delta.ToolCalls), 2; got != want {
		t.Fatalf("tool calls len = %d, want %d", got, want)
	}
	for i, want := range []ToolCall{
		{ID: "call_first", Name: "first", Arguments: map[string]any{"value": float64(1)}},
		{ID: "call_second", Name: "second", Arguments: map[string]any{"value": float64(2)}},
	} {
		got := final.Delta.ToolCalls[i]
		if got.ID != want.ID || got.Name != want.Name || got.Arguments["value"] != want.Arguments["value"] {
			t.Fatalf("tool call %d = %#v, want %#v", i, got, want)
		}
	}
}

func TestOpenAIStreamDecodeChatStreamWithHandler_RoutesIndexlessToolCallsByPosition(t *testing.T) {
	body := strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_first\",\"type\":\"function\",\"function\":{\"name\":\"first\",\"arguments\":\"{\\\"value\\\":\"}},{\"id\":\"call_second\",\"type\":\"function\",\"function\":{\"name\":\"second\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"1}\"}},{\"function\":{\"arguments\":\"2}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	calls := chunks[len(chunks)-1].Delta.ToolCalls
	if got, want := len(calls), 2; got != want {
		t.Fatalf("tool calls len = %d, want %d", got, want)
	}
	for i, want := range []struct {
		id    string
		name  string
		value float64
	}{
		{id: "call_first", name: "first", value: 1},
		{id: "call_second", name: "second", value: 2},
	} {
		if got := calls[i]; got.ID != want.id || got.Name != want.name || got.Arguments["value"] != want.value {
			t.Fatalf("tool call %d = %#v, want ID %q, name %q, and value %v", i, got, want.id, want.name, want.value)
		}
	}
}

func TestOpenAIStreamDecodeChatStreamWithHandler_ReusesIndexlessToolCallID(t *testing.T) {
	body := strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_repeat\",\"type\":\"function\",\"function\":{\"name\":\"repeat\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_repeat\",\"function\":{\"arguments\":\"1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	calls := chunks[len(chunks)-1].Delta.ToolCalls
	if got, want := len(calls), 1; got != want {
		t.Fatalf("tool calls len = %d, want %d", got, want)
	}
	if got, want := calls[0].Arguments["value"], float64(1); got != want {
		t.Fatalf("tool call arguments = %#v, want value %v", calls[0].Arguments, want)
	}
}

func TestOpenAIToolCallIndexPresence_AnonymousIndexlessFragmentReceivesID(t *testing.T) {
	body := strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_lookup\",\"function\":{\"arguments\":\"1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	calls := chunks[len(chunks)-1].Delta.ToolCalls
	if got, want := len(calls), 1; got != want {
		t.Fatalf("tool calls len = %d, want %d", got, want)
	}
	if got, want := calls[0], (ToolCall{ID: "call_lookup", Name: "lookup", Arguments: map[string]any{"value": float64(1)}}); got.ID != want.ID || got.Name != want.Name || got.Arguments["value"] != want.Arguments["value"] {
		t.Fatalf("tool call = %#v, want %#v", got, want)
	}
}

func TestOpenAIToolCallIndexPresence_AnonymousIndexlessPromotedToIndexedFinalizesOnce(t *testing.T) {
	body := strings.NewReader(
		`data: {"choices":[{"delta":{"tool_calls":[{"type":"function","function":{"name":"lookup","arguments":"{\"value\":"}}]}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	calls := chunks[len(chunks)-1].Delta.ToolCalls
	if got, want := len(calls), 1; got != want {
		t.Fatalf("tool calls len = %d, want %d: %#v", got, want, calls)
	}
	if got, want := calls[0], (ToolCall{Name: "lookup", Arguments: map[string]any{"value": float64(1)}}); got.ID != want.ID || got.Name != want.Name || got.Arguments["value"] != want.Arguments["value"] {
		t.Fatalf("tool call = %#v, want %#v", got, want)
	}
}

func TestOpenAIToolCallIndexPresence_IndexedThenIndexlessIDReusesCall(t *testing.T) {
	body := strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_lookup\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"value\\\":\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_lookup\",\"function\":{\"arguments\":\"1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	calls := chunks[len(chunks)-1].Delta.ToolCalls
	if got, want := len(calls), 1; got != want {
		t.Fatalf("tool calls len = %d, want %d", got, want)
	}
	if got, want := calls[0], (ToolCall{ID: "call_lookup", Name: "lookup", Arguments: map[string]any{"value": float64(1)}}); got.ID != want.ID || got.Name != want.Name || got.Arguments["value"] != want.Arguments["value"] {
		t.Fatalf("tool call = %#v, want %#v", got, want)
	}
}

func TestOpenAIToolCallIndexPresence_IndexlessIDThenIndexedIDlessReusesCall(t *testing.T) {
	body := strings.NewReader(
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"value\":"}}]}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	calls := chunks[len(chunks)-1].Delta.ToolCalls
	if got, want := len(calls), 1; got != want {
		t.Fatalf("tool calls len = %d, want %d", got, want)
	}
	if got, want := calls[0], (ToolCall{ID: "call_lookup", Name: "lookup", Arguments: map[string]any{"value": float64(1)}}); got.ID != want.ID || got.Name != want.Name || got.Arguments["value"] != want.Arguments["value"] {
		t.Fatalf("tool call = %#v, want %#v", got, want)
	}
}

func TestOpenAIToolCallIndexPresence_IndexedIDlessSkipsParallelIndexlessCall(t *testing.T) {
	body := strings.NewReader(
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_a","type":"function","function":{"name":"a","arguments":"{\"value\":0}"}},{"id":"call_b","type":"function","function":{"name":"b","arguments":"{\"value\":1}"}}]}}]}` + "\n\n" +
			`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"extra\":true}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	calls := chunks[len(chunks)-1].Delta.ToolCalls
	if got, want := len(calls), 3; got != want {
		t.Fatalf("tool calls len = %d, want %d", got, want)
	}
	byID := make(map[string]ToolCall, len(calls))
	var orphan ToolCall
	for _, call := range calls {
		if call.ID == "" {
			orphan = call
			continue
		}
		byID[call.ID] = call
	}
	if got := byID["call_a"].Arguments["value"]; got != float64(0) {
		t.Fatalf("call_a value = %#v, want 0", got)
	}
	if got := byID["call_b"].Arguments["value"]; got != float64(1) {
		t.Fatalf("call_b value = %#v, want 1", got)
	}
	if _, merged := byID["call_a"].Arguments["extra"]; merged {
		t.Fatalf("indexed fragment merged into call_a at position 0: %#v", byID["call_a"].Arguments)
	}
	if got := orphan.Arguments["extra"]; got != true {
		t.Fatalf("orphan indexed call arguments = %#v, want extra true", orphan.Arguments)
	}
}

func TestOpenAIStreamDecodeChatStreamWithHandler_SeparatesIndexedZeroAndIndexlessToolCalls(t *testing.T) {
	body := strings.NewReader(
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_indexed\",\"type\":\"function\",\"function\":{\"name\":\"indexed\",\"arguments\":\"{\\\"value\\\":0}\"}}]}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"type\":\"function\",\"function\":{\"name\":\"indexless\",\"arguments\":\"{\\\"value\\\":1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
			"data: [DONE]\n\n",
	)

	chunks, err := collectOpenAIStreamChunks(t, body)
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	calls := chunks[len(chunks)-1].Delta.ToolCalls
	if got, want := len(calls), 2; got != want {
		t.Fatalf("tool calls len = %d, want %d", got, want)
	}
	for i, want := range []struct {
		name  string
		value float64
	}{
		{name: "indexed", value: 0},
		{name: "indexless", value: 1},
	} {
		if got := calls[i]; got.Name != want.name || got.Arguments["value"] != want.value {
			t.Fatalf("tool call %d = %#v, want name %q and value %v", i, got, want.name, want.value)
		}
	}
}

func TestOpenAIStreamDecodeChatStreamWithHandler_EOFBeforeFinalChunkIsRetryable(t *testing.T) {
	body := strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"\"}]}\n")

	chunks, err := collectOpenAIStreamChunks(t, body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("decodeChatStreamWithHandler() error = %v, want unexpected EOF", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected streamed content before EOF")
	}
}

// finalizeToolCalls tests

func TestOpenAIStreamFinalizeToolCalls_ValidJSONArguments(t *testing.T) {
	var args strings.Builder
	args.WriteString(`{"city":"London","units":"metric"}`)
	toolCalls := map[int][]*openAIToolCallAccumulator{
		0: {{ID: "call_1", Name: "get_weather", Arguments: args}},
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
	toolCalls := map[int][]*openAIToolCallAccumulator{
		0: {{ID: "call_1", Name: "get_weather", Arguments: args}},
	}
	_, err := finalizeToolCalls(toolCalls)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestOpenAIStreamFinalizeToolCalls_EmptyArguments(t *testing.T) {
	toolCalls := map[int][]*openAIToolCallAccumulator{
		0: {{ID: "call_1", Name: "get_weather"}},
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
	toolCalls := map[int][]*openAIToolCallAccumulator{}
	calls, err := finalizeToolCalls(toolCalls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != nil {
		t.Fatalf("expected nil, got %v", calls)
	}
}

func TestOpenAIStreamFinalizeToolCalls_TrailingCommaInArguments(t *testing.T) {
	var args strings.Builder
	args.WriteString(`{"operations":[{"type":"write","path":"foo.md"},]}`)
	toolCalls := map[int][]*openAIToolCallAccumulator{
		0: {{ID: "call_1", Name: "mutate", Arguments: args}},
	}
	calls, err := finalizeToolCalls(toolCalls)
	if err != nil {
		t.Fatalf("expected trailing comma to be sanitized, got error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].Name != "mutate" {
		t.Fatalf("Name = %q, want %q", calls[0].Name, "mutate")
	}
}

func TestOpenAIStreamFinalizeToolCalls_TrailingCommaInNestedObject(t *testing.T) {
	var args strings.Builder
	args.WriteString(`{"operations":[{"type":"write","path":"a.go","content":"x",},]}`)
	toolCalls := map[int][]*openAIToolCallAccumulator{
		0: {{ID: "call_1", Name: "mutate", Arguments: args}},
	}
	calls, err := finalizeToolCalls(toolCalls)
	if err != nil {
		t.Fatalf("expected nested trailing commas to be sanitized, got error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
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

	err := flushStreamState(func(chunk ChatChunk) error {
		select {
		case out <- chunk:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, state)
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
	state := openAIStreamState{}
	var emitted []ChatChunk

	err := flushStreamState(func(chunk ChatChunk) error {
		emitted = append(emitted, chunk)
		return nil
	}, state)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(emitted) != 0 {
		t.Fatalf("expected no chunk emitted, got %d: %#v", len(emitted), emitted)
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

	err := flushStreamState(func(chunk ChatChunk) error {
		select {
		case out <- chunk:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, state)
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

func collectOpenAIStreamChunks(t *testing.T, body io.Reader) ([]ChatChunk, error) {
	t.Helper()

	var chunks []ChatChunk
	err := decodeChatStreamWithHandler(context.Background(), body, func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	return chunks, err
}
