package delegation

import (
	"time"
)

// DelegationStatus represents the lifecycle state of a delegation.
type DelegationStatus string

const (
	StatusPending   DelegationStatus = "pending"
	StatusRunning   DelegationStatus = "running"
	StatusComplete  DelegationStatus = "complete"
	StatusFailed    DelegationStatus = "failed"
	StatusCancelled DelegationStatus = "cancelled"
)

// DelegationSpec defines what the parent sends to the child agent.
type DelegationSpec struct {
	// Task is the required task description.
	Task string `json:"task"`

	// Context is optional additional context for the child.
	Context string `json:"context,omitempty"`

	// SystemPrompt is an optional override of the system prompt.
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Model is an optional model override.
	Model string `json:"model,omitempty"`

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
type DelegationResult struct {
	// AgentID matches the request.
	AgentID string `json:"agent_id"`

	// Status indicates the final state of the child.
	Status DelegationStatus `json:"status"`

	// Output is the child's final answer or result.
	Output string `json:"output"`

	// Summary is a compact summary if output was oversized.
	Summary string `json:"summary,omitempty"`

	// TurnCount is the number of turns the child executed.
	TurnCount int `json:"turn_count"`

	// TokenCount is the total tokens used by the child.
	TokenCount int `json:"token_count"`

	// Error is populated if the delegation failed.
	Error string `json:"error,omitempty"`
}

// DelegationLimits defines resource constraints for a child execution.
type DelegationLimits struct {
	// MaxTurns limits the number of agent turns.
	MaxTurns int `json:"max_turns"`

	// OutputLimitTokens limits the size of the output.
	OutputLimitTokens int `json:"output_limit_tokens"`

	// Timeout is the maximum time allowed for execution.
	Timeout time.Duration `json:"timeout"`
}
