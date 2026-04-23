package delegation

import (
	"testing"
)

// mockRunState implements the RunState interface for testing
type mockRunState struct {
	turnCount        int
	tokenCount       int
	stopReason       string
	lastAssistantMsg string
}

func (m mockRunState) GetTurnCount() int               { return m.turnCount }
func (m mockRunState) GetTokenCount() int              { return m.tokenCount }
func (m mockRunState) GetStopReason() string           { return m.stopReason }
func (m mockRunState) GetLastAssistantMessage() string { return m.lastAssistantMsg }

func TestBuildResult(t *testing.T) {
	tests := []struct {
		name           string
		agentID        string
		state          mockRunState
		wantStatus     DelegationStatus
		wantTurnCount  int
		wantTokenCount int
		wantOutput     string
	}{
		{
			name:    "maps complete stop reason",
			agentID: "agent-1",
			state: mockRunState{
				turnCount:        5,
				tokenCount:       1000,
				stopReason:       "complete",
				lastAssistantMsg: "test output",
			},
			wantStatus:     StatusComplete,
			wantTurnCount:  5,
			wantTokenCount: 1000,
			wantOutput:     "test output",
		},
		{
			name:    "maps error stop reason",
			agentID: "agent-2",
			state: mockRunState{
				turnCount:        2,
				tokenCount:       500,
				stopReason:       "error",
				lastAssistantMsg: "",
			},
			wantStatus:     StatusFailed,
			wantTurnCount:  2,
			wantTokenCount: 500,
			wantOutput:     "",
		},
		{
			name:    "maps cancelled stop reason",
			agentID: "agent-3",
			state: mockRunState{
				turnCount:        1,
				tokenCount:       100,
				stopReason:       "cancelled",
				lastAssistantMsg: "",
			},
			wantStatus:     StatusCancelled,
			wantTurnCount:  1,
			wantTokenCount: 100,
			wantOutput:     "",
		},
		{
			name:    "unknown stop reason defaults to complete",
			agentID: "agent-4",
			state: mockRunState{
				turnCount:        10,
				tokenCount:       5000,
				stopReason:       "max_turns",
				lastAssistantMsg: "result",
			},
			wantStatus:     StatusComplete,
			wantTurnCount:  10,
			wantTokenCount: 5000,
			wantOutput:     "result",
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
		})
	}
}
