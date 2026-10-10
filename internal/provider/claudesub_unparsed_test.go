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

func TestClaudeSubDecodeUnsignaledInvalidToolInputFails(t *testing.T) {
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
	if _, err := d.decode(claudeSubEvent{Type: "result", Raw: json.RawMessage(`{"subtype":"success","is_error":false}`)}); !errors.Is(err, errClaudeSubDecodeStream) {
		t.Fatalf("unsignaled malformed input error = %v, want %v", err, errClaudeSubDecodeStream)
	}
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
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	delta, err := json.Marshal(map[string]any{"event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": raw}}})
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
