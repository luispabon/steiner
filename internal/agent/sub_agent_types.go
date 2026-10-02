package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
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
	BatchID      string `json:"batch_id,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
}

// SubAgentCompletion is one finished sub-agent result ready for delivery.
type SubAgentCompletion struct {
	// Seq orders completions within one process; it is not durable.
	Seq          uint64
	ParentCallID string
	// BatchID is the tool batch of the originating call; with ParentCallID it
	// identifies the delegation occurrence.
	BatchID   string
	AgentID   string
	AgentType string
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

var toolBatchSeq atomic.Uint64

// toolBatchSeparator joins the first call id plus nonce and the sequence
// number in a tool batch id; newToolBatchID and ToolBatchSeq share it.
const toolBatchSeparator = "#"

// toolBatchNonceJoiner joins the first call id and the process nonce in a
// tool batch id.
const toolBatchNonceJoiner = "~"

// toolBatchNonce is a per-process random token embedded in every batch id.
// Saved admissions persist batch ids from earlier processes, and toolBatchSeq
// restarts at 1 in each process, so without the nonce a resumed session could
// emit a live id equal to a replayed one when the provider repeats call ids.
// It cannot be skipped: (BatchID, CallID) must be a stable occurrence key.
var toolBatchNonce = newToolBatchNonce()

// newToolBatchNonce returns 4 random bytes as hex.
func newToolBatchNonce() string {
	var b [4]byte
	// Since Go 1.24 crypto/rand.Read never returns an error; it crashes the
	// program if the system entropy source fails, so ignoring it is safe.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// newToolBatchID returns a batch id unique within this process and, via the
// process nonce, across process restarts; providers may repeat tool-call ids,
// so the first call id alone cannot identify a batch.
func newToolBatchID(firstCallID string) string {
	return firstCallID + toolBatchNonceJoiner + toolBatchNonce + toolBatchSeparator +
		strconv.FormatUint(toolBatchSeq.Add(1), 10)
}

// ToolBatchSeq returns the process-wide monotonic sequence number embedded in
// a tool batch id produced by newToolBatchID. It reports false when id has no
// numeric suffix after its last separator.
func ToolBatchSeq(id string) (uint64, bool) {
	i := strings.LastIndex(id, toolBatchSeparator)
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(id[i+len(toolBatchSeparator):], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

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
