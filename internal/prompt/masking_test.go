package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestMaskConversationMasksOlderToolResults(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "user 1"},
		{
			Role:    provider.MessageRoleAssistant,
			Content: "tool turn 1",
			ToolCalls: []provider.ToolCall{
				{ID: "call_1", Name: "grep", Arguments: map[string]any{"pattern": "ContextManager", "path": "internal/agent"}},
			},
		},
		{Role: provider.MessageRoleTool, ToolCallID: "call_1", Name: "grep", Content: "raw grep output"},
		{Role: provider.MessageRoleUser, Content: "user 2"},
		{Role: provider.MessageRoleAssistant, Content: "recent answer\nwith more detail"},
	}

	got := MaskConversation(messages, 1)

	if got[2].Content == messages[2].Content {
		t.Fatalf("tool result content = %q, want masked placeholder", got[2].Content)
	}
	if !strings.Contains(got[2].Content, "grep") {
		t.Fatalf("tool result content = %q, want tool name preserved", got[2].Content)
	}
	if !strings.Contains(got[2].Content, "pattern=ContextManager") {
		t.Fatalf("tool result content = %q, want argument summary preserved", got[2].Content)
	}
	if got[1].ToolCalls[0].Name != "grep" {
		t.Fatalf("assistant tool call metadata lost: %+v", got[1].ToolCalls[0])
	}
}

func TestMaskConversationTrimsOlderAssistantProse(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "u1"},
		{Role: provider.MessageRoleAssistant, Content: "first line\nsecond line\nthird line"},
		{Role: provider.MessageRoleUser, Content: "u2"},
		{Role: provider.MessageRoleAssistant, Content: "keep all lines\nsecond line"},
	}

	got := MaskConversation(messages, 1)

	if got[1].Content != "first line" {
		t.Fatalf("older assistant content = %q, want first line only", got[1].Content)
	}
	if got[3].Content != messages[3].Content {
		t.Fatalf("recent assistant content = %q, want unchanged", got[3].Content)
	}
}

func TestMaskConversationKeepsConversationUnchangedWithinWindow(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "u1"},
		{Role: provider.MessageRoleAssistant, Content: "a1"},
		{Role: provider.MessageRoleTool, Name: "read", Content: "tool output"},
	}

	got := MaskConversation(messages, 5)

	for i := range got {
		if got[i].Content != messages[i].Content {
			t.Fatalf("message %d content = %q, want %q", i, got[i].Content, messages[i].Content)
		}
	}
}

func TestMaskConversationHandlesMultiToolTurn(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "u1"},
		{
			Role: provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{
				{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.go"}},
				{ID: "call_2", Name: "grep", Arguments: map[string]any{"pattern": "TODO"}},
			},
		},
		{Role: provider.MessageRoleTool, ToolCallID: "call_1", Name: "read", Content: "file body"},
		{Role: provider.MessageRoleTool, ToolCallID: "call_2", Name: "grep", Content: "grep body"},
		{Role: provider.MessageRoleAssistant, Content: "recent"},
	}

	got := MaskConversation(messages, 1)

	if !strings.Contains(got[2].Content, "read") {
		t.Fatalf("first masked tool result = %q, want read metadata", got[2].Content)
	}
	if !strings.Contains(got[3].Content, "grep") {
		t.Fatalf("second masked tool result = %q, want grep metadata", got[3].Content)
	}
}
