package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestMessageConvert_ToProviderMessages(t *testing.T) {
	t.Run("empty input returns nil", func(t *testing.T) {
		if got := toProviderMessages(nil); got != nil {
			t.Errorf("expected nil for nil input, got %v", got)
		}
		if got := toProviderMessages([]Message{}); got != nil {
			t.Errorf("expected nil for empty slice, got %v", got)
		}
	})

	t.Run("summary role mapped to system", func(t *testing.T) {
		msgs := []Message{
			{Role: MessageRoleSummary, Content: "summary content"},
			{Role: MessageRoleUser, Content: "user content"},
		}
		result := toProviderMessages(msgs)
		if len(result) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(result))
		}
		if result[0].Role != provider.MessageRoleSystem {
			t.Errorf("expected system role for summary, got %s", result[0].Role)
		}
		if result[1].Role != provider.MessageRoleUser {
			t.Errorf("expected user role, got %s", result[1].Role)
		}
		if result[0].Content != "summary content" {
			t.Errorf("expected content preserved, got %s", result[0].Content)
		}
		if result[1].Content != "user content" {
			t.Errorf("expected content preserved, got %s", result[1].Content)
		}
	})

	t.Run("preserves all standard roles", func(t *testing.T) {
		msgs := []Message{
			{Role: MessageRoleUser, Content: "hello", Turn: 1},
			{Role: MessageRoleAssistant, Content: "hi", Turn: 2},
			{Role: MessageRoleTool, Content: "result", ToolCallID: "call_1", Turn: 3},
		}
		result := toProviderMessages(msgs)
		if len(result) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(result))
		}
		if result[0].Role != provider.MessageRoleUser {
			t.Errorf("expected user, got %s", result[0].Role)
		}
		if result[1].Role != provider.MessageRoleAssistant {
			t.Errorf("expected assistant, got %s", result[1].Role)
		}
		if result[2].Role != provider.MessageRoleTool {
			t.Errorf("expected tool, got %s", result[2].Role)
		}
		if result[2].ToolCallID != "call_1" {
			t.Errorf("expected ToolCallID call_1, got %s", result[2].ToolCallID)
		}
		if result[0].Turn != 1 || result[1].Turn != 2 || result[2].Turn != 3 {
			t.Fatalf("turns not preserved: %#v", result)
		}
	})

	t.Run("clones tool calls deeply", func(t *testing.T) {
		msgs := []Message{
			{
				Role: MessageRoleAssistant,
				ToolCalls: []ToolCall{
					{
						ID:   "tc_1",
						Name: "read",
						Arguments: map[string]any{
							"path": "/file",
							"nested": map[string]any{
								"key": "value",
							},
						},
					},
				},
			},
		}
		result := toProviderMessages(msgs)
		if len(result) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result))
		}
		if len(result[0].ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(result[0].ToolCalls))
		}
		tc := result[0].ToolCalls[0]
		if tc.ID != "tc_1" || tc.Name != "read" {
			t.Errorf("tool call fields mismatch: ID=%s Name=%s", tc.ID, tc.Name)
		}
		if tc.Arguments["path"] != "/file" {
			t.Errorf("expected /file, got %v", tc.Arguments["path"])
		}
		nested, ok := tc.Arguments["nested"].(map[string]any)
		if !ok {
			t.Fatal("expected nested to be a map")
		}
		if nested["key"] != "value" {
			t.Errorf("expected value, got %v", nested["key"])
		}

		tc.Arguments["new_key"] = "new_value"
		nested["key"] = "modified"
		if _, exists := msgs[0].ToolCalls[0].Arguments["new_key"]; exists {
			t.Error("original should not have new_key after modifying clone")
		}
		if msgs[0].ToolCalls[0].Arguments["nested"].(map[string]any)["key"] != "value" {
			t.Error("original nested map unchanged after modifying clone")
		}
	})
}

