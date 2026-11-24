package provider

import (
	"bytes"
	"encoding/json"
	"testing"
)

// anthropicWirePrefix returns the JSON payload of req without its final message.
// The payload includes the system blocks, tools and params, so any change before
// the final message, including a moved cache_control breakpoint, changes the bytes.
func anthropicWirePrefix(t *testing.T, req ChatRequest) []byte {
	t.Helper()
	wire := anthropicRequestWire(req, "model", false)
	wire.Messages = wire.Messages[:len(wire.Messages)-1]
	b, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return b
}

// anthropicWireFinal returns the JSON of the final message of req's payload.
func anthropicWireFinal(t *testing.T, req ChatRequest) []byte {
	t.Helper()
	wire := anthropicRequestWire(req, "model", false)
	b, err := json.Marshal(wire.Messages[len(wire.Messages)-1])
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return b
}

// TestAnthropicCompactionRetryPrefixIsByteIdentical proves a compaction retry
// changes only its final instruction message on the wire. The final message has
// its own rolling breakpoint, but the previous user message also carries one, so
// the retry still reads the cached history.
func TestAnthropicCompactionRetryPrefixIsByteIdentical(t *testing.T) {
	t.Parallel()
	const instruction = "Summarize the conversation."
	tools := []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read", Parameters: map[string]any{"type": "object"}}}}
	history := []Message{
		{Role: MessageRoleSystem, Content: "system prompt"},
		{Role: MessageRoleUser, Content: "first request"},
		{Role: MessageRoleAssistant, Content: "first answer"},
		{Role: MessageRoleUser, Content: "second request"},
		{Role: MessageRoleAssistant, Content: "second answer"},
	}
	build := func(final string) ChatRequest {
		messages := append(append([]Message(nil), history...), Message{Role: MessageRoleUser, Content: final})
		return ChatRequest{
			Model:    "model",
			Messages: messages,
			Tools:    tools,
			Params:   map[string]any{"temperature": 0.2},
		}
	}
	first := build(instruction)

	wire := anthropicRequestWire(first, "model", false)
	historyBreakpoint := false
	for _, msg := range wire.Messages[:len(wire.Messages)-1] {
		for _, block := range msg.Content {
			if block.CacheControl != nil {
				historyBreakpoint = true
			}
		}
	}
	if !historyBreakpoint {
		t.Fatal("no cache_control breakpoint before the final message, so the retry would not reuse the cached history")
	}

	firstPrefix := anthropicWirePrefix(t, first)
	firstFinal := anthropicWireFinal(t, first)
	tests := []struct {
		name  string
		final string
	}{
		{name: "tool calls steering", final: instruction + "\n\nThe previous reply contained tool calls and no text."},
		{name: "final attempt steering", final: instruction + "\n\nThis is the final attempt. Begin directly with the summary."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retry := build(tt.final)
			if got := anthropicWirePrefix(t, retry); !bytes.Equal(got, firstPrefix) {
				t.Errorf("retry payload before the final message differs from the first call:\n got %s\nwant %s", got, firstPrefix)
			}
			if got := anthropicWireFinal(t, retry); bytes.Equal(got, firstFinal) {
				t.Error("retry final message equals the first call's, want the steering in it")
			}
		})
	}
}
