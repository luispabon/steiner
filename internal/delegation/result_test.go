package delegation

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

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
		name           string
		agentID        string
		state          agent.RunState
		wantStatus     DelegationStatus
		wantTurnCount  int
		wantTokenCount int
		wantOutput     string
		wantStopReason string
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
			name:           "maps cancelled stop reason",
			agentID:        "agent-3",
			state:          makeRunState(1, 100, agent.StopReasonCancelled, ""),
			wantStatus:     StatusCancelled,
			wantTurnCount:  1,
			wantTokenCount: 100,
			wantOutput:     "",
			wantStopReason: "",
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
			spec := DelegationSpec{AgentID: tt.agentID}
			got := BuildResult(tt.agentID, tt.state, spec)

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
		})
	}
}
