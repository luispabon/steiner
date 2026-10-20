package provider

import (
	"math"
	"testing"
)

func TestMessageToResponsesItemsRejectsUnmarshalableArguments(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "bash", Arguments: map[string]any{"timeout": math.NaN()}}
	tests := []struct {
		name string
		msg  Message
	}{
		{
			name: "no provider blocks",
			msg:  Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{call}},
		},
		{
			name: "matched function_call block",
			msg: Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{call}, ProviderMetadata: codexBlocksMeta(
				CodexMessageBlock{Kind: "function_call", CallID: "c1"},
			)},
		},
		{
			name: "unmatched tool call",
			msg: Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{call}, ProviderMetadata: codexBlocksMeta(
				CodexMessageBlock{Kind: "message", Text: "hi"},
			)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := messageToResponsesItems(tt.msg); err == nil {
				t.Fatal("messageToResponsesItems() error = nil, want marshal error")
			}
		})
	}
}

func TestResponsesFunctionCallItemPrefersRawArguments(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "bash", Arguments: map[string]any{"timeout": math.NaN()}, RawArguments: `{"timeout":1}`}
	item, err := responsesFunctionCallItem(call)
	if err != nil {
		t.Fatalf("responsesFunctionCallItem: %v", err)
	}
	if item.Args != call.RawArguments {
		t.Fatalf("Args = %q, want raw arguments %q", item.Args, call.RawArguments)
	}
}
