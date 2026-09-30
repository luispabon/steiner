package agent

import (
	"reflect"
	"testing"
)

func TestReplaySafeConversation_PreservesCodexBlockMetadataAndOrder(t *testing.T) {
	conversation := []Message{{
		Role:    MessageRoleAssistant,
		Content: "visible response",
		ProviderMetadata: &MessageProviderMetadata{Codex: &CodexMessageMetadata{
			ReasoningID: "reason_xyz",
			Blocks: []CodexMessageBlock{
				{Kind: "message", Phase: "analysis", Text: "thinking"},
				{Kind: "message", Phase: "commentary", Text: "searching"},
				{Kind: "function_call", CallID: "call-1"},
				{Kind: "message", Phase: "final_answer", Text: "done"},
			},
		}},
	}}

	got := ReplaySafeConversation(conversation)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].ProviderMetadata == nil || got[0].ProviderMetadata.Codex == nil {
		t.Fatalf("got Codex metadata = %#v, want preserved", got[0].ProviderMetadata)
	}
	if !reflect.DeepEqual(got[0].ProviderMetadata.Codex, conversation[0].ProviderMetadata.Codex) {
		t.Fatalf("got Codex metadata = %#v, want %#v", got[0].ProviderMetadata.Codex, conversation[0].ProviderMetadata.Codex)
	}
}

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

func TestReplaySafeConversation_DropsAssistantThatBecomesEmptyAfterToolCallRemoval(t *testing.T) {
	conversation := []Message{
		{Role: MessageRoleUser, Content: "investigate"},
		{
			Role: MessageRoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "call-1", Name: "grep", Arguments: map[string]any{"pattern": "foo"}},
			},
		},
		{Role: MessageRoleAssistant, Content: "interrupted"},
	}

	got := ReplaySafeConversation(conversation)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Role != MessageRoleUser || got[1].Role != MessageRoleAssistant {
		t.Fatalf("got = %#v, want user + trailing assistant", got)
	}
	if got[1].Content != "interrupted" {
		t.Fatalf("got[1].Content = %q, want %q", got[1].Content, "interrupted")
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
