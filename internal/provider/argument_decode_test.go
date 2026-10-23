package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// assertMarkedDecodeCall fails unless call is a malformed call recorded by
// markArgumentDecodeError.
func assertMarkedDecodeCall(t *testing.T, call ToolCall, wantID, wantName string) {
	t.Helper()
	if call.ID != wantID || call.Name != wantName {
		t.Fatalf("call identity = %q/%q, want %q/%q", call.ID, call.Name, wantID, wantName)
	}
	if call.Arguments == nil || len(call.Arguments) != 0 {
		t.Fatalf("Arguments = %#v, want empty map", call.Arguments)
	}
	if call.RawArguments != "{}" {
		t.Fatalf("RawArguments = %q, want {}", call.RawArguments)
	}
	if call.ArgumentDecodeError == "" || len(call.ArgumentDecodeError) > maxArgumentDecodeErrorBytes {
		t.Fatalf("ArgumentDecodeError = %q, want non-empty and at most %d bytes", call.ArgumentDecodeError, maxArgumentDecodeErrorBytes)
	}
}

// assertValidDecodeCall fails unless call decoded its arguments intact. Every
// caller decodes a "read" call.
func assertValidDecodeCall(t *testing.T, call ToolCall, wantID string) {
	t.Helper()
	if call.ID != wantID || call.Name != "read" {
		t.Fatalf("call identity = %q/%q, want %q/read", call.ID, call.Name, wantID)
	}
	if got := call.Arguments["path"]; got != "x" {
		t.Fatalf("Arguments[path] = %#v, want \"x\"", got)
	}
	if call.ArgumentDecodeError != "" {
		t.Fatalf("ArgumentDecodeError = %q, want empty", call.ArgumentDecodeError)
	}
}

func TestMarkArgumentDecodeErrorDropsInvalidRaw(t *testing.T) {
	call := markArgumentDecodeError(ToolCall{ID: "call_1", Name: "read", RawArguments: `{"path":`}, errors.New("unexpected end of JSON input"))
	assertMarkedDecodeCall(t, call, "call_1", "read")
	if want := `decode "read" arguments: unexpected end of JSON input`; call.ArgumentDecodeError != want {
		t.Fatalf("ArgumentDecodeError = %q, want %q", call.ArgumentDecodeError, want)
	}
}

func TestMarkArgumentDecodeErrorTruncatesOnRuneBoundary(t *testing.T) {
	// Each é is two bytes, so the 512-byte cut lands inside a rune unless the
	// truncation backs up to a rune start.
	name := strings.Repeat("é", 400)
	full := "decode " + `"` + name + `"` + " arguments: boom"
	call := markArgumentDecodeError(ToolCall{Name: name}, errors.New("boom"))
	assertMarkedDecodeCall(t, call, "", name)
	if !utf8.ValidString(call.ArgumentDecodeError) {
		t.Fatalf("ArgumentDecodeError is not valid UTF-8: %q", call.ArgumentDecodeError)
	}
	if !strings.HasPrefix(full, call.ArgumentDecodeError) {
		t.Fatalf("ArgumentDecodeError is not a prefix of the full message")
	}
	if len(call.ArgumentDecodeError) < maxArgumentDecodeErrorBytes-1 {
		t.Fatalf("ArgumentDecodeError length = %d, want a cut within one byte of %d", len(call.ArgumentDecodeError), maxArgumentDecodeErrorBytes)
	}
}

func TestCloneToolCallsPreservesArgumentDecodeError(t *testing.T) {
	calls := []ToolCall{markArgumentDecodeError(ToolCall{ID: "call_1", Name: "read"}, errors.New("boom"))}
	got := CloneToolCalls(calls)
	assertMarkedDecodeCall(t, got[0], "call_1", "read")
	if got[0].ArgumentDecodeError != calls[0].ArgumentDecodeError {
		t.Fatalf("cloned ArgumentDecodeError = %q, want %q", got[0].ArgumentDecodeError, calls[0].ArgumentDecodeError)
	}
}

