package provider

import "testing"

func TestMessageHashInput_PreservesContract(t *testing.T) {
	msg := Message{
		Role:    MessageRoleAssistant,
		Content: "answer",
		ToolCalls: []ToolCall{
			{Name: "read", Arguments: map[string]any{"path": "one"}, RawArguments: `{"raw":true}`},
			{Name: "write", Arguments: map[string]any{"path": "two"}},
		},
	}

	if got, want := MessageHashInput(msg), `assistantanswerread{"path":"one"}{"raw":true}write{"path":"two"}`; got != want {
		t.Fatalf("MessageHashInput() = %q, want %q", got, want)
	}
}

func TestMessageHashInput_SkipsArgumentsWhenMarshalFails(t *testing.T) {
	msg := Message{
		Role: MessageRoleAssistant,
		ToolCalls: []ToolCall{{
			Name:         "read",
			Arguments:    map[string]any{"invalid": func() {}},
			RawArguments: "raw",
		}},
	}

	if got, want := MessageHashInput(msg), "assistantreadraw"; got != want {
		t.Fatalf("MessageHashInput() = %q, want %q", got, want)
	}
}

func TestMessageHashInput_EmptyMessage(t *testing.T) {
	if got := MessageHashInput(Message{}); got != "" {
		t.Fatalf("MessageHashInput(Message{}) = %q, want empty", got)
	}
}
