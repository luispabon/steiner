package agent

import (
	"encoding/json"
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

func TestMaskConversationBeforeTurnUsesRestoredRetainedDelegateSummary(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
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
			}),
		},
		NextGenerationID: 2,
	}

	data, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("marshal lineage: %v", err)
	}

	var restored ConversationLineage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal lineage: %v", err)
	}

	got := maskConversationBeforeTurn(restored.FullMessages(), 2)
	if got[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("masked tool call id = %q, want preserved", got[1].ToolCalls[0].ID)
	}
	if got[1].ToolCalls[0].Name != "delegate" {
		t.Fatalf("masked tool call name = %q, want preserved", got[1].ToolCalls[0].Name)
	}
	if got[1].ToolCalls[0].Arguments["task"] != "[masked historical delegate request from turn 1; see retained delegation summary in paired tool result]" {
		t.Fatalf("masked delegate args = %#v", got[1].ToolCalls[0].Arguments)
	}
	if strings.Contains(got[2].Content, "full delegate output with paths and details") {
		t.Fatalf("masked delegate content leaked full output: %q", got[2].Content)
	}
	if !strings.Contains(got[2].Content, "retained delegation summary") {
		t.Fatalf("masked delegate content = %q, want retained summary block", got[2].Content)
	}
	if got[2].ToolCallID != "call_1" {
		t.Fatalf("tool result ToolCallID = %q, want preserved", got[2].ToolCallID)
	}
	if got[2].Name != "delegate" {
		t.Fatalf("tool result name = %q, want preserved", got[2].Name)
	}
	if got[6].Content != "recent answer" {
		t.Fatalf("recent assistant content = %q, want unchanged", got[6].Content)
	}
}

func TestMaskConversationBeforeTurnMasksHistoricalDelegateInputsInMixedAssistantMessage(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{Role: MessageRoleUser, Content: "u1", Turn: 1},
				{
					Role:    MessageRoleAssistant,
					Content: "delegate work",
					Turn:    1,
					ToolCalls: []ToolCall{
						{ID: "call_delegate", Name: "delegate", Arguments: map[string]any{"task": "inspect the repo and summarize findings"}},
						{ID: "call_read", Name: "read", Arguments: map[string]any{"path": "internal/agent/masking.go", "offset": 0}},
					},
				},
				{
					Role:       MessageRoleTool,
					ToolCallID: "call_delegate",
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
				{
					Role:       MessageRoleTool,
					ToolCallID: "call_read",
					Content:    "read output",
					Turn:       1,
				},
				{Role: MessageRoleUser, Content: "u2", Turn: 2},
				{Role: MessageRoleAssistant, Content: "middle answer", Turn: 2},
				{Role: MessageRoleUser, Content: "u3", Turn: 3},
				{
					Role:    MessageRoleAssistant,
					Content: "recent answer",
					Turn:    3,
					ToolCalls: []ToolCall{
						{ID: "call_recent", Name: "delegate", Arguments: map[string]any{"task": "fresh delegate task"}},
					},
				},
			}),
		},
		NextGenerationID: 2,
	}

	data, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("marshal lineage: %v", err)
	}

	var restored ConversationLineage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal lineage: %v", err)
	}

	got := maskConversationBeforeTurn(restored.FullMessages(), 2)
	if got[1].ToolCalls[0].ID != "call_delegate" || got[1].ToolCalls[0].Name != "delegate" {
		t.Fatalf("delegate tool call pairing changed: %#v", got[1].ToolCalls[0])
	}
	if got[1].ToolCalls[0].Arguments["task"] != "[masked historical delegate request from turn 1; see retained delegation summary in paired tool result]" {
		t.Fatalf("delegate args = %#v", got[1].ToolCalls[0].Arguments)
	}
	if got[1].ToolCalls[1].Arguments["path"] != "internal/agent/masking.go" || got[1].ToolCalls[1].Arguments["offset"] != float64(0) {
		t.Fatalf("non-delegate args changed: %#v", got[1].ToolCalls[1].Arguments)
	}
	if got[2].ToolCallID != "call_delegate" || got[2].Name != "delegate" {
		t.Fatalf("delegate tool result pairing changed: %#v", got[2])
	}
	if got[2].Retention == nil || got[2].Retention.Summary != "summary text with findings" {
		t.Fatalf("delegate retention lost after masking: %#v", got[2].Retention)
	}
	if !strings.Contains(got[2].Content, "retained delegation summary") || !strings.Contains(got[2].Content, "summary text with findings") {
		t.Fatalf("delegate tool result content = %q, want retained summary", got[2].Content)
	}
	if got[3].ToolCallID != "call_read" {
		t.Fatalf("read tool result ToolCallID = %q, want preserved", got[3].ToolCallID)
	}
	if !strings.Contains(got[3].Content, "read") {
		t.Fatalf("read tool result content = %q, want tool name from ToolCallID", got[3].Content)
	}
	if !strings.Contains(got[3].Content, "path=internal/agent/masking.go") {
		t.Fatalf("read tool result content = %q, want summarized args", got[3].Content)
	}
	if got[7].ToolCalls[0].ID != "call_recent" || got[7].ToolCalls[0].Name != "delegate" {
		t.Fatalf("recent delegate tool call changed: %#v", got[7].ToolCalls[0])
	}
	if got[7].ToolCalls[0].Arguments["task"] != "fresh delegate task" {
		t.Fatalf("recent delegate args = %#v, want unchanged", got[7].ToolCalls[0].Arguments)
	}
}

