package agent

import (
	"fmt"
	"reflect"
	"strings"
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

func TestReplaySafeConversation_DropsIncompleteToolRun(t *testing.T) {
	assistant := Message{
		Role:    MessageRoleAssistant,
		Content: "running searches",
		ToolCalls: []ToolCall{
			{ID: "call-1", Name: "grep"},
			{ID: "call-2", Name: "read"},
		},
	}

	cases := []struct {
		name string
		tail []Message
	}{
		{
			name: "short run",
			tail: []Message{
				{Role: MessageRoleTool, Content: "first match", ToolCallID: "call-1"},
			},
		},
		{
			name: "mismatched call id",
			tail: []Message{
				{Role: MessageRoleTool, Content: "first match", ToolCallID: "call-1"},
				{Role: MessageRoleTool, Content: "wrong call", ToolCallID: "call-9"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conversation := append([]Message{{Role: MessageRoleUser, Content: "investigate"}, assistant}, tc.tail...)
			conversation = append(conversation, Message{Role: MessageRoleAssistant, Content: "done"})

			got := ReplaySafeConversation(conversation)
			if len(got) != 3 {
				t.Fatalf("len(got) = %d, want 3", len(got))
			}
			if len(got[1].ToolCalls) != 0 {
				t.Fatalf("got[1].ToolCalls = %#v, want cleared partial tool calls", got[1].ToolCalls)
			}
			if got[2].Role != MessageRoleAssistant || got[2].Content != "done" {
				t.Fatalf("got[2] = %#v, want trailing assistant preserved", got[2])
			}
		})
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

// TestReplaySafeConversation_PreservesElidedToolResultPairing proves replay
// keeps a tool call paired with the recall marker that replaced its result,
// while a verbatim result on the same assistant message stays untouched.
func TestReplaySafeConversation_PreservesElidedToolResultPairing(t *testing.T) {
	marker := recallMarker("tool-call-1-0123456789abcdef", "/recall/tool-call-1-0123456789abcdef")
	conversation := []Message{
		{Role: MessageRoleUser, Content: "investigate"},
		{
			Role:    MessageRoleAssistant,
			Content: "running searches",
			ToolCalls: []ToolCall{
				{ID: "call-1", Name: "grep", Arguments: map[string]any{"pattern": "foo"}},
				{ID: "call-2", Name: "read", Arguments: map[string]any{"path": "bar.go"}},
			},
		},
		{Role: MessageRoleTool, Content: marker, ToolCallID: "call-1", Name: "grep"},
		{Role: MessageRoleTool, Content: "full result", ToolCallID: "call-2", Name: "read"},
		{Role: MessageRoleAssistant, Content: "done"},
	}

	got := ReplaySafeConversation(conversation)
	if len(got) != len(conversation) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(conversation))
	}
	if len(got[1].ToolCalls) != 2 {
		t.Fatalf("got[1].ToolCalls = %#v, want both paired tool calls preserved", got[1].ToolCalls)
	}
	if got[2].Role != MessageRoleTool || got[2].ToolCallID != "call-1" || got[2].Content != marker {
		t.Fatalf("got[2] = %#v, want the marker result still paired to call-1", got[2])
	}
	if got[3].Role != MessageRoleTool || got[3].ToolCallID != "call-2" || got[3].Content != "full result" {
		t.Fatalf("got[3] = %#v, want the verbatim result still paired to call-2", got[3])
	}
}

// TestElidedSubAgentResultEnvelopeKeepsMarkerInner proves the envelope that
// elision writes still parses: every attribute survives, the byte length counts
// the marker, and Inner is exactly the marker with no usage preamble.
func TestElidedSubAgentResultEnvelopeKeepsMarkerInner(t *testing.T) {
	raw := RenderSubAgentResultEnvelope(SubAgentCompletion{
		Seq:              1,
		ParentCallID:     "call-1",
		AgentID:          "agent-a",
		AgentType:        "explore",
		Status:           "completed",
		ObjectivePreview: "objective",
		TurnCount:        3,
		TokenCount:       120,
		Body:             `{"output":"found it"}`,
	})
	marker := recallMarker("agent-call-1-0123456789abcdef", "/recall/agent-call-1-0123456789abcdef")
	elided, ok := ReplaceSubAgentResultInner(raw, marker)
	if !ok {
		t.Fatalf("ReplaceSubAgentResultInner failed for the rendered envelope")
	}
	parsed, ok := ParseSubAgentResultEnvelope(elided)
	if !ok {
		t.Fatalf("elided envelope does not parse: %q", elided)
	}
	want := ParsedSubAgentResult{
		AgentID:   "agent-a",
		AgentType: "explore",
		Status:    "completed",
		CallID:    "call-1",
		Inner:     marker,
	}
	if parsed != want {
		t.Fatalf("parsed = %+v, want %+v", parsed, want)
	}
	if bytes := fmt.Sprintf(`bytes="%d"`, len(marker)); !strings.Contains(elided, bytes) {
		t.Fatalf("elided envelope lacks rewritten %s: %q", bytes, elided)
	}
}
