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

func TestMessageHashInput_DistinguishesReasoningContent(t *testing.T) {
	base := Message{Role: MessageRoleAssistant, Content: "answer"}
	withReasoning := base
	withReasoning.ReasoningContent = "thinking"
	if MessageHashInput(base) == MessageHashInput(withReasoning) {
		t.Fatal("reasoning content must affect the hash input")
	}
}

func TestMessageHashInput_DistinguishesImages(t *testing.T) {
	first := Message{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{{MediaType: "image/png", Data: "aaa"}}}
	second := Message{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{{MediaType: "image/png", Data: "bbb"}}}
	if MessageHashInput(first) == MessageHashInput(second) {
		t.Fatal("image data must affect the hash input")
	}
	withImage := Message{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{{MediaType: "image/png", Data: "aaa"}}}
	withoutImage := Message{Role: MessageRoleUser, Content: "look"}
	if MessageHashInput(withImage) == MessageHashInput(withoutImage) {
		t.Fatal("image presence must affect the hash input")
	}
}

func TestMessageHashInput_ImageOrderAndDeterminism(t *testing.T) {
	ab := Message{Role: MessageRoleUser, Images: []ImageBlock{{MediaType: "image/png", Data: "a"}, {MediaType: "image/png", Data: "b"}}}
	ba := Message{Role: MessageRoleUser, Images: []ImageBlock{{MediaType: "image/png", Data: "b"}, {MediaType: "image/png", Data: "a"}}}
	if MessageHashInput(ab) == MessageHashInput(ba) {
		t.Fatal("image order must affect the hash input")
	}
	if MessageHashInput(ab) != MessageHashInput(ab) {
		t.Fatal("hash input must be deterministic for identical messages")
	}
}