func TestMaskConversationBeforeTurnFallsBackForRestoredMissingRetention(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{Role: MessageRoleUser, Content: "u1", Turn: 1},
				{
					Role: MessageRoleAssistant,
					ToolCalls: []ToolCall{
						{ID: "call_1", Name: "delegate", Arguments: map[string]any{"task": "do work"}},
					},
					Turn: 1,
				},
				{Role: MessageRoleTool, ToolCallID: "call_1", Name: "delegate", Content: "full delegate output", Turn: 1},
				{Role: MessageRoleUser, Content: "u2", Turn: 2},
				{Role: MessageRoleAssistant, Content: "middle answer", Turn: 2},
				{Role: MessageRoleUser, Content: "u3", Turn: 3},
				{Role: MessageRoleAssistant, Content: "recent answer", Turn: 3},
			}),
		},
		NextGenerationID: 2,
	}

	data, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("marshal lineage: %v", err)
	}

	var restored ConversationLineage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal lineage: %v", err)
	}

	got := maskConversationBeforeTurn(restored.FullMessages(), 2)
	if got[2].Content == "full delegate output" {
		t.Fatal("delegate content was not masked")
	}
	if !strings.Contains(got[2].Content, "older tool result masked") && !strings.Contains(got[2].Content, "tool result from turn") {
		t.Fatalf("fallback masking = %q, want generic placeholder", got[2].Content)
	}
}

func TestMaskConversationMasksHistoricalDelegateToolCallInputs(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1"},
		{
			Role:    MessageRoleAssistant,
			Content: "delegate work",
			Turn:    1,
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "delegate", Arguments: map[string]any{"task": "inspect the repo and summarize findings"}},
			},
		},
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_1",
			Name:       "delegate",
			Content:    "full delegate output",
			Turn:       1,
		},
		{Role: MessageRoleUser, Content: "u2"},
		{Role: MessageRoleAssistant, Content: "middle answer", Turn: 2},
		{Role: MessageRoleUser, Content: "u3"},
		{Role: MessageRoleAssistant, Content: "recent answer", Turn: 3},
	}

	got := maskConversation(messages, 1)
	if got[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("masked tool call id = %q, want preserved", got[1].ToolCalls[0].ID)
	}
	if got[1].ToolCalls[0].Name != "delegate" {
		t.Fatalf("masked tool call name = %q, want preserved", got[1].ToolCalls[0].Name)
	}
	if got[2].ToolCallID != "call_1" {
		t.Fatalf("tool result ToolCallID = %q, want preserved", got[2].ToolCallID)
	}
	if got[2].Name != "delegate" {
		t.Fatalf("tool result name = %q, want preserved", got[2].Name)
	}
	if got[1].ToolCalls[0].Arguments["task"] != "[masked historical delegate request from turn 1; see retained delegation summary in paired tool result]" {
		t.Fatalf("masked delegate args = %#v", got[1].ToolCalls[0].Arguments)
	}
	if messages[1].ToolCalls[0].Arguments["task"] != "inspect the repo and summarize findings" {
		t.Fatalf("source conversation mutated: %#v", messages[1].ToolCalls[0].Arguments)
	}
}

func TestMaskConversationLeavesRecentDelegateToolCallInputsUnmasked(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1"},
		{
			Role:    MessageRoleAssistant,
			Content: "older answer",
			Turn:    1,
		},
		{Role: MessageRoleUser, Content: "u2"},
		{
			Role:    MessageRoleAssistant,
			Content: "middle answer",
			Turn:    2,
		},
		{Role: MessageRoleUser, Content: "u3"},
		{
			Role:    MessageRoleAssistant,
			Content: "recent answer",
			Turn:    3,
			ToolCalls: []ToolCall{
				{ID: "call_recent", Name: "delegate", Arguments: map[string]any{"task": "fresh delegate task"}},
			},
		},
	}

	got := maskConversation(messages, 1)
	if got[5].ToolCalls[0].Arguments["task"] != "fresh delegate task" {
		t.Fatalf("recent delegate args = %#v, want unchanged", got[5].ToolCalls[0].Arguments)
	}
}

