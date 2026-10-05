package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func codexBlocksMeta(blocks ...CodexMessageBlock) *MessageProviderMetadata {
	return &MessageProviderMetadata{Codex: &CodexMessageMetadata{Blocks: blocks}}
}

func TestMessageToResponsesItemsSkipsEmptyAssistantContent(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "bash", RawArguments: `{}`}
	tests := []struct {
		name      string
		msg       Message
		wantTypes []string
	}{
		{
			name:      "empty commentary block only",
			msg:       Message{Role: MessageRoleAssistant, ProviderMetadata: codexBlocksMeta(CodexMessageBlock{Kind: "message", Phase: "commentary"})},
			wantTypes: nil,
		},
		{
			name: "empty final_answer after commentary",
			msg: Message{Role: MessageRoleAssistant, ProviderMetadata: codexBlocksMeta(
				CodexMessageBlock{Kind: "message", Phase: "commentary", Text: "hi"},
				CodexMessageBlock{Kind: "message", Phase: "final_answer"},
			)},
			wantTypes: []string{"message"},
		},
		{
			name: "empty block between text and function call keeps order",
			msg: Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{call}, ProviderMetadata: codexBlocksMeta(
				CodexMessageBlock{Kind: "message", Text: "a"},
				CodexMessageBlock{Kind: "message"},
				CodexMessageBlock{Kind: "function_call", CallID: "c1"},
			)},
			wantTypes: []string{"message", "function_call"},
		},
		{
			name:      "legacy image-only assistant message",
			msg:       Message{Role: MessageRoleAssistant, Images: []ImageBlock{{MediaType: "image/png", Data: "x"}}},
			wantTypes: nil,
		},
		{
			name: "lone reasoning dropped",
			msg: Message{Role: MessageRoleAssistant, ReasoningContent: "t",
				ProviderMetadata: codexBlocksMeta(CodexMessageBlock{Kind: "message"})},
			wantTypes: nil,
		},
		{
			name: "reasoning before function call kept",
			msg: Message{Role: MessageRoleAssistant, ReasoningContent: "t", ToolCalls: []ToolCall{call},
				ProviderMetadata: codexBlocksMeta(CodexMessageBlock{Kind: "message"}, CodexMessageBlock{Kind: "function_call", CallID: "c1"})},
			wantTypes: []string{"reasoning", "function_call"},
		},
		{
			name: "reasoning before text block kept",
			msg: Message{Role: MessageRoleAssistant, ReasoningContent: "t",
				ProviderMetadata: codexBlocksMeta(CodexMessageBlock{Kind: "message", Text: "a"})},
			wantTypes: []string{"reasoning", "message"},
		},
		{
			name:      "legacy reasoning only dropped",
			msg:       Message{Role: MessageRoleAssistant, ReasoningContent: "t"},
			wantTypes: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := messageToResponsesItems(tt.msg)
			if err != nil {
				t.Fatalf("messageToResponsesItems() error = %v", err)
			}
			var got []string
			for _, item := range items {
				got = append(got, item.Type)
				if item.Type == "message" && len(item.Content) == 0 {
					t.Errorf("message item without content: %+v", item)
				}
			}
			if strings.Join(got, ",") != strings.Join(tt.wantTypes, ",") {
				t.Errorf("item types = %v, want %v", got, tt.wantTypes)
			}
		})
	}
}

func TestResponsesRequestHasNoContentlessMessageItems(t *testing.T) {
	wire, err := responsesRequestWire(ChatRequest{Messages: []Message{
		{Role: MessageRoleUser, Content: "go"},
		{Role: MessageRoleAssistant, ProviderMetadata: codexBlocksMeta(
			CodexMessageBlock{Kind: "message", Phase: "commentary", Text: "narration"},
			CodexMessageBlock{Kind: "message", Phase: "final_answer"},
		)},
		{Role: MessageRoleUser, Content: "confirmed"},
	}}, "m", false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(wire.Input)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	for i, item := range items {
		if item["type"] == "message" && item["content"] == nil {
			t.Errorf("input[%d] is a message without content: %s", i, data)
		}
	}
}
