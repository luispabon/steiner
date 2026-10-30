package provider

import (
	"strings"
	"testing"
)

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
	if first := MessageHashInput(ab); first != MessageHashInput(ab) {
		t.Fatal("hash input must be deterministic for identical messages")
	}
}

func TestAppendMessageHashInput_MatchesMessageHashInput(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
	}{
		{name: "empty"},
		{
			name: "role_content_reasoning",
			msg:  Message{Role: MessageRoleAssistant, Content: "answer", ReasoningContent: "thinking"},
		},
		{
			name: "tool_calls_and_raw_arguments",
			msg: Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{
				{Name: "read", Arguments: map[string]any{"path": "one", "nested": map[string]any{"limit": 1}}, RawArguments: `{"raw":true}`},
				{Name: "write", Arguments: map[string]any{"path": "two"}},
			}},
		},
		{
			name: "unmarshalable_arguments",
			msg:  Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{Name: "read", Arguments: map[string]any{"invalid": func() {}}, RawArguments: "raw"}}},
		},
		{
			name: "images",
			msg: Message{Role: MessageRoleUser, Images: []ImageBlock{
				{MediaType: "image/png", Data: "aaa"},
				{MediaType: "image/jpeg", Data: "bbb"},
			}},
		},
		{
			name: "unicode_large",
			msg:  Message{Role: MessageRoleUser, Content: strings.Repeat("héllo wörld ✓\n", 512)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := MessageHashInput(tc.msg)
			if got := string(AppendMessageHashInput(nil, tc.msg)); got != want {
				t.Fatalf("AppendMessageHashInput() = %q, want %q", got, want)
			}
			// A caller reusing a buffer must get the same bytes as one
			// appending to an empty buffer.
			buf := []byte("stale-content-")
			if got := string(AppendMessageHashInput(buf[:0], tc.msg)); got != want {
				t.Fatalf("AppendMessageHashInput() over a reused buffer = %q, want %q", got, want)
			}
		})
	}
}
