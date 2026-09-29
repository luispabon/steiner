package agent

import (
	"context"
	"encoding/json"
	"time"
)

// SubAgentState is a pending sub-agent's lifecycle state as shown to the model.
type SubAgentState string

const (
	// SubAgentRunning means the sub-agent is executing.
	SubAgentRunning SubAgentState = "running"
	// SubAgentQueued means the sub-agent is waiting for a running slot.
	SubAgentQueued SubAgentState = "queued"
	// SubAgentFinished means the sub-agent has a result that is not yet delivered.
	SubAgentFinished SubAgentState = "finished"
)

// PendingSubAgent is one undelivered sub-agent for the pending line.
type PendingSubAgent struct {
	AgentID   string
	AgentType string
	State     SubAgentState
}

// SubAgentLedgerEntry is the durable record of an outstanding sub-agent.
type SubAgentLedgerEntry struct {
	AgentID      string `json:"agent_id"`
	AgentType    string `json:"agent_type"`
	ParentCallID string `json:"parent_call_id"`
	Group        string `json:"group,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
}

// SubAgentCompletion is one finished sub-agent result ready for delivery.
type SubAgentCompletion struct {
	// Seq orders completions within one process; it is not durable.
	Seq          uint64
	ParentCallID string
	AgentID      string
	AgentType    string
	// Status is the delegation result status, or "lost".
	Status string
	// Quiet is true when the user caused the cancellation.
	Quiet            bool
	ObjectivePreview string
	TurnCount        int
	TokenCount       int
	Duration         time.Duration
	// Body is the JSON of the projected DelegationResultEnvelope.
	Body string
}

// CompletionSink receives released completions, already group-ordered.
type CompletionSink interface {
	DeliverCompletions([]SubAgentCompletion)
}

type toolBatchIDKey struct{}

// WithToolBatchID returns ctx stamped with the id of the current tool batch.
func WithToolBatchID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, toolBatchIDKey{}, id)
}

// ToolBatchIDFrom returns the tool batch id stamped on ctx, or "".
func ToolBatchIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(toolBatchIDKey{}).(string)
	return id
}

// ProjectedToolResult returns the JSON of value's provider-facing projection
// when value defines one.
func ProjectedToolResult(value any) (string, bool) {
	return projectedToolResult(value)
}

// ProjectedToolError returns the JSON of err's provider-facing projection when
// err defines one.
func ProjectedToolError(err error) (string, bool) {
	return projectedToolError(err)
}

// FailureBody returns the JSON envelope for a failed tool call with no
// projection of its own.
func FailureBody(status, message string) string {
	data, err := json.Marshal(DelegationResultEnvelope{Output: message, Status: status, Reason: message})
	if err != nil {
		return message
	}
	return string(data)
}
