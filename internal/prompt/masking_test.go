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
			Role:             provider.MessageRoleAssistant,
			Content:          "tool turn 1",
			ReasoningContent: "reasoning",
			ToolCalls: []provider.ToolCall{
				{ID: "call_1", Name: "grep", Arguments: map[string]any{"pattern": "ContextManager", "path": "internal/agent"}},
			},
			ProviderMetadata: &provider.MessageProviderMetadata{
				Anthropic: &provider.AnthropicMessageMetadata{ThinkingSignature: "sig_123"},
			},
		},
		{Role: provider.MessageRoleTool, ToolCallID: "call_1", Name: "grep", Content: "raw grep output"},
		{Role: provider.MessageRoleUser, Content: "user 2"},
		{
			Role:    provider.MessageRoleAssistant,
			Content: "tool turn 2",
			ToolCalls: []provider.ToolCall{
				{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "foo.go"}},
			},
		},
		{Role: provider.MessageRoleTool, ToolCallID: "call_2", Name: "read", Content: "file body"},
		{Role: provider.MessageRoleUser, Content: "user 3"},
		{Role: provider.MessageRoleAssistant, Content: "recent answer\nwith more detail"},
	}

	got := MaskConversation(messages, 1)

	// Turn 1 tool result should be masked.
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
	if got[1].ProviderMetadata == nil || got[1].ProviderMetadata.Anthropic == nil {
		t.Fatalf("assistant provider metadata lost: %#v", got[1].ProviderMetadata)
	}
	if got, want := got[1].ProviderMetadata.Anthropic.ThinkingSignature, "sig_123"; got != want {
		t.Fatalf("assistant thinking signature = %q, want %q", got, want)
	}
	// Turn 2 tool result should be preserved (within the 2-turn grace window).
	if got[5].Content != messages[5].Content {
		t.Fatalf("turn 2 tool result content = %q, want unmasked", got[5].Content)
	}
}

func TestMaskConversationTrimsOlderAssistantProse(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "u1"},
		{Role: provider.MessageRoleAssistant, Content: "first line\nsecond line\nthird line"},
		{Role: provider.MessageRoleUser, Content: "u2"},
		{Role: provider.MessageRoleAssistant, Content: "second turn"},
		{Role: provider.MessageRoleUser, Content: "u3"},
		{Role: provider.MessageRoleAssistant, Content: "keep all lines\nsecond line"},
	}

	got := MaskConversation(messages, 1)

	// No Turn set — masked content is first line only (no turn prefix).
	if got[1].Content != "first line" {
		t.Fatalf("older assistant content = %q, want first line only", got[1].Content)
	}
	if got[3].Content != messages[3].Content {
		t.Fatalf("middle assistant content = %q, want unchanged", got[3].Content)
	}
	if got[5].Content != messages[5].Content {
		t.Fatalf("recent assistant content = %q, want unchanged", got[5].Content)
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

func TestMaskConversationIncludesTurnInMaskedText(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "u1", Turn: 1},
		{
			Role:    provider.MessageRoleAssistant,
			Content: "first answer",
			Turn:    1,
			ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "read", Arguments: map[string]any{"path": "foo.go"}},
			},
		},
		{Role: provider.MessageRoleTool, ToolCallID: "c1", Name: "read", Content: "file body", Turn: 1},
		{Role: provider.MessageRoleUser, Content: "u2", Turn: 2},
		{Role: provider.MessageRoleAssistant, Content: "middle", Turn: 2},
		{Role: provider.MessageRoleUser, Content: "u3", Turn: 3},
		{Role: provider.MessageRoleAssistant, Content: "recent", Turn: 3},
	}

	got := MaskConversation(messages, 1)

	if !strings.Contains(got[2].Content, "turn 1") {
		t.Fatalf("masked tool result = %q, want turn reference", got[2].Content)
	}
	if !strings.Contains(got[2].Content, "re-read if needed") {
		t.Fatalf("masked tool result = %q, want re-read hint", got[2].Content)
	}
	if !strings.Contains(got[1].Content, "[turn 1]") {
		t.Fatalf("masked assistant = %q, want turn prefix", got[1].Content)
	}
	// Turn 2 and 3 should remain unmasked.
	if got[4].Content != messages[4].Content {
		t.Fatalf("middle assistant content = %q, want unchanged", got[4].Content)
	}
	if got[6].Content != messages[6].Content {
		t.Fatalf("recent assistant content = %q, want unchanged", got[6].Content)
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
		{Role: provider.MessageRoleUser, Content: "u2"},
		{Role: provider.MessageRoleAssistant, Content: "middle"},
		{Role: provider.MessageRoleUser, Content: "u3"},
		{Role: provider.MessageRoleAssistant, Content: "recent"},
	}

	got := MaskConversation(messages, 1)

	if !strings.Contains(got[2].Content, "read") {
		t.Fatalf("first masked tool result = %q, want read metadata", got[2].Content)
	}
	if !strings.Contains(got[3].Content, "grep") {
		t.Fatalf("second masked tool result = %q, want grep metadata", got[3].Content)
	}
	// Turn 2 and 3 should remain unmasked.
	if got[5].Content != messages[5].Content {
		t.Fatalf("middle assistant content = %q, want unchanged", got[5].Content)
	}
	if got[7].Content != messages[7].Content {
		t.Fatalf("recent assistant content = %q, want unchanged", got[7].Content)
	}
}