func TestMaskConversationLeavesNonDelegateToolCallArgumentsUnchanged(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1"},
		{
			Role:    MessageRoleAssistant,
			Content: "tool work",
			Turn:    1,
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "internal/agent/masking.go", "offset": 0}},
			},
		},
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_1",
			Name:       "read",
			Content:    "file content",
			Turn:       1,
		},
		{Role: MessageRoleUser, Content: "u2"},
		{Role: MessageRoleAssistant, Content: "middle answer", Turn: 2},
		{Role: MessageRoleUser, Content: "u3"},
		{Role: MessageRoleAssistant, Content: "recent answer", Turn: 3},
	}

	got := maskConversation(messages, 1)
	if got[1].ToolCalls[0].Arguments["path"] != "internal/agent/masking.go" {
		t.Fatalf("non-delegate args changed: %#v", got[1].ToolCalls[0].Arguments)
	}
	if got[1].ToolCalls[0].Arguments["offset"] != 0 {
		t.Fatalf("non-delegate args changed: %#v", got[1].ToolCalls[0].Arguments)
	}
}

func TestMaskConversationMasksOnlyDelegateToolCallsInMixedAssistantMessage(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "u1"},
		{
			Role:    MessageRoleAssistant,
			Content: "mixed tools",
			Turn:    7,
			ToolCalls: []ToolCall{
				{ID: "call_delegate", Name: "delegate", Arguments: map[string]any{"task": "delegate this"}},
				{ID: "call_read", Name: "read", Arguments: map[string]any{"path": "README.md"}},
			},
		},
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_delegate",
			Name:       "delegate",
			Content:    "delegate output",
			Turn:       7,
		},
		{
			Role:       MessageRoleTool,
			ToolCallID: "call_read",
			Name:       "read",
			Content:    "read output",
			Turn:       7,
		},
		{Role: MessageRoleUser, Content: "u2"},
		{Role: MessageRoleAssistant, Content: "middle answer", Turn: 8},
		{Role: MessageRoleUser, Content: "u3"},
		{Role: MessageRoleAssistant, Content: "recent answer", Turn: 9},
	}

	got := maskConversation(messages, 1)
	if got[1].ToolCalls[0].Arguments["task"] != "[masked historical delegate request from turn 7; see retained delegation summary in paired tool result]" {
		t.Fatalf("delegate args = %#v", got[1].ToolCalls[0].Arguments)
	}
	if got[1].ToolCalls[1].Arguments["path"] != "README.md" {
		t.Fatalf("non-delegate args changed in mixed tool call set: %#v", got[1].ToolCalls[1].Arguments)
	}
	if got[2].ToolCallID != "call_delegate" || got[2].Name != "delegate" {
		t.Fatalf("delegate pairing changed: %#v", got[2])
	}
	if got[3].ToolCallID != "call_read" || got[3].Name != "read" {
		t.Fatalf("read pairing changed: %#v", got[3])
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

func TestRetainedSummaryTruncationIsUTF8SafeAfterJSONRoundTrip(t *testing.T) {
	longSummary := strings.Repeat("世界", 800)
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
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
				{Role: MessageRoleUser, Content: "u2", Turn: 2},
				{Role: MessageRoleAssistant, Content: "middle answer", Turn: 2},
				{Role: MessageRoleUser, Content: "u3", Turn: 3},
				{Role: MessageRoleAssistant, Content: "recent answer", Turn: 3},
			}),
		},
		NextGenerationID: 2,
	}

	data, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("marshal lineage: %v", err)
	}

	var restored ConversationLineage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal lineage: %v", err)
	}
	if got, want := restored.Generations[0].Messages[2].Retention.Summary, longSummary; got != want {
		t.Fatalf("restored retention summary truncated before masking: got len %d, want len %d", len(got), len(want))
	}

	got := maskConversationBeforeTurn(restored.FullMessages(), 2)
	if !utf8.ValidString(got[2].Content) {
		t.Fatal("masked retained summary is not valid UTF-8")
	}
	if !strings.Contains(got[2].Content, "...") {
		t.Fatalf("masked retained summary = %q, want truncation ellipsis", got[2].Content)
	}
}