func TestMessageConvert_FromProviderMessages(t *testing.T) {
	t.Run("empty input returns nil", func(t *testing.T) {
		if got := fromProviderMessages(nil); got != nil {
			t.Errorf("expected nil for nil input, got %v", got)
		}
		if got := fromProviderMessages([]provider.Message{}); got != nil {
			t.Errorf("expected nil for empty slice, got %v", got)
		}
	})

	t.Run("reverses toProviderMessages", func(t *testing.T) {
		original := []Message{
			{Role: MessageRoleUser, Content: "hello", Turn: 4},
			{Role: MessageRoleAssistant, Content: "world", Name: "bot", Turn: 5},
			{Role: MessageRoleTool, Content: "result", ToolCallID: "t_1", Turn: 6},
		}
		result := fromProviderMessages(toProviderMessages(original))
		if len(result) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(result))
		}
		if result[0].Role != MessageRoleUser || result[0].Content != "hello" {
			t.Errorf("first message mismatch: %+v", result[0])
		}
		if result[1].Role != MessageRoleAssistant || result[1].Content != "world" || result[1].Name != "bot" {
			t.Errorf("second message mismatch: %+v", result[1])
		}
		if result[2].Role != MessageRoleTool || result[2].Content != "result" || result[2].ToolCallID != "t_1" {
			t.Errorf("third message mismatch: %+v", result[2])
		}
		if result[0].Turn != 4 || result[1].Turn != 5 || result[2].Turn != 6 {
			t.Fatalf("turns not preserved: %#v", result)
		}
	})

	t.Run("preserves tool calls", func(t *testing.T) {
		providerMsgs := []provider.Message{
			{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{
					{
						ID:   "tc_1",
						Name: "read",
						Arguments: map[string]any{
							"path": "/file",
						},
					},
				},
			},
		}
		result := fromProviderMessages(providerMsgs)
		if len(result) != 1 {
			t.Fatalf("expected 1 message, got %d", len(result))
		}
		if len(result[0].ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(result[0].ToolCalls))
		}
		tc := result[0].ToolCalls[0]
		if tc.ID != "tc_1" || tc.Name != "read" {
			t.Errorf("tool call mismatch: %+v", tc)
		}
		if tc.Arguments["path"] != "/file" {
			t.Errorf("expected /file, got %v", tc.Arguments["path"])
		}
	})

	t.Run("deep clone preserves independence", func(t *testing.T) {
		providerMsgs := []provider.Message{
			{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{
					{
						ID:   "tc_1",
						Name: "read",
						Arguments: map[string]any{
							"nested": map[string]any{"key": "value"},
						},
					},
				},
			},
		}
		result := fromProviderMessages(providerMsgs)
		tc := result[0].ToolCalls[0]
		tc.Arguments["new_key"] = "new_value"
		tc.Arguments["nested"].(map[string]any)["key"] = "modified"

		origNested := providerMsgs[0].ToolCalls[0].Arguments["nested"].(map[string]any)
		if origNested["key"] != "value" {
			t.Error("original nested map should be unchanged after modifying clone")
		}
		if _, exists := providerMsgs[0].ToolCalls[0].Arguments["new_key"]; exists {
			t.Error("original should not have new_key after modifying clone")
		}
	})
}

func TestMessageConvert_ToProviderMessage(t *testing.T) {
	t.Run("summary role becomes system", func(t *testing.T) {
		msg := Message{Role: MessageRoleSummary, Content: "summary"}
		result := toProviderMessage(msg)
		if result.Role != provider.MessageRoleSystem {
			t.Errorf("expected system, got %s", result.Role)
		}
		if result.Content != "summary" {
			t.Errorf("expected summary, got %s", result.Content)
		}
	})

	t.Run("context block role passes through as-is", func(t *testing.T) {
		msg := Message{Role: MessageRoleContextBlock, Content: "block"}
		result := toProviderMessage(msg)
		if result.Role != provider.MessageRole("context-block") {
			t.Errorf("expected context-block, got %s", result.Role)
		}
	})
}

