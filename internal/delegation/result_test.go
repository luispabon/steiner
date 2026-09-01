package delegation

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func makeRunStateWithToolCall(turnCount, tokenCount int, stopReason agent.StopReason, toolName string, toolArgs map[string]any) agent.RunState {
	return agent.RunState{
		TurnCount:  turnCount,
		TokenCount: tokenCount,
		StopReason: stopReason,
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "c0", Name: toolName, Arguments: toolArgs}}},
		},
	}
}

func makeRunState(turnCount, tokenCount int, stopReason agent.StopReason, lastAssistantMsg string) agent.RunState {
	state := agent.RunState{
		TurnCount:  turnCount,
		TokenCount: tokenCount,
		StopReason: stopReason,
	}
	if lastAssistantMsg != "" {
		state.Conversation = []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: lastAssistantMsg},
		}
	}
	return state
}

func TestBuildResult(t *testing.T) {
	tests := []struct {
		name                 string
		agentID              string
		state                agent.RunState
		wantStatus           Status
		wantTurnCount        int
		wantTokenCount       int
		wantOutput           string
		wantStopReason       string
		wantSessionResumable bool
	}{
		{
			name:           "maps complete stop reason",
			agentID:        "agent-1",
			state:          makeRunState(5, 1000, agent.StopReasonComplete, "test output"),
			wantStatus:     StatusComplete,
			wantTurnCount:  5,
			wantTokenCount: 1000,
			wantOutput:     "test output",
			wantStopReason: "",
		},
		{
			name:           "maps error stop reason",
			agentID:        "agent-2",
			state:          makeRunState(2, 500, agent.StopReasonError, ""),
			wantStatus:     StatusFailed,
			wantTurnCount:  2,
			wantTokenCount: 500,
			wantOutput:     "",
			wantStopReason: "",
		},
		{
			name:                 "maps cancelled stop reason",
			agentID:              "agent-3",
			state:                makeRunState(1, 100, agent.StopReasonCancelled, ""),
			wantStatus:           StatusCancelled,
			wantTurnCount:        1,
			wantTokenCount:       100,
			wantOutput:           "",
			wantStopReason:       "",
			wantSessionResumable: true,
		},
		{
			name:                 "cancelled with output maps to partial",
			agentID:              "agent-3b",
			state:                makeRunState(5, 2000, agent.StopReasonCancelled, "useful work"),
			wantStatus:           StatusPartial,
			wantTurnCount:        5,
			wantTokenCount:       2000,
			wantOutput:           "useful work",
			wantStopReason:       "cancelled",
			wantSessionResumable: true,
		},
		{
			name:                 "cancelled with tool calls but no text maps to partial",
			agentID:              "agent-3d",
			state:                makeRunStateWithToolCall(3, 1500, agent.StopReasonCancelled, "glob", map[string]any{"pattern": "**/*_test.go"}),
			wantStatus:           StatusPartial,
			wantTurnCount:        3,
			wantTokenCount:       1500,
			wantOutput:           "",
			wantStopReason:       "cancelled",
			wantSessionResumable: true,
		},
		{
			name:                 "cancelled zero turns maps to cancelled",
			agentID:              "agent-3c",
			state:                makeRunState(0, 0, agent.StopReasonCancelled, ""),
			wantStatus:           StatusCancelled,
			wantTurnCount:        0,
			wantTokenCount:       0,
			wantOutput:           "",
			wantStopReason:       "",
			wantSessionResumable: true,
		},
		{
			name:           "max_turns stop reason maps to partial",
			agentID:        "agent-4",
			state:          makeRunState(10, 5000, agent.StopReasonMaxTurns, "result"),
			wantStatus:     StatusPartial,
			wantTurnCount:  10,
			wantTokenCount: 5000,
			wantOutput:     "result",
			wantStopReason: "max_turns",
		},
		{
			name:           "max_tokens stop reason maps to partial",
			agentID:        "agent-5",
			state:          makeRunState(8, 100000, agent.StopReasonMaxTokens, "partial result"),
			wantStatus:     StatusPartial,
			wantTurnCount:  8,
			wantTokenCount: 100000,
			wantOutput:     "partial result",
			wantStopReason: "max_tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildResult(tt.agentID, tt.state)

			if got.AgentID != tt.agentID {
				t.Errorf("AgentID=%q, want %q", got.AgentID, tt.agentID)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("Status=%q, want %q", got.Status, tt.wantStatus)
			}
			if got.TurnCount != tt.wantTurnCount {
				t.Errorf("TurnCount=%d, want %d", got.TurnCount, tt.wantTurnCount)
			}
			if got.TokenCount != tt.wantTokenCount {
				t.Errorf("TokenCount=%d, want %d", got.TokenCount, tt.wantTokenCount)
			}
			if got.Output != tt.wantOutput {
				t.Errorf("Output=%q, want %q", got.Output, tt.wantOutput)
			}
			if got.StopReason != tt.wantStopReason {
				t.Errorf("StopReason=%q, want %q", got.StopReason, tt.wantStopReason)
			}
			if got.SessionResumable != tt.wantSessionResumable {
				t.Errorf("SessionResumable=%v, want %v", got.SessionResumable, tt.wantSessionResumable)
			}
		})
	}
}

