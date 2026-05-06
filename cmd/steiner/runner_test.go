package main

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

func TestToProviderConversationPreservesTurn(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "hello", Turn: 4},
		{Role: agent.MessageRoleAssistant, Content: "world", Turn: 5},
		{
			Role:       agent.MessageRoleTool,
			Content:    "result",
			ToolCallID: "call_1",
			Turn:       6,
		},
	}

	got := toProviderConversation(messages)
	if len(got) != len(messages) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(messages))
	}
	for i, message := range got {
		if message.Turn != messages[i].Turn {
			t.Fatalf("message %d turn = %d, want %d", i, message.Turn, messages[i].Turn)
		}
	}
	if got[2].ToolCallID != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", got[2].ToolCallID)
	}
	if got[0].Role != provider.MessageRoleUser || got[1].Role != provider.MessageRoleAssistant || got[2].Role != provider.MessageRoleTool {
		t.Fatalf("roles preserved incorrectly: %#v", got)
	}
}
