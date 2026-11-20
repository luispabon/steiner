package agent

import (
	"testing"
)

func TestTextUtil_SummarizeConversationMessages_empty(t *testing.T) {
	got := summarizeConversationMessages(nil, 5)
	if got != "none recorded" {
		t.Errorf("got %q, want %q", got, "none recorded")
	}
	got = summarizeConversationMessages([]Message{}, 5)
	if got != "none recorded" {
		t.Errorf("got %q, want %q", got, "none recorded")
	}
}

func TestTextUtil_SummarizeConversationMessages_respectsMaxMessages(t *testing.T) {
	tests := []struct {
		name        string
		messages    []Message
		maxMessages int
		want        string
	}{
		{
			name: "zero max uses all",
			messages: []Message{
				{Role: MessageRoleUser, Content: "hello"},
				{Role: MessageRoleAssistant, Content: "hi"},
			},
			maxMessages: 0,
			want:        "user: hello | assistant: hi",
		},
		{
			name: "negative max uses all",
			messages: []Message{
				{Role: MessageRoleUser, Content: "a"},
				{Role: MessageRoleAssistant, Content: "b"},
			},
			maxMessages: -1,
			want:        "user: a | assistant: b",
		},
		{
			name: "max larger than length uses all",
			messages: []Message{
				{Role: MessageRoleUser, Content: "x"},
			},
			maxMessages: 10,
			want:        "user: x",
		},
		{
			name: "keeps most recent when limited",
			messages: []Message{
				{Role: MessageRoleUser, Content: "first"},
				{Role: MessageRoleAssistant, Content: "second"},
				{Role: MessageRoleUser, Content: "third"},
			},
			maxMessages: 2,
			want:        "assistant: second | user: third",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeConversationMessages(tt.messages, tt.maxMessages)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTextUtil_FirstMessageContentByRole(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		role     MessageRole
		want     string
	}{
		{
			name:     "no messages returns empty",
			messages: nil,
			role:     MessageRoleUser,
			want:     "",
		},
		{
			name: "skips whitespace-only content",
			messages: []Message{
				{Role: MessageRoleUser, Content: "  "},
				{Role: MessageRoleUser, Content: "real content"},
			},
			role: MessageRoleUser,
			want: "real content",
		},
		{
			name: "returns first match for role",
			messages: []Message{
				{Role: MessageRoleAssistant, Content: "first"},
				{Role: MessageRoleUser, Content: "second"},
				{Role: MessageRoleAssistant, Content: "third"},
			},
			role: MessageRoleAssistant,
			want: "first",
		},
		{
			name: "returns empty when role not found",
			messages: []Message{
				{Role: MessageRoleUser, Content: "hello"},
			},
			role: MessageRoleAssistant,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstMessageContentByRole(tt.messages, tt.role)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTextUtil_CountSummarizedMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		want     int
	}{
		{
			name:     "empty",
			messages: nil,
			want:     0,
		},
		{
			name: "prior summary is not summarized again",
			messages: []Message{
				{Role: MessageRoleSummary, Content: "prior summary"},
				{Role: MessageRoleUser, Content: "question"},
				{Role: MessageRoleAssistant, Content: "answer"},
			},
			want: 2,
		},
		{
			name: "counts every non-summary role",
			messages: []Message{
				{Role: MessageRoleUser, Content: "question"},
				{Role: MessageRoleAssistant, Content: "answer"},
				{Role: MessageRoleTool, Content: "output"},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countSummarizedMessages(tt.messages)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