func TestBuildResultCarriesCacheTokens(t *testing.T) {
	state := agent.RunState{
		TurnCount:         3,
		TokenCount:        1000,
		StopReason:        agent.StopReasonComplete,
		InputTokens:       200,
		CacheReadTokens:   750,
		CacheCreateTokens: 50,
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: "done"},
		},
	}

	got := BuildResult("agent-cache", state)

	if got.InputTokens != 200 {
		t.Errorf("InputTokens=%d, want 200", got.InputTokens)
	}
	if got.CacheReadTokens != 750 {
		t.Errorf("CacheReadTokens=%d, want 750", got.CacheReadTokens)
	}
	if got.CacheCreateTokens != 50 {
		t.Errorf("CacheCreateTokens=%d, want 50", got.CacheCreateTokens)
	}
}

func TestCountAdvisorUsage(t *testing.T) {
	tests := []struct {
		name       string
		conv       []agent.Message
		wantUses   int
		wantDenied int
	}{
		{
			name:       "no advisor calls",
			conv:       []agent.Message{},
			wantUses:   0,
			wantDenied: 0,
		},
		{
			name: "one successful advisor call",
			conv: []agent.Message{
				{
					Role: agent.MessageRoleAssistant,
					ToolCalls: []agent.ToolCall{
						{ID: "c1", Name: "advisor", Arguments: map[string]any{"question": "test"}},
					},
				},
				{
					Role: agent.MessageRoleTool,
					Name: "advisor",
					Content: "advice",
				},
			},
			wantUses:   1,
			wantDenied: 0,
		},
		{
			name: "one denied advisor call",
			conv: []agent.Message{
				{
					Role: agent.MessageRoleAssistant,
					ToolCalls: []agent.ToolCall{
						{ID: "c1", Name: "advisor", Arguments: map[string]any{"question": "test"}},
					},
				},
				{
					Role: agent.MessageRoleTool,
					Name: "advisor",
					Content: "advisor budget exhausted for this session (1/1); proceed on your own judgment",
				},
			},
			wantUses:   0,
			wantDenied: 1,
		},
		{
			name: "three calls, two denied (one successful, two denied)",
			conv: []agent.Message{
				{
					Role: agent.MessageRoleAssistant,
					ToolCalls: []agent.ToolCall{
						{ID: "c1", Name: "advisor", Arguments: map[string]any{"question": "q1"}},
					},
				},
				{
					Role: agent.MessageRoleTool,
					Name: "advisor",
					Content: "good advice",
				},
				{
					Role: agent.MessageRoleAssistant,
					ToolCalls: []agent.ToolCall{
						{ID: "c2", Name: "advisor", Arguments: map[string]any{"question": "q2"}},
					},
				},
				{
					Role: agent.MessageRoleTool,
					Name: "advisor",
					Content: "advisor budget exhausted for this session (1/1); proceed on your own judgment",
				},
				{
					Role: agent.MessageRoleAssistant,
					ToolCalls: []agent.ToolCall{
						{ID: "c3", Name: "advisor", Arguments: map[string]any{"question": "q3"}},
					},
				},
				{
					Role: agent.MessageRoleTool,
					Name: "advisor",
					Content: "advisor budget exhausted for this session (1/1); proceed on your own judgment",
				},
			},
			wantUses:   1,
			wantDenied: 2,
		},
		{
			name: "advisor call with other tools",
			conv: []agent.Message{
				{
					Role: agent.MessageRoleAssistant,
					ToolCalls: []agent.ToolCall{
						{ID: "c1", Name: "read", Arguments: map[string]any{"path": "file.go"}},
						{ID: "c2", Name: "advisor", Arguments: map[string]any{"question": "test"}},
					},
				},
				{
					Role: agent.MessageRoleTool,
					Name: "read",
					Content: "file content",
				},
				{
					Role: agent.MessageRoleTool,
					Name: "advisor",
					Content: "advice",
				},
			},
			wantUses:   1,
			wantDenied: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uses, denied := countAdvisorUsage(tt.conv)
			if uses != tt.wantUses {
				t.Errorf("uses=%d, want %d", uses, tt.wantUses)
			}
			if denied != tt.wantDenied {
				t.Errorf("denied=%d, want %d", denied, tt.wantDenied)
			}
		})
	}
}
