package agent

import (
	"context"
	"time"

	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

// ToolExecutor runs a named tool invocation for the agent loop.
type ToolExecutor interface {
	Execute(ctx context.Context, toolName, callID string, input map[string]any) (any, error)
}

// usageRecorder records per-call token usage for cache-hit-rate analytics.
// It is satisfied by *usagestats.Recorder; nil means recording is disabled.
type usageRecorder interface {
	Record(usagestats.Observation)
}

// ToolBatchSealer closes delegation group membership for a tool batch in one
// scope. *delegation.Supervisor satisfies it.
type ToolBatchSealer interface {
	SealGroupBatch(scope, batchID string)
}

// RunRequest carries all parameters needed for a single agent run.
type RunRequest struct {
	Provider       provider.Provider
	Executor       ToolExecutor
	Tools          []provider.ToolSpec
	Prompt         prompt.AssemblyOptions
	ModelBudget    prompt.ModelTokenBudget
	ResolvedModel  provider.ResolvedModel
	MaxTokens      *int
	Limits         Limits
	Events         output.EventSink
	ContextManager *ContextStateManager

	// StreamingPreferred signals whether the caller wants streaming responses.
	// When false, ChatCompletion is tried first and streaming is used only as a
	// fallback. Interactive mode sets this to true; --exec defaults to false.
	StreamingPreferred bool

	// PromptCacheKey is a stable identifier sent to the provider to route
	// requests to the same cache shard across turns. Empty disables it.
	PromptCacheKey string

	// TransportSession routes this agent's requests within supported transports.
	TransportSession string
	// ParentTransportSession identifies the parent session for supported transports.
	ParentTransportSession string

	// CompactionLogPath is an optional file path for logging compaction request/response pairs.
	// When non-empty, compaction calls write their full API request and final response to this file.
	CompactionLogPath string

	// HistoryArchive receives the messages each compaction removes from the
	// conversation, once the compaction is adopted. Nil disables archiving
	// (exec runs and sub-agents).
	HistoryArchive HistoryArchive

	// DrainInbox returns the next boundary delivery. Nil for sub-agents.
	DrainInbox func() InboxDrain

	// Sealer and GroupScope seal the run stream's delegation group batches:
	// after every tool batch the loop calls Sealer.SealGroupBatch(GroupScope,
	// batchID) with the id stamped via WithToolBatchID. Either one unset
	// disables sealing; a scope is sealed only by the stream that owns it.
	Sealer     ToolBatchSealer
	GroupScope string

	// PendingSubAgents returns the current pending list for post-compaction
	// re-injection. Nil disables it.
	PendingSubAgents func() []PendingSubAgent

	// UsageRecorder records cache-hit-rate observations per usage-bearing model
	// response. Nil disables recording (tests, unwired paths).
	UsageRecorder usageRecorder

	// UsageSource identifies which call surface this run represents, for
	// session-scoped usage attribution. The zero value is usagestats.SourceParent.
	UsageSource usagestats.Source

	// VisionCapabilities tracks per-model vision capability for the session.
	// Nil disables capability-driven retry logic (tests, unwired paths preserve old behavior).
	VisionCapabilities *VisionCapabilities

	// ImageStore registers read-produced images for vision tools. Nil disables
	// registration, but does not prevent the parent model from receiving image data.
	ImageStore *ImageStore

	// ParallelClassOf classifies a tool call by name for concurrent-execution
	// purposes. Nil means every call runs serially, which is the pre-existing
	// behaviour and what child runs receive for the delegation class (children
	// cannot themselves delegate — see CLAUDE.md's sub-agent nesting invariant).
	// It also decides delegation admission: calls classified as
	// ParallelClassDelegation get a defaulted admission, so a request carrying
	// delegation tools must set it or no admission is recorded.
	ParallelClassOf func(toolName string) ParallelClass

	// MaxParallelTools bounds how many ParallelClassTool calls execute
	// concurrently within a turn. Must be at least 1 for any parallel
	// execution of that class to occur; a value <= 0 forces serial execution
	// for that class (see parallelRunLength's guard — never leave this path
	// reachable with an unbounded semaphore).
	MaxParallelTools int

	// MaxParallelDelegations bounds how many ParallelClassDelegation calls
	// (specialized sub-agent spawns, follow_up) execute concurrently within a
	// turn, independent of MaxParallelTools. Same zero/negative-means-serial
	// contract as MaxParallelTools.
	MaxParallelDelegations int

	// ReserveDelegationAgentID, when non-nil, mints the child agent ID for a
	// delegation tool call in the order the parent emitted its calls. The
	// ordered tool-queue phase calls it once per spawning call before any
	// handler runs, and the reserved ID is stamped on that call's execution
	// context so a concurrent handler binds its child to the parent's call
	// order instead of racing the ID generator. It returns "" for calls that
	// spawn no new child, such as follow_up resuming an existing agent. Nil
	// keeps the pre-existing behaviour: handlers generate their own ID.
	ReserveDelegationAgentID func(toolName string) string

	// SourceConversation is the real conversation with internal roles intact
	// (e.g. compaction summaries); when set, initializeRunState uses it
	// instead of reconstructing from Prompt.Conversation.
	SourceConversation []Message

	// SeedLineage is the lineage the run resumes from. Only its latest
	// generation's BackendState is used, and only when that generation matches
	// the source conversation (see seedRunLineage).
	SeedLineage ConversationLineage

	// SeedParentUsage is the last parent usage the run resumes from. It is
	// used only when it was measured on this run's model and context window
	// (see seedParentUsage). Nil starts without one.
	SeedParentUsage *ParentUsage

	// SeedLastMainRequest is the last main request the run resumes from. It is
	// used only when its provider alias, backend model ID and prompt cache key
	// all match this run's request (see seedLastMainRequest).
	SeedLastMainRequest LastMainRequest

	// Clock supplies the request start time recorded in LastMainRequest. Nil
	// uses the real clock.
	Clock Clock

	// Diagnostics receives cache-stream records for this run; emission is added
	// by stage 2 of the unified-diagnostics plan (issue #707). A nil writer is
	// a no-op, so unwired paths and tests need no special handling.
	Diagnostics *diagnostics.Writer

	// CacheBaseline, when non-nil, tracks the previous outbound message hash
	// sequence per cache-key identity so cache diagnostics can report the shared
	// prefix with the prior accepted request. Nil disables baseline tracking.
	CacheBaseline *CacheBaselineStore

	// AgentID and AgentType identify this run's delegation scope for
	// diagnostics records, mirroring output.WithAgentScope/WithAgentTypeScope.
	// Empty for the top-level parent run.
	AgentID   string
	AgentType string

	// TurnBudgetNotice, when non-nil, produces a message appended to the
	// conversation at each crossed turn-budget threshold. Nil disables notices.
	// The parent interactive run never sets this; only delegated children do.
	TurnBudgetNotice func(turnsUsed, maxTurns int) string
}

// LastMainRequest identifies the most recent successful main model request of
// a run: when it started and the provider alias, backend model ID and prompt
// cache key it was sent with. Compaction calls and failed calls never set it.
type LastMainRequest struct {
	At             time.Time
	ProviderAlias  string
	BackendModelID string
	PromptCacheKey string
}

// InboxDrain is one boundary delivery to the run.
type InboxDrain struct {
	// Message is appended to the conversation when non-nil.
	Message *Message
	// Wake forces another turn after a complete assistant-only turn.
	Wake bool
	// UserText is the user-typed text carried by Message; with Images it is
	// the SteerReceived payload. Empty text emits no event.
	UserText string
	// Images are the image blocks attached to UserText, carried into the
	// SteerReceived payload for the UI to render.
	Images []ImageBlock
}
