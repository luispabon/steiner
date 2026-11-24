package agent

import (
	"testing"
)

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
			got := countSummarizedMessages(tt.messages, nil)
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
