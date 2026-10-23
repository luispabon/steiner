package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestClaudeSubDecodeCertifiedUnparsedToolInput(t *testing.T) {
	events := claudeSubDecodeAll(t, claudeSubDecodeHooks{ToolName: claudeSubStripPrefix}, claudeSubDecodeEvents(t, "turn_unparsed_tool_input.jsonl"))
	messages := claudeSubDecodedOfKind(events, claudeSubDecodeMessage)
	if len(messages) != 1 || messages[0].Message == nil || len(messages[0].Message.ToolCalls) != 1 {
		t.Fatalf("messages = %+v, want one tool call", messages)
	}
	call := messages[0].Message.ToolCalls[0]
	wantRaw := "{\"path\": \"x\""
	if call.ID != "toolu_unparsed" || call.Name != "read" || call.RawArguments != wantRaw {
		t.Fatalf("call = %+v, want original identity and raw arguments", call)
	}
	sentinel, ok := call.Arguments[UnparsedToolInputKey].(map[string]any)
	if !ok || sentinel["raw"] != wantRaw {
		t.Fatalf("arguments = %+v, want exact sentinel", call.Arguments)
	}
	gotLength, ok := sentinel["len"].(int)
	if !ok {
		gotLengthFloat, floatOK := sentinel["len"].(float64)
		if !floatOK {
			t.Fatalf("sentinel length type = %T, want number", sentinel["len"])
		}
		gotLength = int(gotLengthFloat)
	}
	if gotLength != len([]byte(wantRaw)) {
		t.Fatalf("sentinel length = %d, want %d", gotLength, len([]byte(wantRaw)))
	}
}

func TestClaudeSubDecodeUnsignaledInvalidToolInputIsMarked(t *testing.T) {
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for _, ev := range []claudeSubEvent{
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_bad","name":"read"}}}`)},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}}`)},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_good","name":"read"}}}`)},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"x\"}"}}}`)},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)},
	} {
		if _, err := d.decode(ev); err != nil {
			t.Fatalf("decode %s: %v", ev.Type, err)
		}
	}
	events, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)})
	if err != nil {
		t.Fatalf("unsignaled malformed input decode error = %v, want nil", err)
	}
	messages := claudeSubDecodedOfKind(events, claudeSubDecodeMessage)
	if len(messages) != 1 || messages[0].Message == nil || len(messages[0].Message.ToolCalls) != 2 {
		t.Fatalf("messages = %+v, want one message with two tool calls", messages)
	}
	calls := messages[0].Message.ToolCalls
	assertMarkedDecodeCall(t, calls[0], "toolu_bad", "read")
	assertValidDecodeCall(t, calls[1], "toolu_good")
}

func TestClaudeSubDecodeMismatchedUnparsedSentinelFails(t *testing.T) {
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for _, ev := range []claudeSubEvent{
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_bad","name":"read"}}}`)},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}}`)},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)},
	} {
		if _, err := d.decode(ev); err != nil {
			t.Fatalf("decode %s: %v", ev.Type, err)
		}
	}
	input := map[string]any{UnparsedToolInputKey: map[string]any{"raw": "{\"other\":", "len": 9}}
	block := map[string]any{"type": "tool_use", "id": "toolu_bad", "name": "read", "input": input}
	echo, err := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{block}}})
	if err != nil {
		t.Fatalf("marshal echo: %v", err)
	}
	if _, err := d.decode(claudeSubEvent{Type: "assistant", Raw: echo}); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("mismatched sentinel error = %v, want %v", err, errClaudeSubDecodeStream)
	}
}

// claudeSubSentinelDecode streams one tool_use whose input is raw, then confirms
// it with an assistant echo carrying the CLI sentinel. lengthJSON is the len
// field as JSON text, so invalid JSON types can be expressed. It returns the
// events of the whole turn, or the first decode error.
func claudeSubSentinelDecode(t *testing.T, raw, lengthJSON string) ([]claudeSubDecoded, error) {
	t.Helper()
	return claudeSubStreamedSentinelDecode(t, raw, raw, lengthJSON)
}