func TestMaskConversationGracePeriod(t *testing.T) {
	messages := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "u1"},
		{Role: provider.MessageRoleAssistant, Content: "turn 1"},
		{Role: provider.MessageRoleUser, Content: "u2"},
		{Role: provider.MessageRoleAssistant, Content: "turn 2"},
	}

	// With only 2 assistant turns and windowTurns=1, the effective window is 2,
	// so nothing should be masked.
	got := MaskConversation(messages, 1)

	for i := range got {
		if got[i].Content != messages[i].Content {
			t.Fatalf("message %d content = %q, want %q", i, got[i].Content, messages[i].Content)
		}
	}
}

func TestMaskConversationBeforeTurnKeepsMaskedPrefixStable(t *testing.T) {
	base := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "u1", Turn: 1},
		{
			Role:    provider.MessageRoleAssistant,
			Content: "turn 1 answer",
			Turn:    1,
			ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "read", Arguments: map[string]any{"path": "a.go"}},
			},
			ProviderMetadata: &provider.MessageProviderMetadata{
				Anthropic: &provider.AnthropicMessageMetadata{ThinkingSignature: "sig_456"},
			},
		},
		{Role: provider.MessageRoleTool, ToolCallID: "c1", Name: "read", Content: "file body", Turn: 1},
		{Role: provider.MessageRoleUser, Content: "u2", Turn: 2},
		{Role: provider.MessageRoleAssistant, Content: "turn 2 answer", Turn: 2},
		{Role: provider.MessageRoleTool, Name: "bash", Content: "bash body", Turn: 2},
		{Role: provider.MessageRoleUser, Content: "u3", Turn: 3},
		{Role: provider.MessageRoleAssistant, Content: "turn 3 answer", Turn: 3},
	}
	extended := append(provider.CloneMessages(base), provider.Message{Role: provider.MessageRoleUser, Content: "u4", Turn: 4})
	extended = append(extended, provider.Message{Role: provider.MessageRoleAssistant, Content: "turn 4 answer", Turn: 4})

	gotBase := MaskConversationBeforeTurn(base, 3)
	gotExtended := MaskConversationBeforeTurn(extended, 3)

	for i := 0; i < len(gotBase); i++ {
		if gotBase[i].Content != gotExtended[i].Content {
			t.Fatalf("masked prefix message %d content = %q, want %q", i, gotBase[i].Content, gotExtended[i].Content)
		}
		if gotBase[i].Role != gotExtended[i].Role {
			t.Fatalf("masked prefix message %d role = %q, want %q", i, gotBase[i].Role, gotExtended[i].Role)
		}
	}
	if gotBase[1].Content == base[1].Content {
		t.Fatalf("turn 1 assistant content = %q, want masked", gotBase[1].Content)
	}
	if gotBase[1].ProviderMetadata == nil || gotBase[1].ProviderMetadata.Anthropic == nil {
		t.Fatalf("turn 1 provider metadata lost: %#v", gotBase[1].ProviderMetadata)
	}
	if gotBase[7].Content != base[7].Content {
		t.Fatalf("turn 3 assistant content = %q, want unmasked", gotBase[7].Content)
	}
}