func TestOpenAIStreamFinalizeMarksMalformedCall(t *testing.T) {
	bad := &openAIToolCallAccumulator{ID: "call_bad", Name: "read"}
	bad.Arguments.WriteString(`{"path":`)
	good := &openAIToolCallAccumulator{ID: "call_good", Name: "read"}
	good.Arguments.WriteString(`{"path":"x"}`)
	calls := finalizeToolCallAccumulators([]*openAIToolCallAccumulator{bad, good})
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	assertMarkedDecodeCall(t, calls[0], "call_bad", "read")
	assertValidDecodeCall(t, calls[1], "call_good")
}

func TestOpenAINonStreamNormalizeMarksMalformedCall(t *testing.T) {
	toolCalls := []openAIToolCall{
		{ID: "call_bad", Type: "function", Function: openAIToolCallFunction{Name: "read", Arguments: `{"path":`}},
		{ID: "call_good", Type: "function", Function: openAIToolCallFunction{Name: "read", Arguments: `{"path":"x"}`}},
	}
	calls := normalizeToolCalls(toolCalls)
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	assertMarkedDecodeCall(t, calls[0], "call_bad", "read")
	assertValidDecodeCall(t, calls[1], "call_good")
}

func TestAnthropicStreamFinalizeMarksMalformedCall(t *testing.T) {
	bad := &anthropicToolUseAccumulator{ID: "toolu_bad", Name: "read"}
	bad.Input.WriteString(`{"path":`)
	good := &anthropicToolUseAccumulator{ID: "toolu_good", Name: "read"}
	good.Input.WriteString(`{"path":"x"}`)
	calls := finalizeAnthropicToolUses(map[int]*anthropicToolUseAccumulator{0: bad, 1: good})
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	assertMarkedDecodeCall(t, calls[0], "toolu_bad", "read")
	assertValidDecodeCall(t, calls[1], "toolu_good")
}

func TestResponsesToolCallMarksMalformedCall(t *testing.T) {
	bad := responsesToolCall(responsesItem{Type: "function_call", CallID: "call_bad", Name: "read", Args: `{"path":`})
	assertMarkedDecodeCall(t, bad, "call_bad", "read")
	good := responsesToolCall(responsesItem{Type: "function_call", CallID: "call_good", Name: "read", Args: `{"path":"x"}`})
	assertValidDecodeCall(t, good, "call_good")
}

func TestMergeResponsesToolCallKeepsArgumentDecodeError(t *testing.T) {
	bad := responsesToolCall(responsesItem{Type: "function_call", CallID: "call_bad", Name: "read", Args: `{"path":`})
	entry := &responsesLedgerEntry{}
	mergeResponsesToolCall(entry, bad)
	if entry.call == nil {
		t.Fatal("entry.call = nil, want merged call")
	}
	assertMarkedDecodeCall(t, *entry.call, "call_bad", "read")
}

func TestMarkedToolCallReplaysEmptyObject(t *testing.T) {
	marked := markArgumentDecodeError(ToolCall{ID: "call_bad", Name: "read"}, errors.New("boom"))
	msg := Message{Role: "assistant", ToolCalls: []ToolCall{marked}}

	openAIMessages, err := toOpenAIMessages(msg)
	if err != nil {
		t.Fatalf("toOpenAIMessages() error = %v", err)
	}
	if data, _ := json.Marshal(openAIMessages); !strings.Contains(string(data), `"arguments":"{}"`) {
		t.Fatalf("OpenAI chat encoding = %s, want arguments \"{}\"", data)
	}

	item, err := responsesFunctionCallItem(marked)
	if err != nil {
		t.Fatalf("responsesFunctionCallItem() error = %v", err)
	}
	if item.Args != "{}" {
		t.Fatalf("Responses function_call args = %q, want {}", item.Args)
	}

	anthropicMsg := toAnthropicMessage(msg)
	if anthropicMsg == nil {
		t.Fatal("toAnthropicMessage() = nil")
	}
	if data, _ := json.Marshal(anthropicMsg); !strings.Contains(string(data), `"input":{}`) {
		t.Fatalf("Anthropic encoding = %s, want input {}", data)
	}
}