// claudeSubStreamedSentinelDecode streams one tool_use whose input is streamed,
// then confirms it with an assistant echo whose sentinel raw is echoRaw. The
// two differ when the CLI truncated the raw prefix.
func claudeSubStreamedSentinelDecode(t *testing.T, streamed, echoRaw, lengthJSON string) ([]claudeSubDecoded, error) {
	t.Helper()
	rawJSON, err := json.Marshal(echoRaw)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	delta, err := json.Marshal(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": streamed}}})
	if err != nil {
		t.Fatalf("marshal delta: %v", err)
	}
	input := json.RawMessage(`{"` + UnparsedToolInputKey + `":{"raw":` + string(rawJSON) + `,"len":` + lengthJSON + `}}`)
	echo, err := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "toolu_sentinel", "name": "read", "input": input}}}})
	if err != nil {
		t.Fatalf("marshal echo: %v", err)
	}
	events := []claudeSubEvent{
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_start","message":{"usage":{}}}}`)},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_sentinel","name":"read","input":{}}}}`)},
		{Type: "stream_event", Raw: delta},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"content_block_stop","index":0}}`)},
		{Type: "stream_event", Raw: json.RawMessage(`{"event":{"type":"message_stop"}}`)},
		{Type: "assistant", Raw: echo},
	}
	var out []claudeSubDecoded
	d := newClaudeSubDecoder(claudeSubDecodeHooks{})
	for _, ev := range events {
		got, err := d.decode(ev)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// claudeSubSentinelCall decodes a sentinel with a valid len and returns the
// single assembled tool call.
func claudeSubSentinelCall(t *testing.T, raw, lengthJSON string) ToolCall {
	t.Helper()
	out, err := claudeSubSentinelDecode(t, raw, lengthJSON)
	if err != nil {
		t.Fatalf("decode sentinel: %v", err)
	}
	messages := claudeSubDecodedOfKind(out, claudeSubDecodeMessage)
	if len(messages) != 1 || messages[0].Message == nil || len(messages[0].Message.ToolCalls) != 1 {
		t.Fatalf("messages = %+v, want one tool call", messages)
	}
	return messages[0].Message.ToolCalls[0]
}

// TestClaudeSubDecodeTruncatedUnparsedSentinelUsesFullInput accepts the CLI's
// truncated raw prefix and emits the full streamed input, with its byte length.
func TestClaudeSubDecodeTruncatedUnparsedSentinelUsesFullInput(t *testing.T) {
	long := `{"path": "` + strings.Repeat("a", 3000) + `"}`
	// The 2048th UTF-16 unit is the high surrogate of the emoji, so the CLI
	// drops it and the raw prefix holds 2047 units.
	surrogate := `{"p": "` + strings.Repeat("a", 2040) + "😀" + strings.Repeat("b", 10) + `"}`
	for _, tc := range []struct {
		name     string
		streamed string
		echoRaw  string
	}{
		{"ascii over cap", long, long[:2048]},
		{"surrogate pair cut", surrogate, `{"p": "` + strings.Repeat("a", 2040)},
		{"exactly cap", claudeSubInputOfUnits(2048), claudeSubInputOfUnits(2048)},
		{"one unit over cap", claudeSubInputOfUnits(2049), claudeSubInputOfUnits(2049)[:2048]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := claudeSubStreamedSentinelDecode(t, tc.streamed, tc.echoRaw, "2048")
			if err != nil {
				t.Fatalf("decode truncated sentinel: %v", err)
			}
			messages := claudeSubDecodedOfKind(out, claudeSubDecodeMessage)
			if len(messages) != 1 || messages[0].Message == nil || len(messages[0].Message.ToolCalls) != 1 {
				t.Fatalf("messages = %+v, want one tool call", messages)
			}
			call := messages[0].Message.ToolCalls[0]
			if call.RawArguments != tc.streamed {
				t.Fatalf("raw arguments = %q, want full input %q", call.RawArguments, tc.streamed)
			}
			sentinel, ok := call.Arguments[UnparsedToolInputKey].(map[string]any)
			if !ok || sentinel["raw"] != tc.streamed {
				t.Fatalf("arguments = %+v, want sentinel with full input", call.Arguments)
			}
			if got, want := sentinel["len"], len([]byte(tc.streamed)); got != want {
				t.Fatalf("sentinel length = %v, want %v", got, want)
			}
		})
	}
}

// claudeSubInputOfUnits returns an ASCII input of exactly n UTF-16 code units.
func claudeSubInputOfUnits(n int) string {
	const open, closing = `{"p": "`, `"}`
	return open + strings.Repeat("a", n-len(open)-len(closing)) + closing
}

