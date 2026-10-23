package agent

import (
	"encoding/json"
	"testing"
)

// markedAgentToolCall is a malformed call as the provider layer records it.
func markedAgentToolCall() ToolCall {
	return ToolCall{
		ID:                  "call_bad",
		Name:                "read",
		Arguments:           map[string]any{},
		RawArguments:        "{}",
		ArgumentDecodeError: `decode "read" arguments: unexpected end of JSON input`,
	}
}

func assertMarkedAgentToolCall(t *testing.T, call ToolCall) {
	t.Helper()
	if call.ArgumentDecodeError != `decode "read" arguments: unexpected end of JSON input` {
		t.Fatalf("ArgumentDecodeError = %q, want the decode message", call.ArgumentDecodeError)
	}
	if call.RawArguments != "{}" {
		t.Fatalf("RawArguments = %q, want {}", call.RawArguments)
	}
	if len(call.Arguments) != 0 {
		t.Fatalf("Arguments = %#v, want empty", call.Arguments)
	}
}

func TestMessageConvertPreservesArgumentDecodeError(t *testing.T) {
	msg := Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{markedAgentToolCall()}}

	provider := toProviderMessage(msg)
	if len(provider.ToolCalls) != 1 {
		t.Fatalf("provider tool calls = %d, want 1", len(provider.ToolCalls))
	}
	assertMarkedAgentToolCall(t, ToolCall{
		ID:                  provider.ToolCalls[0].ID,
		Name:                provider.ToolCalls[0].Name,
		Arguments:           provider.ToolCalls[0].Arguments,
		RawArguments:        provider.ToolCalls[0].RawArguments,
		ArgumentDecodeError: provider.ToolCalls[0].ArgumentDecodeError,
	})

	back := fromProviderMessage(provider)
	if len(back.ToolCalls) != 1 {
		t.Fatalf("round-trip tool calls = %d, want 1", len(back.ToolCalls))
	}
	assertMarkedAgentToolCall(t, back.ToolCalls[0])
}

func TestCloneMessagesPreservesArgumentDecodeError(t *testing.T) {
	msgs := []Message{{Role: MessageRoleAssistant, ToolCalls: []ToolCall{markedAgentToolCall()}}}
	cloned := CloneMessages(msgs)
	if len(cloned[0].ToolCalls) != 1 {
		t.Fatalf("cloned tool calls = %d, want 1", len(cloned[0].ToolCalls))
	}
	assertMarkedAgentToolCall(t, cloned[0].ToolCalls[0])
}

// TestConversationLineageJSONPreservesArgumentDecodeError checks that a
// marked call survives the JSON form that session storage writes and reads.
func TestConversationLineageJSONPreservesArgumentDecodeError(t *testing.T) {
	lineage := ConversationLineage{
		Generations: []ConversationGeneration{
			newConversationGeneration(1, nil, []Message{
				{Role: MessageRoleUser, Content: "read it"},
				{Role: MessageRoleAssistant, ToolCalls: []ToolCall{markedAgentToolCall()}},
			}),
		},
		NextGenerationID: 2,
	}
	data, err := json.Marshal(lineage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored ConversationLineage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	messages := restored.Generations[0].Messages
	if len(messages) != 2 || len(messages[1].ToolCalls) != 1 {
		t.Fatalf("restored messages = %+v, want assistant with one tool call", messages)
	}
	assertMarkedAgentToolCall(t, messages[1].ToolCalls[0])

	provider := toProviderMessage(messages[1])
	if provider.ToolCalls[0].RawArguments != "{}" || provider.ToolCalls[0].ArgumentDecodeError == "" {
		t.Fatalf("provider call after restore = %+v, want marked empty object", provider.ToolCalls[0])
	}
}