func TestMessageConvert_FromProviderMessage(t *testing.T) {
	t.Run("system role maps to string system", func(t *testing.T) {
		msg := provider.Message{Role: provider.MessageRoleSystem, Content: "system"}
		result := fromProviderMessage(msg)
		if result.Role != MessageRole("system") {
			t.Errorf("expected system, got %s", result.Role)
		}
	})
}

func TestMessageConvert_LastAssistantMessage(t *testing.T) {
	t.Run("returns last assistant message", func(t *testing.T) {
		msgs := []Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "first"},
			{Role: MessageRoleTool, Content: "result"},
			{Role: MessageRoleAssistant, Content: "second"},
		}
		msg, found := LastAssistantMessage(msgs)
		if !found {
			t.Fatal("expected to find assistant message")
		}
		if msg.Content != "second" {
			t.Errorf("expected 'second', got %s", msg.Content)
		}
	})

	t.Run("returns false when no assistant message", func(t *testing.T) {
		msgs := []Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleTool, Content: "result"},
		}
		_, found := LastAssistantMessage(msgs)
		if found {
			t.Fatal("expected not to find assistant message")
		}
	})

	t.Run("returns false for nil slice", func(t *testing.T) {
		_, found := LastAssistantMessage(nil)
		if found {
			t.Fatal("expected not to find assistant message in nil slice")
		}
	})

	t.Run("returns false for empty slice", func(t *testing.T) {
		_, found := LastAssistantMessage([]Message{})
		if found {
			t.Fatal("expected not to find assistant message in empty slice")
		}
	})

	t.Run("single assistant message is found", func(t *testing.T) {
		msgs := []Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "only one"},
		}
		msg, found := LastAssistantMessage(msgs)
		if !found {
			t.Fatal("expected to find assistant message")
		}
		if msg.Content != "only one" {
			t.Errorf("expected 'only one', got %s", msg.Content)
		}
	})
}

func TestMessageConvert_AssemblyOptions(t *testing.T) {
	t.Run("prefers lineage messages over conversation", func(t *testing.T) {
		lineageMsgs := []Message{
			{Role: MessageRoleUser, Content: "lineage msg"},
		}
		convMsgs := []Message{
			{Role: MessageRoleUser, Content: "conversation msg"},
		}
		state := RunState{
			Conversation: convMsgs,
			Lineage:      newConversationLineage(lineageMsgs),
			Context:      ContextState{},
		}
		base := prompt.AssemblyOptions{}
		result := assemblyOptions(base, state)
		if len(result.Conversation) != 1 {
			t.Fatalf("expected 1 conversation message, got %d", len(result.Conversation))
		}
		if result.Conversation[0].Content != "lineage msg" {
			t.Errorf("expected 'lineage msg', got %s", result.Conversation[0].Content)
		}
	})

	t.Run("falls back to conversation when lineage empty", func(t *testing.T) {
		convMsgs := []Message{
			{Role: MessageRoleUser, Content: "fallback msg"},
		}
		state := RunState{
			Conversation: convMsgs,
			Lineage:      ConversationLineage{},
			Context:      ContextState{},
		}
		base := prompt.AssemblyOptions{}
		result := assemblyOptions(base, state)
		if len(result.Conversation) != 1 {
			t.Fatalf("expected 1 conversation message, got %d", len(result.Conversation))
		}
		if result.Conversation[0].Content != "fallback msg" {
			t.Errorf("expected 'fallback msg', got %s", result.Conversation[0].Content)
		}
	})

	t.Run("sets tool results to nil", func(t *testing.T) {
		state := RunState{
			Lineage: newConversationLineage([]Message{{Role: MessageRoleUser, Content: "hi"}}),
			Context: ContextState{},
		}
		base := prompt.AssemblyOptions{
			ToolResults: []provider.Message{{Role: provider.MessageRoleTool, Content: "old"}},
		}
		result := assemblyOptions(base, state)
		if result.ToolResults != nil {
			t.Error("expected ToolResults to be nil")
		}
	})

	t.Run("passes through non-conversation fields", func(t *testing.T) {
		state := RunState{
			Lineage: newConversationLineage(nil),
			Context: ContextState{},
		}
		base := prompt.AssemblyOptions{
			HomeDir:    "/home",
			SkillsRoot: "/skills",
		}
		result := assemblyOptions(base, state)
		if result.HomeDir != "/home" {
			t.Errorf("expected /home, got %s", result.HomeDir)
		}
		if result.SkillsRoot != "/skills" {
			t.Errorf("expected /skills, got %s", result.SkillsRoot)
		}
	})
}

