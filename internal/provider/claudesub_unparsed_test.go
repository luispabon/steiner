package provider

import (
	"encoding/json"
	"errors"
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
