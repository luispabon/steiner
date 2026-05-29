package provider

import (
	"testing"
)

func TestChatRequestWire_IncludeEmptyReasoning_FillsAssistantMessages(t *testing.T) {
	tests := []struct {
		name                  string
		messages              []Message
		includeEmptyReasoning bool
		wantReasoningNil      []bool // per-message: true = nil expected, false = non-nil expected
	}{
		{
			name: "fills nil reasoning on assistant messages when flag set",
			messages: []Message{
				{Role: MessageRoleUser, Content: "hi"},
				{Role: MessageRoleAssistant, Content: "hello"},
				{Role: MessageRoleAssistant, Content: "world", ReasoningContent: "thinking"},
			},
			includeEmptyReasoning: true,
			wantReasoningNil:      []bool{true, false, false},
		},
		{
			name: "leaves messages unchanged when flag not set",
			messages: []Message{
				{Role: MessageRoleUser, Content: "hi"},
				{Role: MessageRoleAssistant, Content: "hello"},
			},
			includeEmptyReasoning: false,
			wantReasoningNil:      []bool{true, true},
		},
		{
			name: "does not fill reasoning on user or tool messages",
			messages: []Message{
				{Role: MessageRoleUser, Content: "hi"},
				{Role: MessageRoleTool, Content: "result", ToolCallID: "id1"},
				{Role: MessageRoleAssistant, Content: "ok"},
			},
			includeEmptyReasoning: true,
			wantReasoningNil:      []bool{true, true, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ChatRequest{
				Model:                 "test-model",
				Messages:              tt.messages,
				IncludeEmptyReasoning: tt.includeEmptyReasoning,
			}
			wire, err := chatRequestWire(req, "test-model", false)
			if err != nil {
				t.Fatalf("chatRequestWire() error = %v", err)
			}
			if len(wire.Messages) != len(tt.wantReasoningNil) {
				t.Fatalf("got %d messages, want %d", len(wire.Messages), len(tt.wantReasoningNil))
			}
			for i, wantNil := range tt.wantReasoningNil {
				gotNil := wire.Messages[i].ReasoningContent == nil
				if gotNil != wantNil {
					t.Errorf("message[%d] ReasoningContent nil = %v, want %v", i, gotNil, wantNil)
				}
			}
		})
	}
}

func TestChatRequestWire_IncludeEmptyReasoning_EmptyStringValue(t *testing.T) {
	req := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: MessageRoleAssistant, Content: "answer"},
		},
		IncludeEmptyReasoning: true,
	}
	wire, err := chatRequestWire(req, "test-model", false)
	if err != nil {
		t.Fatalf("chatRequestWire() error = %v", err)
	}
	if wire.Messages[0].ReasoningContent == nil {
		t.Fatal("ReasoningContent = nil, want pointer to empty string")
	}
	if *wire.Messages[0].ReasoningContent != "" {
		t.Fatalf("*ReasoningContent = %q, want empty string", *wire.Messages[0].ReasoningContent)
	}
}

func TestChatRequestWire_IncludeEmptyReasoning_PreservesExistingReasoning(t *testing.T) {
	req := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: MessageRoleAssistant, Content: "answer", ReasoningContent: "deep thought"},
		},
		IncludeEmptyReasoning: true,
	}
	wire, err := chatRequestWire(req, "test-model", false)
	if err != nil {
		t.Fatalf("chatRequestWire() error = %v", err)
	}
	if wire.Messages[0].ReasoningContent == nil {
		t.Fatal("ReasoningContent = nil, want non-nil")
	}
	if *wire.Messages[0].ReasoningContent != "deep thought" {
		t.Fatalf("*ReasoningContent = %q, want %q", *wire.Messages[0].ReasoningContent, "deep thought")
	}
}
