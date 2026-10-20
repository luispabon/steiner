package output

import (
	"strings"
	"time"
)

// DelegationOccurrence identifies one delegation occurrence: one sub_agent or
// follow_up call within one tool batch. Provider call IDs can repeat across
// batches and follow_up reuses agent IDs, so (BatchID, CallID) is the identity
// and AgentID names the child. Lifecycle events embed it so their JSON stays
// flat.
type DelegationOccurrence struct {
	CallID  string `json:"call_id,omitempty"`
	BatchID string `json:"batch_id,omitempty"`
	AgentID string `json:"agent_id"`
}

// DelegationAcceptedEvent records an admitted delegation call.
type DelegationAcceptedEvent struct {
	DelegationOccurrence
	Group string `json:"group"`
}

// DelegationStartedEvent records the start of a delegated child task.
type DelegationStartedEvent struct {
	DelegationOccurrence
	TaskPreview string `json:"task_preview"`
	ModelAlias  string `json:"model_alias,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
}

// DelegationQueuedEvent records a delegated child task waiting for a running slot.
type DelegationQueuedEvent struct {
	DelegationOccurrence
	AgentType   string `json:"agent_type,omitempty"`
	TaskPreview string `json:"task_preview"`
}

// DeliveredSubAgent is one sub-agent result inside a SubAgentsDeliveredEvent.
type DeliveredSubAgent struct {
	AgentID      string `json:"agent_id"`
	AgentType    string `json:"agent_type,omitempty"`
	Status       string `json:"status"`
	ParentCallID string `json:"parent_call_id,omitempty"`
	BatchID      string `json:"batch_id,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
}

// SubAgentsDeliveredEvent records sub-agent results delivered to the model, in
// delivery order.
type SubAgentsDeliveredEvent struct {
	Items []DeliveredSubAgent `json:"items"`
}

// SubAgentResultsUnansweredEvent records sub-agent results in the conversation
// tail that no assistant message followed. Reason is the run error, if any.
type SubAgentResultsUnansweredEvent struct {
	AgentIDs []string `json:"agent_ids"`
	Reason   string   `json:"reason,omitempty"`
}

// DelegationCacheWaitingEvent records a sub-agent delegation waiting behind a
// shared prompt-cache dispatch slot for the leader's first streamed token.
type DelegationCacheWaitingEvent struct {
	DelegationOccurrence
	DeadlineUnixNano int64 `json:"deadline_unix_nano"`
}

// DelegationCompleteEvent records a successful delegated child task.
type DelegationCompleteEvent struct {
	DelegationOccurrence
	AgentType         string `json:"agent_type,omitempty"`
	DurationMs        int64  `json:"duration_ms,omitempty"`
	Status            string `json:"status"`
	TurnCount         int    `json:"turn_count"`
	TokenCount        int    `json:"token_count"`
	ToolCallCount     int    `json:"tool_call_count"`
	Output            string `json:"output,omitempty"`
	InputTokens       int    `json:"input_tokens,omitempty"`
	CacheReadTokens   int    `json:"cache_read_tokens,omitempty"`
	CacheCreateTokens int    `json:"cache_create_tokens,omitempty"`
	AdvisorBudget     int    `json:"advisor_budget,omitempty"`
	AdvisorUses       int    `json:"advisor_uses,omitempty"`
	AdvisorDenied     int    `json:"advisor_denied,omitempty"`
}

