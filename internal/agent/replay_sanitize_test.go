package agent

import "testing"

func TestReplaySafeConversation_StripsDanglingToolCalls(t *testing.T) {
	conversation := []Message{
		{Role: MessageRoleUser, Content: "investigate"},
		{
			Role:    MessageRoleAssistant,
			Content: "starting search",
			ToolCalls: []ToolCall{
				{ID: "call-1", Name: "grep", Arguments: map[string]any{"pattern": "foo"}},
			},
		},
		{Role: MessageRoleAssistant, Content: "interrupted"},
		{Role: MessageRoleTool, Content: "orphaned result", ToolCallID: "orphan"},
	}

	got := ReplaySafeConversation(conversation)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if len(got[1].ToolCalls) != 0 {
		t.Fatalf("got[1].ToolCalls = %#v, want cleared dangling tool calls", got[1].ToolCalls)
	}
	if got[2].Role != MessageRoleAssistant || got[2].Content != "interrupted" {
		t.Fatalf("got[2] = %#v, want trailing assistant preserved", got[2])
	}
}

func TestReplaySafeConversation_PreservesPairedToolResults(t *testing.T) {
	conversation := []Message{
		{Role: MessageRoleUser, Content: "investigate"},
		{
			Role:    MessageRoleAssistant,
			Content: "running search",
			ToolCalls: []ToolCall{
				{ID: "call-1", Name: "grep", Arguments: map[string]any{"pattern": "foo"}},
			},
		},
		{Role: MessageRoleTool, Content: "match", ToolCallID: "call-1"},
		{Role: MessageRoleAssistant, Content: "done"},
	}

	got := ReplaySafeConversation(conversation)
	if len(got) != len(conversation) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(conversation))
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("got[1].ToolCalls = %#v, want paired tool call preserved", got[1].ToolCalls)
	}
	if got[2].Role != MessageRoleTool || got[2].ToolCallID != "call-1" {
		t.Fatalf("got[2] = %#v, want paired tool result preserved", got[2])
	}
}
