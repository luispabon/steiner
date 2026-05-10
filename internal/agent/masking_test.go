package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMaskConversationUsesRetainedDelegateSummary(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1"},
		{
			Role: MessageRoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "delegate", Arguments: map[string]any{"task": "do work"}},
			},
		},
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_1",
			Name:       "delegate",
			Content:    "full delegate output with paths and details",
			Turn:       1,
			Retention: &MessageRetention{
				Kind:       "delegate_summary",
				Summary:    "summary text with findings",
				AgentID:    "child-123",
				Status:     "complete",
				TurnCount:  4,
				TokenCount: 8120,
			},
		},
		{Role: MessageRoleUser, Content: "u2"},
		{Role: MessageRoleAssistant, Content: "middle answer"},
		{Role: MessageRoleUser, Content: "u3"},
		{Role: MessageRoleAssistant, Content: "recent answer"},
	}

	got := maskConversation(messages, 1)
	if strings.Contains(got[2].Content, "full delegate output with paths and details") {
		t.Fatalf("masked delegate content leaked full output: %q", got[2].Content)
	}
	if !strings.Contains(got[2].Content, "retained delegation summary") {
		t.Fatalf("masked delegate content = %q, want retained summary block", got[2].Content)
	}
	if !strings.Contains(got[2].Content, "summary text with findings") {
		t.Fatalf("masked delegate content = %q, want summary text", got[2].Content)
	}
	if got[2].Retention == nil || got[2].Retention.Summary != "summary text with findings" {
		t.Fatalf("retention lost after masking: %#v", got[2].Retention)
	}
}

func TestMaskConversationBeforeTurnUsesRetainedDelegateSummary(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1", Turn: 1},
		{
			Role: MessageRoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "delegate", Arguments: map[string]any{"task": "do work"}},
			},
			Turn: 1,
		},
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_1",
			Name:       "delegate",
			Content:    "full delegate output with paths and details",
			Turn:       1,
			Retention: &MessageRetention{
				Kind:       "delegate_summary",
				Summary:    "summary text with findings",
				AgentID:    "child-123",
				Status:     "complete",
				TurnCount:  4,
				TokenCount: 8120,
			},
		},
		{Role: MessageRoleUser, Content: "u2", Turn: 2},
		{Role: MessageRoleAssistant, Content: "middle answer", Turn: 2},
		{Role: MessageRoleUser, Content: "u3", Turn: 3},
		{Role: MessageRoleAssistant, Content: "recent answer", Turn: 3},
	}

	got := maskConversationBeforeTurn(messages, 3)
	if strings.Contains(got[2].Content, "full delegate output with paths and details") {
		t.Fatalf("masked delegate content leaked full output: %q", got[2].Content)
	}
	if !strings.Contains(got[2].Content, "retained delegation summary") {
		t.Fatalf("masked delegate content = %q, want retained summary block", got[2].Content)
	}
	if got[6].Content != "recent answer" {
		t.Fatalf("recent assistant content = %q, want unchanged", got[6].Content)
	}
}

func TestMaskConversationFallsBackForMissingRetention(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1"},
		{
			Role: MessageRoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "delegate", Arguments: map[string]any{"task": "do work"}},
			},
		},
		{Role: MessageRoleTool, ToolCallID: "call_1", Name: "delegate", Content: "full delegate output", Turn: 1},
		{Role: MessageRoleUser, Content: "u2"},
		{Role: MessageRoleAssistant, Content: "middle answer"},
		{Role: MessageRoleUser, Content: "u3"},
		{Role: MessageRoleAssistant, Content: "recent answer"},
	}

	got := maskConversation(messages, 1)
	if got[2].Content == "full delegate output" {
		t.Fatal("delegate content was not masked")
	}
	if !strings.Contains(got[2].Content, "older tool result masked") && !strings.Contains(got[2].Content, "tool result from turn") {
		t.Fatalf("fallback masking = %q, want generic placeholder", got[2].Content)
	}
}

func TestMaskConversationLeavesScratchpadAndRecentMessagesAlone(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1"},
		{
			Role: MessageRoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "scratchpad", Arguments: map[string]any{"intent": "plan"}},
			},
		},
		{Role: MessageRoleTool, ToolCallID: "call_1", Name: "scratchpad", Content: `{"ok":true}`, Turn: 1},
		{Role: MessageRoleUser, Content: "u2"},
		{Role: MessageRoleAssistant, Content: "middle answer"},
		{Role: MessageRoleUser, Content: "u3"},
		{Role: MessageRoleAssistant, Content: "recent answer"},
	}

	got := maskConversation(messages, 1)
	if got[2].Content != "" {
		t.Fatalf("scratchpad tool result = %q, want empty string", got[2].Content)
	}
	if got[6].Content != "recent answer" {
		t.Fatalf("recent assistant content = %q, want unchanged", got[6].Content)
	}
}

func TestRetainedSummaryTruncationIsUTF8Safe(t *testing.T) {
	longSummary := strings.Repeat("世界", 800)
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1"},
		{
			Role: MessageRoleAssistant,
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "delegate", Arguments: map[string]any{"task": "do work"}},
			},
		},
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_1",
			Name:       "delegate",
			Content:    "full delegate output",
			Turn:       1,
			Retention: &MessageRetention{
				Kind:       "delegate_summary",
				Summary:    longSummary,
				AgentID:    "child-123",
				Status:     "complete",
				TurnCount:  4,
				TokenCount: 8120,
			},
		},
		{Role: MessageRoleUser, Content: "u2"},
		{Role: MessageRoleAssistant, Content: "middle answer"},
		{Role: MessageRoleUser, Content: "u3"},
		{Role: MessageRoleAssistant, Content: "recent answer"},
	}

	got := maskConversation(messages, 1)
	if !utf8.ValidString(got[2].Content) {
		t.Fatal("masked retained summary is not valid UTF-8")
	}
	if !strings.Contains(got[2].Content, "...") {
		t.Fatalf("masked retained summary = %q, want truncation ellipsis", got[2].Content)
	}
}
