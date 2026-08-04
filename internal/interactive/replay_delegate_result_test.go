package interactive

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestDecodeReplayedDelegateResult(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		wantOK  bool
		want    replayedDelegateResult
	}{
		{
			name:    "empty input",
			content: "",
			wantOK:  false,
		},
		{
			name:    "whitespace only input",
			content: "   ",
			wantOK:  false,
		},
		{
			name:    "malformed json",
			content: "{not json",
			wantOK:  false,
		},
		{
			name:    "valid full payload",
			content: `{"agent_id":"agent-42","status":"complete","output":"done","summary":"a summary","turn_count":3,"token_count":100,"tool_call_count":2,"error":""}`,
			wantOK:  true,
			want: replayedDelegateResult{
				AgentID:       "agent-42",
				Status:        "complete",
				Output:        "done",
				Summary:       "a summary",
				TurnCount:     3,
				TokenCount:    100,
				ToolCallCount: 2,
			},
		},
		{
			name:    "partial payload with missing optional fields",
			content: `{"agent_id":"agent-7","output":"partial output"}`,
			wantOK:  true,
			want: replayedDelegateResult{
				AgentID: "agent-7",
				Output:  "partial output",
			},
		},
		{
			name:    "failed status payload",
			content: `{"agent_id":"agent-9","status":"failed","error":"boom"}`,
			wantOK:  true,
			want: replayedDelegateResult{
				AgentID: "agent-9",
				Status:  "failed",
				Error:   "boom",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := decodeReplayedDelegateResult(tt.content)
			if ok != tt.wantOK {
				t.Fatalf("decodeReplayedDelegateResult(%q) ok = %v, want %v", tt.content, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got != tt.want {
				t.Errorf("decodeReplayedDelegateResult(%q) = %+v, want %+v", tt.content, got, tt.want)
			}
		})
	}
}

func TestApplyDecodedDelegationState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		initial replayedDelegationState
		decoded replayedDelegateResult
		want    replayedDelegationState
	}{
		{
			name: "full payload overrides all fields",
			initial: replayedDelegationState{
				agentID: "agent-fallback",
				status:  "complete",
				output:  "fallback output",
				error:   "fallback output",
			},
			decoded: replayedDelegateResult{
				AgentID:       "agent-decoded",
				Status:        "failed",
				Output:        "decoded output",
				Error:         "decoded error",
				TurnCount:     5,
				TokenCount:    200,
				ToolCallCount: 4,
			},
			want: replayedDelegationState{
				agentID:       "agent-decoded",
				status:        "failed",
				output:        "decoded output",
				error:         "decoded error",
				turnCount:     5,
				tokenCount:    200,
				toolCallCount: 4,
			},
		},
		{
			name: "empty optional fields keep initial values except output and error",
			initial: replayedDelegationState{
				agentID:       "agent-fallback",
				status:        "complete",
				output:        "fallback output",
				error:         "fallback output",
				turnCount:     1,
				tokenCount:    10,
				toolCallCount: 1,
			},
			decoded: replayedDelegateResult{
				Output: "decoded output only",
			},
			want: replayedDelegationState{
				agentID:       "agent-fallback",
				status:        "complete",
				output:        "decoded output only",
				error:         "fallback output",
				turnCount:     1,
				tokenCount:    10,
				toolCallCount: 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.initial
			applyDecodedDelegationState(&state, tt.decoded)
			if state != tt.want {
				t.Errorf("applyDecodedDelegationState() = %+v, want %+v", state, tt.want)
			}
		})
	}
}

func TestBuildReplayedDelegationState(t *testing.T) {
	t.Parallel()

	t.Run("valid payload with no retention", func(t *testing.T) {
		t.Parallel()
		content := `{"agent_id":"agent-1","status":"complete","output":"result text","turn_count":2,"token_count":50,"tool_call_count":1}`
		state := buildReplayedDelegationState("call-1", nil, content)

		want := replayedDelegationState{
			agentID:       "agent-1",
			status:        "complete",
			output:        "result text",
			error:         content,
			turnCount:     2,
			tokenCount:    50,
			toolCallCount: 1,
		}
		if state != want {
			t.Errorf("buildReplayedDelegationState() = %+v, want %+v", state, want)
		}
	})

	t.Run("failed status surfaces decoded error", func(t *testing.T) {
		t.Parallel()
		content := `{"agent_id":"agent-2","status":"failed","error":"delegate crashed"}`
		state := buildReplayedDelegationState("call-2", nil, content)

		if state.status != "failed" {
			t.Fatalf("status = %q, want failed", state.status)
		}
		if state.error != "delegate crashed" {
			t.Errorf("error = %q, want %q", state.error, "delegate crashed")
		}
	})

	t.Run("undecodable content falls back to defaults", func(t *testing.T) {
		t.Parallel()
		content := "plain text result"
		state := buildReplayedDelegationState("call-3", nil, content)

		want := replayedDelegationState{
			agentID: "agent-call-3",
			status:  "complete",
			output:  content,
			error:   content,
		}
		if state != want {
			t.Errorf("buildReplayedDelegationState() = %+v, want %+v", state, want)
		}
	})
}

func TestApplyRetainedDelegationState(t *testing.T) {
	t.Parallel()

	t.Run("nil retention leaves state unchanged", func(t *testing.T) {
		t.Parallel()
		state := replayedDelegationState{
			agentID:    "agent-decoded",
			status:     "complete",
			turnCount:  3,
			tokenCount: 100,
		}
		want := state
		applyRetainedDelegationState(&state, nil)
		if state != want {
			t.Errorf("applyRetainedDelegationState(nil) = %+v, want %+v", state, want)
		}
	})

	t.Run("retention overrides decoded values when both are set", func(t *testing.T) {
		t.Parallel()
		state := replayedDelegationState{
			agentID:    "agent-decoded",
			status:     "complete",
			turnCount:  3,
			tokenCount: 100,
		}
		retention := &agent.MessageRetention{
			AgentID:    "agent-retained",
			Status:     "failed",
			TurnCount:  9,
			TokenCount: 900,
		}
		applyRetainedDelegationState(&state, retention)

		if state.agentID != "agent-retained" {
			t.Errorf("agentID = %q, want retention value %q (retention wins over decoded)", state.agentID, "agent-retained")
		}
		if state.status != "failed" {
			t.Errorf("status = %q, want retention value %q (retention wins over decoded)", state.status, "failed")
		}
		if state.turnCount != 9 {
			t.Errorf("turnCount = %d, want retention value %d (retention wins over decoded)", state.turnCount, 9)
		}
		if state.tokenCount != 900 {
			t.Errorf("tokenCount = %d, want retention value %d (retention wins over decoded)", state.tokenCount, 900)
		}
	})

	t.Run("empty or zero retention fields fall back to decoded values", func(t *testing.T) {
		t.Parallel()
		state := replayedDelegationState{
			agentID:    "agent-decoded",
			status:     "complete",
			turnCount:  3,
			tokenCount: 100,
		}
		want := state
		retention := &agent.MessageRetention{}
		applyRetainedDelegationState(&state, retention)

		if state != want {
			t.Errorf("applyRetainedDelegationState(zero retention) = %+v, want unchanged %+v", state, want)
		}
	})
}