// TestClaudeSubUnparsedRawPrefix checks the CLI cap at its boundaries, for a cut
// surrogate pair, and for invalid UTF-8. Every result must be a byte prefix of
// the input.
func TestClaudeSubUnparsedRawPrefix(t *testing.T) {
	invalid := `{"p": "` + "\xff" + strings.Repeat("a", 3000) + `"}`
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"under cap", "abc", "abc"},
		{"exactly cap", claudeSubInputOfUnits(2048), claudeSubInputOfUnits(2048)},
		{"one unit over cap", claudeSubInputOfUnits(2049), claudeSubInputOfUnits(2049)[:2048]},
		{"surrogate pair cut", `{"p": "` + strings.Repeat("a", 2040) + "😀bbbb" + `"}`, `{"p": "` + strings.Repeat("a", 2040)},
		{"invalid byte over cap", invalid, invalid[:2048]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := claudeSubUnparsedRawPrefix(tc.in)
			if !strings.HasPrefix(tc.in, got) {
				t.Fatalf("prefix %q is not a byte prefix of input", got)
			}
			if got != tc.want {
				t.Fatalf("prefix = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestClaudeSubDecodeTruncatedUnparsedSentinelMismatchFails rejects raw values
// that are neither the streamed input nor the CLI's truncated prefix of it.
func TestClaudeSubDecodeTruncatedUnparsedSentinelMismatchFails(t *testing.T) {
	long := `{"path": "` + strings.Repeat("a", 3000) + `"}`
	for _, tc := range []struct {
		name     string
		streamed string
		echoRaw  string
	}{
		{"wrong prefix content", long, "X" + long[1:2048]},
		{"prefix one unit short", long, long[:2047]},
		{"prefix one unit long", long, long[:2049]},
		{"short raw not the input", `{"path": "x"}`, `{"path":`},
		{"exact cap truncated", claudeSubInputOfUnits(2048), claudeSubInputOfUnits(2048)[:2047]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := claudeSubStreamedSentinelDecode(t, tc.streamed, tc.echoRaw, "2048")
			if !errors.Is(err, errClaudeSubDecodeStream) || !strings.Contains(err.Error(), "does not match accumulated input") {
				t.Fatalf("decode error = %v, want mismatch error wrapping %v", err, errClaudeSubDecodeStream)
			}
		})
	}
}

// TestClaudeSubDecodeUnparsedSentinelLengthIsNormalized accepts any non-negative
// source len for non-ASCII raw input. The CLI's len unit is unconfirmed, so the
// emitted sentinel carries the UTF-8 byte length computed by the decoder.
func TestClaudeSubDecodeUnparsedSentinelLengthIsNormalized(t *testing.T) {
	// 10 ASCII bytes, then é (2 bytes, 1 rune, 1 UTF-16 unit), 😀 (4 bytes,
	// 1 rune, 2 UTF-16 units) and a closing quote: 17 bytes, 13 runes, 14 units.
	raw := "{\"path\": \"é😀\""
	for _, lengthJSON := range []string{"0", "13", "14", "17", "99"} {
		t.Run(lengthJSON, func(t *testing.T) {
			call := claudeSubSentinelCall(t, raw, lengthJSON)
			if call.RawArguments != raw {
				t.Fatalf("raw arguments = %q, want %q", call.RawArguments, raw)
			}
			sentinel, ok := call.Arguments[UnparsedToolInputKey].(map[string]any)
			if !ok || sentinel["raw"] != raw {
				t.Fatalf("arguments = %+v, want exact sentinel", call.Arguments)
			}
			if got, want := sentinel["len"], len([]byte(raw)); got != want {
				t.Fatalf("sentinel length = %v, want %d", got, want)
			}
		})
	}
}

// TestClaudeSubDecodeUnparsedSentinelInvalidLengthFails rejects a len field that
// is not a non-negative JSON integer.
func TestClaudeSubDecodeUnparsedSentinelInvalidLengthFails(t *testing.T) {
	raw := "{\"path\": \"é😀\""
	for _, lengthJSON := range []string{`-1`, `1.5`, `1e2`, `"17"`, `null`, `true`, `9223372036854775808`} {
		t.Run(lengthJSON, func(t *testing.T) {
			_, err := claudeSubSentinelDecode(t, raw, lengthJSON)
			if !errors.Is(err, errClaudeSubDecodeStream) || !strings.Contains(err.Error(), "length is invalid") {
				t.Fatalf("decode error = %v, want length error wrapping %v", err, errClaudeSubDecodeStream)
			}
		})
	}
}
