package agent

import (
	"context"
	"testing"
)

func TestNaiveContextManagerPostIngestion(t *testing.T) {
	tests := []struct {
		name  string
		state RunState
	}{
		{
			name:  "empty state passes through unchanged",
			state: RunState{},
		},
		{
			name: "state with conversation passes through unchanged",
			state: RunState{
				Conversation: []Message{
					{Role: MessageRoleUser, Content: "hello"},
				},
				TurnCount: 3,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &NaiveContextManager{}
			got, err := m.PostIngestion(context.Background(), tc.state)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.TurnCount != tc.state.TurnCount {
				t.Errorf("TurnCount: got %d, want %d", got.TurnCount, tc.state.TurnCount)
			}
			if len(got.Conversation) != len(tc.state.Conversation) {
				t.Errorf("Conversation len: got %d, want %d", len(got.Conversation), len(tc.state.Conversation))
			}
		})
	}
}

func TestNaiveContextManagerPreAssembly(t *testing.T) {
	tests := []struct {
		name  string
		state RunState
	}{
		{
			name:  "empty state passes through unchanged",
			state: RunState{},
		},
		{
			name: "state with context passes through unchanged",
			state: RunState{
				TurnCount:  5,
				TokenCount: 1000,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &NaiveContextManager{}
			got, err := m.PreAssembly(context.Background(), tc.state)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.TurnCount != tc.state.TurnCount {
				t.Errorf("TurnCount: got %d, want %d", got.TurnCount, tc.state.TurnCount)
			}
			if got.TokenCount != tc.state.TokenCount {
				t.Errorf("TokenCount: got %d, want %d", got.TokenCount, tc.state.TokenCount)
			}
		})
	}
}

func TestSmartContextManagerPostIngestion(t *testing.T) {
	m := &SmartContextManager{}
	state := RunState{TurnCount: 2}
	got, err := m.PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TurnCount != state.TurnCount {
		t.Errorf("TurnCount: got %d, want %d", got.TurnCount, state.TurnCount)
	}
}

func TestSmartContextManagerPreAssembly(t *testing.T) {
	m := &SmartContextManager{}
	state := RunState{TurnCount: 4}
	got, err := m.PreAssembly(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TurnCount != state.TurnCount {
		t.Errorf("TurnCount: got %d, want %d", got.TurnCount, state.TurnCount)
	}
}

func TestNewContextManager(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		wantType string
	}{
		{"naive mode", "naive", "*agent.NaiveContextManager"},
		{"smart mode", "smart", "*agent.SmartContextManager"},
		{"empty falls back to naive", "", "*agent.NaiveContextManager"},
		{"unknown falls back to naive", "unknown", "*agent.NaiveContextManager"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewContextManager(tc.mode)
			if m == nil {
				t.Fatal("NewContextManager returned nil")
			}
			switch tc.wantType {
			case "*agent.NaiveContextManager":
				if _, ok := m.(*NaiveContextManager); !ok {
					t.Errorf("got %T, want *NaiveContextManager", m)
				}
			case "*agent.SmartContextManager":
				if _, ok := m.(*SmartContextManager); !ok {
					t.Errorf("got %T, want *SmartContextManager", m)
				}
			}
		})
	}
}
