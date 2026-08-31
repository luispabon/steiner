package delegation

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestFailedDelegateExecutionContextStatusMapping(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		state     agent.RunState
		status    Status
		reason    string
		resumable bool
	}{
		{name: "cancelled without activity", err: context.Canceled, status: StatusCancelled, resumable: true},
		{name: "cancelled with output", err: context.Canceled, state: agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, Content: "work"}}}, status: StatusPartial, reason: "cancelled", resumable: true},
		{name: "deadline without activity", err: context.DeadlineExceeded, status: StatusCancelled, reason: "limit reached", resumable: true},
		{name: "deadline with tool activity", err: context.DeadlineExceeded, state: agent.RunState{Conversation: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{Name: "read"}}}}}, status: StatusPartial, reason: "limit reached", resumable: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := failedDelegateExecution(Spec{AgentID: "agent-test"}, tt.state, TokenUsage{}, tt.err, newTraceCollector("agent-test", "task"), nil).Value.(Result)
			if result.Status != tt.status || result.StopReason != tt.reason || result.SessionResumable != tt.resumable {
				t.Fatalf("result = %#v, want status=%q reason=%q resumable=%v", result, tt.status, tt.reason, tt.resumable)
			}
			envelope := result.ProjectToolResult()
			if envelope.Status != string(tt.status) || envelope.Reason != tt.reason {
				t.Fatalf("envelope = %#v, want status=%q reason=%q", envelope, tt.status, tt.reason)
			}
		})
	}
}
