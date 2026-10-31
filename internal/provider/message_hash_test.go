package provider

import "testing"

func TestAppendMessageHashInput_PreservesContract(t *testing.T) {
	msg := Message{
		Role:    MessageRoleAssistant,
		Content: "answer",
		ToolCalls: []ToolCall{
			{Name: "read", Arguments: map[string]any{"path": "one"}, RawArguments: `{"raw":true}`},
			{Name: "write", Arguments: map[string]any{"path": "two"}},
		},
	}

	if got, want := string(AppendMessageHashInput(nil, msg)), `assistantanswerread{"path":"one"}{"raw":true}write{"path":"two"}`; got != want {
		t.Fatalf("AppendMessageHashInput() = %q, want %q", got, want)
	}
}

func TestAppendMessageHashInput_SkipsArgumentsWhenMarshalFails(t *testing.T) {
	msg := Message{
		Role: MessageRoleAssistant,
		ToolCalls: []ToolCall{{
			Name:         "read",
			Arguments:    map[string]any{"invalid": func() {}},
			RawArguments: "raw",
		}},
	}

	if got, want := string(AppendMessageHashInput(nil, msg)), "assistantreadraw"; got != want {
		t.Fatalf("AppendMessageHashInput() = %q, want %q", got, want)
	}
}

func TestAppendMessageHashInput_EmptyMessage(t *testing.T) {
	if got := string(AppendMessageHashInput(nil, Message{})); got != "" {
		t.Fatalf("AppendMessageHashInput(Message{}) = %q, want empty", got)
	}
}

func TestAppendMessageHashInput_DistinguishesReasoningContent(t *testing.T) {
	base := Message{Role: MessageRoleAssistant, Content: "answer"}
	withReasoning := base
	withReasoning.ReasoningContent = "thinking"
	if string(AppendMessageHashInput(nil, base)) == string(AppendMessageHashInput(nil, withReasoning)) {
		t.Fatal("reasoning content must affect the hash input")
	}
}

func TestAppendMessageHashInput_DistinguishesImages(t *testing.T) {
	first := Message{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{{MediaType: "image/png", Data: "aaa"}}}
	second := Message{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{{MediaType: "image/png", Data: "bbb"}}}
	if string(AppendMessageHashInput(nil, first)) == string(AppendMessageHashInput(nil, second)) {
		t.Fatal("image data must affect the hash input")
	}
	withImage := Message{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{{MediaType: "image/png", Data: "aaa"}}}
	withoutImage := Message{Role: MessageRoleUser, Content: "look"}
	if string(AppendMessageHashInput(nil, withImage)) == string(AppendMessageHashInput(nil, withoutImage)) {
		t.Fatal("image presence must affect the hash input")
	}
}

func TestAppendMessageHashInput_ImageOrderAndDeterminism(t *testing.T) {
	ab := Message{Role: MessageRoleUser, Images: []ImageBlock{{MediaType: "image/png", Data: "a"}, {MediaType: "image/png", Data: "b"}}}
	ba := Message{Role: MessageRoleUser, Images: []ImageBlock{{MediaType: "image/png", Data: "b"}, {MediaType: "image/png", Data: "a"}}}
	if string(AppendMessageHashInput(nil, ab)) == string(AppendMessageHashInput(nil, ba)) {
		t.Fatal("image order must affect the hash input")
	}
	if first := string(AppendMessageHashInput(nil, ab)); first != string(AppendMessageHashInput(nil, ab)) {
		t.Fatal("hash input must be deterministic for identical messages")
	}
}

func TestAppendMessageHashInput_LiteralBytes(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want string
	}{
		{
			name: "role_then_content_then_reasoning",
			msg:  Message{Role: MessageRoleAssistant, Content: "answer", ReasoningContent: "thinking"},
			want: "assistantanswerthinking",
		},
		{
			name: "tool_args_in_call_order_then_raw_args",
			msg: Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{
				{Name: "read", Arguments: map[string]any{"path": "one", "limit": 2}, RawArguments: `{"raw":true}`},
				{Name: "write", Arguments: map[string]any{"path": "two"}},
			}},
			want: `assistantread{"limit":2,"path":"one"}{"raw":true}write{"path":"two"}`,
		},
		{
			name: "images_in_order_with_media_type",
			msg: Message{Role: MessageRoleUser, Images: []ImageBlock{
				{MediaType: "image/png", Data: "aaa"},
				{MediaType: "image/jpeg", Data: "bbb"},
			}},
			want: "userimage/pngaaaimage/jpegbbb",
		},
		{
			name: "unicode_content",
			msg:  Message{Role: MessageRoleUser, Content: "héllo wörld ✓"},
			want: "userhéllo wörld ✓",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(AppendMessageHashInput(nil, tc.msg)); got != tc.want {
				t.Fatalf("AppendMessageHashInput() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAppendMessageHashInput_ReusedBuffer proves a caller that reuses one
// buffer still gets only the bytes of the current message.
func TestAppendMessageHashInput_ReusedBuffer(t *testing.T) {
	msg := Message{Role: MessageRoleAssistant, Content: "answer", ReasoningContent: "thinking"}
	buf := []byte("stale-content-")
	if got, want := string(AppendMessageHashInput(buf[:0], msg)), "assistantanswerthinking"; got != want {
		t.Fatalf("AppendMessageHashInput() over a reused buffer = %q, want %q", got, want)
	}
}
