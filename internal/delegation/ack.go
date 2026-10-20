package delegation

import (
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

const (
	ackStatusRunning = "running"
	ackStatusQueued  = "queued"

	ackOutput = "Results arrive automatically in a separate message. Do not poll or use sleep to wait for this agent, duplicate its work, or report results before receiving them. Continue any independent work; otherwise end your turn. The harness resumes you when results are delivered."
)

// AckResult is the immediate tool result of an async sub-agent spawn.
type AckResult struct {
	AgentID string `json:"agent_id"`
	// Queued is true when the child is waiting for a running slot.
	Queued bool `json:"queued,omitempty"`
}

func (a AckResult) status() string {
	if a.Queued {
		return ackStatusQueued
	}
	return ackStatusRunning
}

// ProjectToolResult returns the compact provider-facing acknowledgement.
func (a AckResult) ProjectToolResult() agent.DelegationResultEnvelope {
	return agent.DelegationResultEnvelope{
		Output:       ackOutput,
		Status:       a.status(),
		Continuation: &agent.DelegationContinuation{AgentID: a.AgentID},
	}
}

func ackExecutionResult(ticket SpawnTicket) tool.ExecutionResult {
	ack := AckResult(ticket)
	return tool.ExecutionResult{
		Value: ack,
		Retention: &tool.ToolRetention{
			Kind:    tool.RetentionKindDelegateSummary,
			AgentID: ack.AgentID,
			Status:  ack.status(),
		},
	}
}
