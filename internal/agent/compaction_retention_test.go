package agent

import (
	"context"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
)

// TestSelectRetention pins the step-down choice on the retention fixture: three
// turns of normal tail, one tool-heavy oldest turn, budgets set to the fit points
// measured for that fixture.
func TestSelectRetention(t *testing.T) {
	messages := retentionFixtureMessages()
	tests := []struct {
		name         string
		messages     []Message
		contextSize  int
		maxUnits     int
		wantOK       bool
		wantUnits    int
		wantRetained int
	}{
		{
			name:         "largest fitting tail is three units",
			messages:     messages,
			contextSize:  6000,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    3,
			wantRetained: 7,
		},
		{
			name:         "steps down to two units when three overflow",
			messages:     messages,
			contextSize:  5060,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    2,
			wantRetained: 4,
		},
		{
			name:         "steps down to one unit when two overflow",
			messages:     messages,
			contextSize:  2100,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    1,
			wantRetained: 2,
		},
		{
			name:         "no tail fits",
			messages:     messages,
			contextSize:  1500,
			maxUnits:     3,
			wantOK:       false,
			wantUnits:    0,
			wantRetained: 2,
		},
		{
			name:         "fewer units than maxUnits keeps all of them",
			messages:     messages[5:],
			contextSize:  6000,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    2,
			wantRetained: 4,
		},
		{
			name:         "unknown window accepts maxUnits",
			messages:     messages,
			contextSize:  0,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    3,
			wantRetained: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerStub := &fakeProvider{}
			req := retentionFixtureRequest(tt.contextSize, providerStub)
			state := RunState{Lineage: newConversationLineage(tt.messages)}
			source, retained, units, ok, err := selectRetention(context.Background(), req, state, tt.messages, tt.messages, prompt.CompactionModeNormal, tt.maxUnits)
			if err != nil {
				t.Fatalf("selectRetention() error = %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if units != tt.wantUnits {
				t.Fatalf("units = %d, want %d", units, tt.wantUnits)
			}
			if len(retained) != tt.wantRetained {
				t.Fatalf("retained = %d messages, want %d", len(retained), tt.wantRetained)
			}
			if got, want := len(source)+len(retained), len(tt.messages); got != want {
				t.Fatalf("source+retained = %d messages, want %d (a partition of the conversation)", got, want)
			}
			if got := len(providerStub.requests); got != 0 {
				t.Fatalf("model calls = %d, want 0 (selection is computation only)", got)
			}
		})
	}
}

// TestWithoutProtectedOutputs checks which unit messages the model has not read
// yet: tool and sub-agent results after the unit's last assistant message.
func TestWithoutProtectedOutputs(t *testing.T) {
	subAgent := Message{Role: MessageRoleUser, Content: "sub", Source: MessageSourceSubAgentResult}
	tests := []struct {
		name string
		unit []Message
		want []string
	}{
		{
			name: "tool results after the last assistant are protected",
			unit: []Message{
				{Role: MessageRoleUser, Content: "u"},
				{Role: MessageRoleAssistant, Content: "a"},
				{Role: MessageRoleTool, Content: "t1"},
				{Role: MessageRoleTool, Content: "t2"},
			},
			want: []string{"user:u", "assistant:a"},
		},
		{
			name: "tool results before the last assistant stay",
			unit: []Message{
				{Role: MessageRoleAssistant, Content: "a1"},
				{Role: MessageRoleTool, Content: "t1"},
				{Role: MessageRoleAssistant, Content: "a2"},
				{Role: MessageRoleTool, Content: "t2"},
			},
			want: []string{"assistant:a1", "tool:t1", "assistant:a2"},
		},
		{
			name: "sub-agent result alone is protected",
			unit: []Message{subAgent},
			want: nil,
		},
		{
			name: "unit without an assistant protects its tool results",
			unit: []Message{
				{Role: MessageRoleUser, Content: "u"},
				{Role: MessageRoleTool, Content: "t"},
			},
			want: []string{"user:u"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make([]string, 0, len(tt.unit))
			for _, message := range withoutProtectedOutputs(tt.unit) {
				got = append(got, string(message.Role)+":"+message.Content)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("withoutProtectedOutputs() = %q, want %q", got, tt.want)
			}
		})
	}
}
