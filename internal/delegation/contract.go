package delegation

import (
	"time"
)

// DelegationStatus represents the lifecycle state of a delegation.
//
//nolint:revive // package API keeps delegation-prefixed names for compatibility.
type DelegationStatus string

const (
	// StatusPending indicates the delegation has been created but not started.
	StatusPending DelegationStatus = "pending"
	// StatusRunning indicates the child agent is actively executing.
	StatusRunning DelegationStatus = "running"
	// StatusComplete indicates the child agent finished successfully.
	StatusComplete DelegationStatus = "complete"
	// StatusPartial indicates the child stopped due to a resource budget (turn or
	// token limit) rather than completing its task. The result may be incomplete.
	StatusPartial DelegationStatus = "partial"
	// StatusFailed indicates the child agent terminated with an error.
	StatusFailed DelegationStatus = "failed"
	// StatusCancelled indicates the delegation was cancelled before completion.
	StatusCancelled DelegationStatus = "cancelled"
)

// DelegationSpec defines what the parent sends to the child agent.
//
//nolint:revive // package API keeps delegation-prefixed names for compatibility.
type DelegationSpec struct {
	// Task is the required task description.
	Task string `json:"task"`

	// Context is optional additional context for the child.
	Context string `json:"context,omitempty"`

	// SystemPrompt is an optional override of the system prompt.
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Limits define resource constraints for the child execution.
	Limits DelegationLimits `json:"limits"`

	// AgentID is a unique identifier for this delegation.
	AgentID string `json:"agent_id"`
}

// GetAgentID returns the AgentID from this DelegationSpec.
// This implements the agent.DelegationSpec interface to avoid circular imports.
func (s DelegationSpec) GetAgentID() string {
	return s.AgentID
}

// DelegationResult defines what the child returns to the parent.
//
//nolint:revive // package API keeps delegation-prefixed names for compatibility.
type DelegationResult struct {
	// AgentID matches the request.
	AgentID string `json:"agent_id"`

	// Status indicates the final state of the child.
	Status DelegationStatus `json:"status"`

	// Output is the child's final answer or result.
	Output string `json:"output"`

	// Summary holds the retained delegate summary. When the delegate's Output is
	// an intermediate fragment (e.g. from a tool-calling turn), this provides a
	// useful condensed view of the delegate's findings.
	Summary string `json:"summary,omitempty"`

	// TurnCount is the number of turns the child executed.
	TurnCount int `json:"turn_count"`

	// TokenCount is the total tokens used by the child.
	TokenCount int `json:"token_count"`

	// StopReason carries the raw stop reason string when Status is StatusPartial.
	// It distinguishes budget exhaustion cause (e.g. "max_turns", "max_tokens").
	StopReason string `json:"stop_reason,omitempty"`

	// Error is populated if the delegation failed.
	Error string `json:"error,omitempty"`

	// ToolCallCount is the number of tool calls the child executed across all turns.
	ToolCallCount int `json:"tool_call_count"`

	// FollowUpCount is the number of successful follow-up turns resumed for this child.
	FollowUpCount int `json:"follow_up_count,omitempty"`

	// Trace captures lifecycle events for delegation diagnostics.
	Trace []TraceEntry `json:"trace,omitempty"`
}

// DelegationLimits defines resource constraints for a child execution.
//
//nolint:revive // package API keeps delegation-prefixed names for compatibility.
type DelegationLimits struct {
	// MaxTurns limits the number of agent turns.
	MaxTurns int `json:"max_turns"`

	// OutputLimitTokens limits the size of the output.
	OutputLimitTokens int `json:"output_limit_tokens"`

	// Timeout is the maximum time allowed for execution.
	Timeout time.Duration `json:"timeout"`
}