// DelegationFailedEvent records a failed delegated child task.
type DelegationFailedEvent struct {
	DelegationOccurrence
	AgentType     string `json:"agent_type,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
	TaskPreview   string `json:"task_preview"`
	Error         string `json:"error"`
	AdvisorBudget int    `json:"advisor_budget,omitempty"`
	AdvisorUses   int    `json:"advisor_uses,omitempty"`
	AdvisorDenied int    `json:"advisor_denied,omitempty"`
}

// DelegationWorktreeDisposalEvent records disposal of a cancelled code agent worktree.
type DelegationWorktreeDisposalEvent struct {
	AgentID string `json:"agent_id"`
	Removed bool   `json:"removed"`
	Error   string `json:"error,omitempty"`
}

// NewDelegationAcceptedEvent creates an event for an accepted delegation.
func NewDelegationAcceptedEvent(occ DelegationOccurrence, group string) Event {
	return newEvent(EventTypeDelegationAccepted, DelegationAcceptedEvent{DelegationOccurrence: occ, Group: group})
}

// NewDelegationStartedEvent creates a delegation started event with the
// resolved model alias and child agent type.
func NewDelegationStartedEvent(occ DelegationOccurrence, taskPreview, modelAlias, agentType string) Event {
	return newEvent(EventTypeDelegationStarted, DelegationStartedEvent{
		DelegationOccurrence: occ,
		TaskPreview:          TruncateWithEllipsis(taskPreview, 120),
		ModelAlias:           strings.TrimSpace(modelAlias),
		AgentType:            agentType,
	})
}

// NewDelegationQueuedEvent creates the event marking a sub-agent queued behind
// the running-agent cap.
func NewDelegationQueuedEvent(occ DelegationOccurrence, agentType, taskPreview string) Event {
	return newEvent(EventTypeDelegationQueued, DelegationQueuedEvent{
		DelegationOccurrence: occ,
		AgentType:            agentType,
		TaskPreview:          TruncateWithEllipsis(taskPreview, 120),
	})
}

// NewSubAgentsDeliveredEvent creates the event reporting sub-agent results
// delivered to the model in one message.
func NewSubAgentsDeliveredEvent(items []DeliveredSubAgent) Event {
	return newEvent(EventTypeSubAgentsDelivered, SubAgentsDeliveredEvent{Items: append([]DeliveredSubAgent(nil), items...)})
}

// NewSubAgentResultsUnansweredEvent creates the event reporting delivered
// sub-agent results the model never answered.
func NewSubAgentResultsUnansweredEvent(agentIDs []string, reason string) Event {
	return newEvent(EventTypeSubAgentResultsUnanswered, SubAgentResultsUnansweredEvent{
		AgentIDs: append([]string(nil), agentIDs...),
		Reason:   reason,
	})
}

// NewDelegationCacheWaitingEvent creates the event marking a gated delegation follower.
func NewDelegationCacheWaitingEvent(occ DelegationOccurrence, deadline time.Time) Event {
	return newEvent(EventTypeDelegationCacheWaiting, DelegationCacheWaitingEvent{
		DelegationOccurrence: occ,
		DeadlineUnixNano:     deadline.UnixNano(),
	})
}

// DelegationCompleteParams holds the arguments for NewDelegationCompleteEvent.
type DelegationCompleteParams struct {
	DelegationOccurrence
	AgentType         string
	DurationMs        int64
	Status            string
	TurnCount         int
	TokenCount        int
	ToolCallCount     int
	Output            string
	InputTokens       int
	CacheReadTokens   int
	CacheCreateTokens int
	AdvisorBudget     int
	AdvisorUses       int
	AdvisorDenied     int
}

// NewDelegationCompleteEvent creates a new delegation complete event.
func NewDelegationCompleteEvent(p DelegationCompleteParams) Event {
	return newEvent(EventTypeDelegationComplete, DelegationCompleteEvent(p))
}

// NewDelegationWorktreeDisposalEvent creates a worktree disposal event.
func NewDelegationWorktreeDisposalEvent(agentID string, removed bool, errMsg string) Event {
	return newEvent(EventTypeDelegationWorktreeDisposal, DelegationWorktreeDisposalEvent{
		AgentID: agentID,
		Removed: removed,
		Error:   errMsg,
	})
}

// DelegationFailedParams holds the arguments for NewDelegationFailedEvent.
type DelegationFailedParams struct {
	DelegationOccurrence
	AgentType     string
	DurationMs    int64
	TaskPreview   string
	Error         string
	AdvisorBudget int
	AdvisorUses   int
	AdvisorDenied int
}

// NewDelegationFailedEvent creates a new delegation failed event.
func NewDelegationFailedEvent(p DelegationFailedParams) Event {
	return newEvent(EventTypeDelegationFailed, DelegationFailedEvent{
		DelegationOccurrence: p.DelegationOccurrence,
		AgentType:            p.AgentType,
		DurationMs:           p.DurationMs,
		TaskPreview:          TruncateWithEllipsis(p.TaskPreview, 120),
		Error:                p.Error,
		AdvisorBudget:        p.AdvisorBudget,
		AdvisorUses:          p.AdvisorUses,
		AdvisorDenied:        p.AdvisorDenied,
	})
}
