package delegation

import (
	"time"

	"github.com/luispabon/steiner/internal/provider"
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

	// SystemSuffix is appended after the shared system preamble; it does not replace it.
	SystemSuffix string `json:"system_suffix,omitempty"`

	// Images are optional image blocks to include in the first child message.
	Images []provider.ImageBlock `json:"images,omitempty"`

	// Limits define resource constraints for the child execution.
	Limits DelegationLimits `json:"limits"`

	// AgentID is a unique identifier for this delegation.
	AgentID string `json:"agent_id"`

	// PriorCacheUsage carries the child agent's cumulative prompt-cache usage
	// from runs before this spawn (extensions and prior follow-ups). The
	// follow_up handler seeds it from the stored ChildSession so reported
	// cache figures describe the agent's whole life rather than a single run.
	// Zero for a fresh spawn.
	PriorCacheUsage CacheUsage `json:"-"`
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

	// InputTokens is the total uncached prompt tokens used by the child.
	InputTokens int `json:"input_tokens,omitempty"`

	// CacheReadTokens is the total cache-read tokens used by the child.
	CacheReadTokens int `json:"cache_read_tokens,omitempty"`

	// CacheCreateTokens is the total cache-create tokens used by the child.
	CacheCreateTokens int `json:"cache_create_tokens,omitempty"`

	// StopReason carries the raw stop reason string when Status is StatusPartial.
	// It distinguishes budget exhaustion cause (e.g. "max_turns", "max_tokens").
	StopReason string `json:"stop_reason,omitempty"`

	// ToolCallCount is the number of tool calls the child executed across all turns.
	ToolCallCount int `json:"tool_call_count"`

	// FollowUpCount is the number of successful follow-up turns resumed for this child.
	FollowUpCount int `json:"follow_up_count,omitempty"`

	// SessionResumable indicates the child session is still in the session store
	// and can be resumed with follow_up using the same AgentID. True for follow-up
	// results after the session was successfully saved, and for delegate results
	// when the parent should expect the session to remain warm. False when the
	// session was not preserved (e.g. the initial delegate failed before the
	// session could be saved, or the follow-up call returned no such session).
	//
	// Set by BuildResult for any StopReasonCancelled mapping, and by
	// failedDelegateExecution for StatusCancelled outcomes. Both paths assume
	// SpawnDelegate's session-save contract: the caller persists the session on
	// the err==nil return path. Direct callers of BuildResult that bypass
	// SpawnDelegate must not rely on SessionResumable being accurate.
	SessionResumable bool `json:"session_resumable,omitempty"`

	// Trace captures lifecycle events for delegation diagnostics.
	Trace []TraceEntry `json:"trace,omitempty"`

	// WorktreePath is the absolute path to the code agent's isolated git
	// worktree. It is empty only when no worktree was provisioned, in which
	// case a code-agent delegation fails. Only set for AgentTypeCode.
	WorktreePath string `json:"worktree_path,omitempty"`

	// WorktreeBranch is the branch name of the code agent's isolated
	// worktree. Only set for AgentTypeCode when provisioning succeeded.
	WorktreeBranch string `json:"worktree_branch,omitempty"`

	// Warnings holds human-readable code-agent warnings surfaced to the
	// orchestrator, such as uncommitted parent-tree changes not visible to
	// the isolated worktree or a dirty-worktree warning after failed commit
	// remediation. Only populated for AgentTypeCode.
	Warnings []string `json:"warnings,omitempty"`
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
