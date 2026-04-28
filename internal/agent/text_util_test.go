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

func TestTextUtil_CountTurns_noUserMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		want     int
	}{
		{
			name:     "empty slice returns 0",
			messages: nil,
			want:     0,
		},
		{
			name: "only assistant returns 1",
			messages: []Message{
				{Role: MessageRoleAssistant, Content: "a"},
			},
			want: 1,
		},
		{
			name: "only tool returns 1",
			messages: []Message{
				{Role: MessageRoleTool, Content: "b"},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countTurns(tt.messages)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTextUtil_CountTurns_countsUserMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []Message
		want     int
	}{
		{
			name: "single user message",
			messages: []Message{
				{Role: MessageRoleUser, Content: "hello"},
			},
			want: 1,
		},
		{
			name: "multiple user messages",
			messages: []Message{
				{Role: MessageRoleUser, Content: "a"},
				{Role: MessageRoleAssistant, Content: "b"},
				{Role: MessageRoleUser, Content: "c"},
			},
			want: 2,
		},
		{
			name: "mixed roles only counts user",
			messages: []Message{
				{Role: MessageRoleTool, Content: "t1"},
				{Role: MessageRoleUser, Content: "u1"},
				{Role: MessageRoleAssistant, Content: "a1"},
				{Role: MessageRoleUser, Content: "u2"},
				{Role: MessageRoleSummary, Content: "s1"},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countTurns(tt.messages)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