func TestMessageConvert_ToPromptContext(t *testing.T) {
	t.Run("empty state produces empty slice", func(t *testing.T) {
		state := ContextState{}
		result := toPromptContext(state)
		if len(result.RetainedSummaries) != 0 {
			t.Errorf("expected 0 summaries, got %d", len(result.RetainedSummaries))
		}
	})

	t.Run("maps retained summaries correctly", func(t *testing.T) {
		state := ContextState{
			// Non-summary fields are deliberately not mapped to prompt.DurableContextState
			// since they are now rendered into the volatile zone via buildScratchpadMessage.
			ActiveConstraints: []ActiveConstraint{
				{Text: "constraint1", Source: "user", Turn: 1},
			},
			RetainedSummaries: []RetainedSummary{
				{Title: "summary1", Text: "body", Source: "compactor", Turn: 4},
			},
		}
		result := toPromptContext(state)

		if len(result.RetainedSummaries) != 1 {
			t.Fatalf("expected 1 summary, got %d", len(result.RetainedSummaries))
		}
		if result.RetainedSummaries[0] != (prompt.DurableSummaryEntry{Title: "summary1", Text: "body", Source: "compactor", Turn: 4}) {
			t.Errorf("summary mismatch: %+v", result.RetainedSummaries[0])
		}
	})
}

func TestMessageConvert_FromPromptContext(t *testing.T) {
	t.Run("empty state produces empty slice", func(t *testing.T) {
		state := prompt.DurableContextState{}
		result := fromPromptContext(state)
		if len(result.RetainedSummaries) != 0 {
			t.Errorf("expected 0 summaries, got %d", len(result.RetainedSummaries))
		}
	})

	t.Run("maps retained summaries correctly", func(t *testing.T) {
		state := prompt.DurableContextState{
			RetainedSummaries: []prompt.DurableSummaryEntry{
				{Title: "s1", Text: "body", Source: "compactor", Turn: 4},
			},
		}
		result := fromPromptContext(state)

		if len(result.RetainedSummaries) != 1 {
			t.Fatalf("expected 1 summary, got %d", len(result.RetainedSummaries))
		}
		if result.RetainedSummaries[0] != (RetainedSummary{Title: "s1", Text: "body", Source: "compactor", Turn: 4}) {
			t.Errorf("summary mismatch: %+v", result.RetainedSummaries[0])
		}
	})
}

func TestMessageConvert_PromptContextRoundTrip(t *testing.T) {
	t.Run("round-trips retained summaries", func(t *testing.T) {
		original := ContextState{
			RetainedSummaries: []RetainedSummary{
				{Title: "title", Text: "body", Source: "compactor", Turn: 4},
			},
		}
		result := fromPromptContext(toPromptContext(original))

		if len(result.RetainedSummaries) != 1 || result.RetainedSummaries[0] != (RetainedSummary{Title: "title", Text: "body", Source: "compactor", Turn: 4}) {
			t.Errorf("summary mismatch after round-trip: %+v", result.RetainedSummaries)
		}
	})
}
