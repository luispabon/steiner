package agent

import (
	"context"
	"encoding/json"
	"strings"
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

func TestPostIngestionNaiveContextManagerKeepsToolOutput(t *testing.T) {
	state := RunState{
		TurnCount: 2,
		Conversation: []Message{
			{
				Role: MessageRoleTool,
				Name: "bash",
				Content: mustJSON(t, map[string]any{
					"exit_code": 1,
					"output":    "warning: keep\nwarning: keep\nfinal",
				}),
			},
		},
	}

	got, err := (&NaiveContextManager{}).PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Conversation[0].Content != state.Conversation[0].Content {
		t.Fatalf("Conversation[0].Content = %q, want unchanged", got.Conversation[0].Content)
	}
	if got.TurnCount != state.TurnCount {
		t.Fatalf("TurnCount = %d, want %d", got.TurnCount, state.TurnCount)
	}
}

func TestPostIngestionSmartContextManagerTransformsToolOutput(t *testing.T) {
	bashContent := mustJSON(t, bashOutputForIngestionTest())
	grepContent := mustJSON(t, grepOutputForIngestionTest())
	readContent := mustJSON(t, map[string]any{
		"path":        "README.md",
		"start_line":  1,
		"end_line":    3,
		"total_lines": 3,
		"output":      "alpha\n\nbeta\n",
	})

	state := RunState{
		TurnCount: 4,
		Conversation: []Message{
			{Role: MessageRoleTool, Name: "bash", Content: bashContent},
			{Role: MessageRoleTool, Name: "grep", Content: grepContent},
			{Role: MessageRoleTool, Name: "read", Content: readContent},
		},
	}
	state.Lineage = newConversationLineage(state.Conversation)

	got, err := (&SmartContextManager{}).PostIngestion(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TurnCount != state.TurnCount {
		t.Fatalf("TurnCount = %d, want %d", got.TurnCount, state.TurnCount)
	}
	if len(got.Conversation) != len(state.Conversation) {
		t.Fatalf("Conversation len = %d, want %d", len(got.Conversation), len(state.Conversation))
	}

	var bashResult struct {
		Output    string `json:"output"`
		Message   string `json:"message"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(got.Conversation[0].Content), &bashResult); err != nil {
		t.Fatalf("unmarshal bash result: %v", err)
	}
	if !bashResult.Truncated {
		t.Fatal("bash truncated = false, want true")
	}
	if !strings.Contains(bashResult.Output, "final tail") {
		t.Fatalf("bash output = %q, want tail content", bashResult.Output)
	}
	if strings.Contains(bashResult.Output, "\x1b[") {
		t.Fatalf("bash output = %q, want ANSI stripped", bashResult.Output)
	}
	if !strings.Contains(bashResult.Output, "warning: retry (repeated 3x)") {
		t.Fatalf("bash output = %q, want repeated warning collapse", bashResult.Output)
	}
	if !strings.Contains(bashResult.Message, "<truncated output shown=") {
		t.Fatalf("bash message = %q, want truncation marker", bashResult.Message)
	}

	var grepResult struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(got.Conversation[1].Content), &grepResult); err != nil {
		t.Fatalf("unmarshal grep result: %v", err)
	}
	if !strings.Contains(grepResult.Output, "info: retrying (repeated 200x)") {
		t.Fatalf("grep output = %q, want collapsed info lines", grepResult.Output)
	}
	if !strings.Contains(grepResult.Output, "<truncated output shown=") {
		t.Fatalf("grep output = %q, want truncation marker", grepResult.Output)
	}

	if got.Conversation[2].Content != state.Conversation[2].Content {
		t.Fatalf("read content = %q, want unchanged", got.Conversation[2].Content)
	}
	if len(got.Lineage.FullMessages()) != len(got.Conversation) {
		t.Fatalf("lineage len = %d, want %d", len(got.Lineage.FullMessages()), len(got.Conversation))
	}
	if got.Lineage.FullMessages()[0].Content != got.Conversation[0].Content {
		t.Fatal("lineage conversation diverged from active conversation")
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

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(data)
}

func bashOutputForIngestionTest() map[string]any {
	return map[string]any{
		"exit_code": 1,
		"output":    strings.Repeat("filler line\n", 900) + "\x1b[31mwarning: retry\x1b[0m\nwarning: retry\nwarning: retry\nfinal tail\n",
	}
}

func grepOutputForIngestionTest() map[string]any {
	lines := make([]string, 0, 240)
	for i := 0; i < 205; i++ {
		lines = append(lines, "info: retrying")
	}
	for i := 0; i < 35; i++ {
		lines = append(lines, "match line")
	}
	return map[string]any{
		"matches":  240,
		"returned": 240,
		"output":   strings.Join(lines, "\n"),
	}
}
