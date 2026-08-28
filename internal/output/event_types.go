package output

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

const (
	// EventTypeModelCallStarted marks the start of a provider call.
	EventTypeModelCallStarted = "model_call_started"
	// EventTypeModelCallFinished marks the end of a provider call.
	EventTypeModelCallFinished = "model_call_finished"
	// EventTypeToolCallStarted marks the start of a tool call.
	EventTypeToolCallStarted = "tool_call_started"
	// EventTypeToolCallFinished marks the end of a tool call.
	EventTypeToolCallFinished = "tool_call_finished"
	// EventTypeApprovalRequested marks a pending approval request.
	EventTypeApprovalRequested = "approval_requested"
	// EventTypeApprovalAccepted marks an accepted approval request.
	EventTypeApprovalAccepted = "approval_accepted"
	// EventTypeApprovalDenied marks a denied approval request.
	EventTypeApprovalDenied = "approval_denied"
	// EventTypeWorkflowHandoffRequested marks a pending workflow handoff request.
	EventTypeWorkflowHandoffRequested = "workflow_handoff_requested"
	// EventTypeWorkflowHandoffAccepted marks an accepted workflow handoff request.
	EventTypeWorkflowHandoffAccepted = "workflow_handoff_accepted"
	// EventTypeWorkflowHandoffDeclined marks a declined workflow handoff request.
	EventTypeWorkflowHandoffDeclined = "workflow_handoff_declined"
	// EventTypeStopReason records why a run stopped.
	EventTypeStopReason = "stop_reason"
	// EventTypeUserInput records user input entering the stream.
	EventTypeUserInput = "user_input"
	// EventTypeAPIRequest records an outbound provider request.
	EventTypeAPIRequest = "api_request"
	// EventTypeAPIResponse records an inbound provider response.
	EventTypeAPIResponse = "api_response"
	// EventTypeRunStarted marks the start of a top-level run.
	EventTypeRunStarted = "run_started"
	// EventTypeRunFinished marks the end of a top-level run.
	EventTypeRunFinished = "run_finished"
	// EventTypeOneshotFinished marks the end of a oneshot run.
	EventTypeOneshotFinished = "oneshot_finished"
	// EventTypeAssistantMessage records a completed assistant message.
	EventTypeAssistantMessage = "assistant_message"
	// EventTypeAssistantChunk records a streamed assistant chunk.
	EventTypeAssistantChunk = "assistant_chunk"
	// EventTypeThinkingChunk records a streamed reasoning chunk.
	EventTypeThinkingChunk = "thinking_chunk"
	// EventTypeProviderDiagnostic records provider retry or transport diagnostics.
	EventTypeProviderDiagnostic = "provider_diagnostic"
	// ProviderDiagnosticKindTransportOverride identifies a provider diagnostic
	// that is informational and safe to suppress from user-facing transcripts.
	ProviderDiagnosticKindTransportOverride = "transport_override"
	// EventTypeContextReport records the assembled context report.
	EventTypeContextReport = "context_report"
	// EventTypeHistoryLoaded records loaded conversation history.
	EventTypeHistoryLoaded = "history_loaded"
	// EventTypeContextDiagnostics records context assembly diagnostics.
	EventTypeContextDiagnostics = "context_diagnostics"
	// EventTypeDelegationStarted marks the start of sub-agent delegation.
	EventTypeDelegationStarted = "delegation_started"
	// EventTypeDelegationComplete marks successful sub-agent completion.
	EventTypeDelegationComplete = "delegation_complete"
	// EventTypeDelegationFailed marks failed sub-agent completion.
	EventTypeDelegationFailed = "delegation_failed"
	// EventTypeDelegationExtension records delegation-specific auxiliary events.
	EventTypeDelegationExtension = "delegation_extension"
	// EventTypeDelegationCacheWaiting marks a sub-agent delegation gated behind a shared prompt-cache dispatch slot.
	EventTypeDelegationCacheWaiting = "delegation_cache_waiting"
	// EventTypeAdvisorStarted marks the start of an advisor call.
	EventTypeAdvisorStarted = "advisor_started"
	// EventTypeAdvisorComplete marks the end of an advisor call.
	EventTypeAdvisorComplete = "advisor_complete"
	// EventTypeAdvisorBudgetExhausted marks a skipped advisor call because the
	// per-run budget was already exhausted.
	EventTypeAdvisorBudgetExhausted = "advisor_budget_exhausted"
	// EventTypePhaseTransition marks a oneshot phase transition.
	EventTypePhaseTransition = "phase_transition"
	// EventTypePhaseIndicator marks a oneshot phase status indicator.
	EventTypePhaseIndicator = "phase_indicator"

	// EventTypeDisplayFile is emitted when the agent wants the TUI to display a
	// file to the user. The event payload carries an explicit preview document so
	// the UI can render the slice without the model-visible tool result or
	// conversation history containing file contents.
	EventTypeDisplayFile = "display_file"

	// EventTypeSteerReceived is emitted when the agent loop consumes a queued
	// steering message and injects it into the conversation.
	EventTypeSteerReceived = "steer_received"
	// EventTypeModeChanged is emitted when the execution mode changes.
	EventTypeModeChanged = "mode_changed"
	// EventTypeSandboxStatus is emitted when the sandbox status is determined at startup.
	EventTypeSandboxStatus = "sandbox_status"
	// EventTypeConfigWarning is emitted when configuration carries a deprecated
	// key worth surfacing. It renders as a status line and never carries
	// sandbox or other sidebar state.
	EventTypeConfigWarning = "config_warning"
	// EventTypeMCPStatus is emitted when the MCP server set changes state. The
	// payload is an immutable snapshot of the MCP surface, so display consumers
	// never read the registry or manager concurrently.
	EventTypeMCPStatus = "mcp_status"
)

// Event is the timestamped envelope emitted by the runtime event stream.
type Event struct {
	Type      string     `json:"type"`
	Timestamp time.Time  `json:"timestamp"`
	Payload   any        `json:"payload,omitempty"`
	Scope     EventScope `json:"scope,omitempty"`
}

// EventScope identifies the transcript scope for a runtime event.
type EventScope struct {
	AgentID   string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
}

// MarshalJSON omits empty scope metadata so top-level events keep their
// existing JSON shape.
func (e Event) MarshalJSON() ([]byte, error) {
	if e.Scope.AgentID == "" && e.Scope.AgentType == "" {
		return json.Marshal(struct {
			Type      string    `json:"type"`
			Timestamp time.Time `json:"timestamp"`
			Payload   any       `json:"payload,omitempty"`
		}{
			Type:      e.Type,
			Timestamp: e.Timestamp,
			Payload:   e.Payload,
		})
	}

	return json.Marshal(struct {
		Type      string     `json:"type"`
		Timestamp time.Time  `json:"timestamp"`
		Payload   any        `json:"payload,omitempty"`
		Scope     EventScope `json:"scope,omitempty"`
	}{
		Type:      e.Type,
		Timestamp: e.Timestamp,
		Payload:   e.Payload,
		Scope:     e.Scope,
	})
}

// EventSink receives structured runtime events.
type EventSink interface {
	Emit(Event)
}

// SinkFunc adapts a function into an EventSink.
type SinkFunc func(Event)

// Emit forwards the event to the wrapped function.
func (f SinkFunc) Emit(event Event) {
	if f != nil {
		f(event)
	}
}

// NoopSink discards all events.
type NoopSink struct{}

// Emit discards the event.
func (NoopSink) Emit(Event) {}

// ForwardSink is a thread-safe EventSink whose target can be swapped at runtime.
// It starts with a NoopSink and forwards events to whatever target is set via Set.
// This is used to wire an event sink into tool environments before the full sink
// chain (including the TUI sink) is assembled.
type ForwardSink struct {
	mu     sync.RWMutex
	target EventSink
}

// NewForwardSink creates a ForwardSink that starts with a NoopSink target.
func NewForwardSink() *ForwardSink {
	return &ForwardSink{target: NoopSink{}}
}

// Set replaces the forwarding target. It is safe to call concurrently.
func (f *ForwardSink) Set(sink EventSink) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sink == nil {
		f.target = NoopSink{}
	} else {
		f.target = sink
	}
}

// Emit forwards the event to the current target. It is safe to call concurrently.
func (f *ForwardSink) Emit(event Event) {
	f.mu.RLock()
	t := f.target
	f.mu.RUnlock()
	t.Emit(event)
}

// ModelCallStartedEvent marks the beginning of a provider request.
type ModelCallStartedEvent struct {
	Turn         int    `json:"turn"`
	Model        string `json:"model,omitempty"`
	MessageCount int    `json:"message_count,omitempty"`
}

// ModelCallFinishedEvent records the outcome of a provider request.
type ModelCallFinishedEvent struct {
	Turn             int     `json:"turn"`
	Model            string  `json:"model,omitempty"`
	FinishReason     string  `json:"finish_reason,omitempty"`
	ToolCalls        int     `json:"tool_calls,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	DurationMs       int64   `json:"duration_ms,omitempty"`
	TTFTMs           int64   `json:"ttft_ms,omitempty"`
	OutputTPS        float64 `json:"output_tps,omitempty"`
	Error            string  `json:"error,omitempty"`
}

// ToolCallStartedEvent records a tool invocation before execution begins.
type ToolCallStartedEvent struct {
	Turn      int            `json:"turn"`
	Tool      string         `json:"tool,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolCallFinishedEvent records a completed tool invocation.
type ToolCallFinishedEvent struct {
	Turn    int         `json:"turn"`
	Tool    string      `json:"tool,omitempty"`
	CallID  string      `json:"call_id,omitempty"`
	Result  string      `json:"result,omitempty"`
	Error   string      `json:"error,omitempty"`
	Preview ToolPreview `json:"-"`
}

// ApprovalEvent captures approval lifecycle decisions for mutation tools.
type ApprovalEvent struct {
	Turn     int    `json:"turn"`
	Tool     string `json:"tool,omitempty"`
	CallID   string `json:"call_id,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Preview  string `json:"preview,omitempty"`
	Allowed  bool   `json:"allowed"`
	Message  string `json:"message,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Server   string `json:"server,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
}

// WorkflowHandoffEvent captures a workflow handoff request for later handling.
type WorkflowHandoffEvent struct {
	Next       string `json:"next,omitempty"`
	Target     string `json:"target,omitempty"`
	Message    string `json:"message,omitempty"`
	Decision   string `json:"decision,omitempty"`
	Submission string `json:"submission,omitempty"`
}

// StopReasonEvent is the payload for EventTypeStopReason.
type StopReasonEvent struct {
	Reason  string `json:"reason"`
	Turn    int    `json:"turn,omitempty"`
	Error   string `json:"error,omitempty"`
	Summary string `json:"summary,omitempty"`
	Action  string `json:"action,omitempty"`
}

// ImageBlock mirrors agent.ImageBlock's shape without importing internal/agent
// (which itself imports internal/output — importing it back would cycle).
type ImageBlock struct {
	ID        string `json:"id,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	SizeBytes int    `json:"size_bytes,omitempty"`
}

// UserInputEvent captures user input forwarded into the event stream.
type UserInputEvent struct {
	Content string       `json:"content"`
	Mode    string       `json:"mode,omitempty"`
	Images  []ImageBlock `json:"images,omitempty"`
}

// APIRequestEvent captures the provider request payload sent for a turn.
type APIRequestEvent struct {
	Model                 string                  `json:"model,omitempty"`
	Messages              []provider.Message      `json:"messages,omitempty"`
	Tools                 []provider.ToolSpec     `json:"tools,omitempty"`
	MaxTokens             *int                    `json:"max_tokens,omitempty"`
	Blocks                []prompt.ContextBlock   `json:"blocks,omitempty"`
	ModelBudget           prompt.ModelTokenBudget `json:"model_budget,omitempty"`
	EstimatedPromptTokens int                     `json:"estimated_prompt_tokens,omitempty"`
	Kind                  string                  `json:"kind,omitempty"`
}

// APIResponseEvent captures the provider response payload for a turn.
type APIResponseEvent struct {
	Message      any    `json:"message,omitempty"`
	Usage        any    `json:"usage,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	Error        string `json:"error,omitempty"`
}

// RunStartedEvent marks the start of a top-level run or compaction flow.
type RunStartedEvent struct {
	Mode      string `json:"mode,omitempty"`
	Model     string `json:"model,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	MaxTurns  int    `json:"max_turns,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// RunFinishedEvent records the terminal outcome of a run.
type RunFinishedEvent struct {
	Turn       int    `json:"turn,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Summary    string `json:"summary,omitempty"`
	NextAction string `json:"next_action,omitempty"`
	Error      string `json:"error,omitempty"`
}

// OneshotFinishedEvent records the terminal outcome of a oneshot run.
type OneshotFinishedEvent struct {
	RunID string `json:"run_id,omitempty"`
	Err   string `json:"err,omitempty"`
}

// AssistantMessageEvent records a completed assistant message.
type AssistantMessageEvent struct {
	Turn    int    `json:"turn,omitempty"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// ChunkSource identifies the origin of a streamed assistant or thinking chunk.
type ChunkSource string

const (
	// ChunkSourceAssistant identifies chunks emitted from assistant output.
	ChunkSourceAssistant ChunkSource = "assistant"
	// ChunkSourceAdvisor identifies chunks emitted by the stronger-model advisor.
	ChunkSourceAdvisor ChunkSource = "advisor"
)

// AssistantChunkEvent records a streamed assistant chunk.
type AssistantChunkEvent struct {
	Turn    int         `json:"turn,omitempty"`
	Content string      `json:"content,omitempty"`
	Source  ChunkSource `json:"source,omitempty"`
}

// ThinkingChunkEvent records a streamed reasoning chunk.
type ThinkingChunkEvent struct {
	Turn    int         `json:"turn,omitempty"`
	Content string      `json:"content,omitempty"`
	Source  ChunkSource `json:"source,omitempty"`
}

// ProviderDiagnosticEvent describes provider retry and transport diagnostics.
type ProviderDiagnosticEvent struct {
	Turn         int    `json:"turn,omitempty"`
	Severity     string `json:"severity,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Suppressible bool   `json:"suppressible,omitempty"`
	Message      string `json:"message,omitempty"`
	Attempt      int    `json:"attempt,omitempty"`
	MaxAttempts  int    `json:"max_attempts,omitempty"`
	Delay        string `json:"delay,omitempty"`
	Partial      bool   `json:"partial,omitempty"`
}

// DelegationStartedEvent records the start of a delegated child task.
type DelegationStartedEvent struct {
	AgentID     string `json:"agent_id"`
	TaskPreview string `json:"task_preview"`
	CallID      string `json:"call_id,omitempty"`
	ModelAlias  string `json:"model_alias,omitempty"`
}

// DelegationCacheWaitingEvent records a sub-agent delegation waiting behind a
// shared prompt-cache dispatch slot for the leader's first streamed token.
type DelegationCacheWaitingEvent struct {
	AgentID          string `json:"agent_id"`
	CallID           string `json:"call_id"`
	DeadlineUnixNano int64  `json:"deadline_unix_nano"`
}

// DelegationCompleteEvent records a successful delegated child task.
type DelegationCompleteEvent struct {
	AgentID           string `json:"agent_id"`
	Status            string `json:"status"`
	TurnCount         int    `json:"turn_count"`
	TokenCount        int    `json:"token_count"`
	ToolCallCount     int    `json:"tool_call_count"`
	Output            string `json:"output,omitempty"`
	InputTokens       int    `json:"input_tokens,omitempty"`
	CacheReadTokens   int    `json:"cache_read_tokens,omitempty"`
	CacheCreateTokens int    `json:"cache_create_tokens,omitempty"`
}

// DelegationFailedEvent records a failed delegated child task.
type DelegationFailedEvent struct {
	AgentID     string `json:"agent_id"`
	TaskPreview string `json:"task_preview"`
	Error       string `json:"error"`
}

// DelegationExtensionEvent is the payload for EventTypeDelegationExtension.
type DelegationExtensionEvent struct {
	AgentID       string `json:"agent_id"`
	Extension     int    `json:"extension"`
	MaxExtensions int    `json:"max_extensions"`
}

// AdvisorStartedEvent records a stronger-model advisor call beginning.
type AdvisorStartedEvent struct {
	Model     string   `json:"model,omitempty"`
	UseNumber int      `json:"use_number"`
	MaxUses   int      `json:"max_uses"`
	Question  string   `json:"question,omitempty"`
	Files     []string `json:"files,omitempty"`
}

// AdvisorCompleteEvent records a completed advisor call.
type AdvisorCompleteEvent struct {
	Model             string `json:"model,omitempty"`
	UseNumber         int    `json:"use_number"`
	MaxUses           int    `json:"max_uses"`
	Note              string `json:"note,omitempty"`
	Error             string `json:"error,omitempty"`
	Truncated         bool   `json:"truncated,omitempty"`
	CacheReadTokens   int    `json:"cache_read_tokens,omitempty"`
	CacheCreateTokens int    `json:"cache_create_tokens,omitempty"`
	InputTokens       int    `json:"input_tokens,omitempty"`
	TokenCount        int    `json:"token_count,omitempty"`
}

// AdvisorBudgetExhaustedEvent records a skipped advisor call after the per-run
// budget was exhausted.
type AdvisorBudgetExhaustedEvent struct {
	Model    string   `json:"model,omitempty"`
	Used     int      `json:"used"`
	MaxUses  int      `json:"max_uses"`
	Message  string   `json:"message,omitempty"`
	Question string   `json:"question,omitempty"`
	Files    []string `json:"files,omitempty"`
}

// PhaseTransitionEvent records a oneshot phase handoff.
type PhaseTransitionEvent struct {
	RunID     string `json:"run_id,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Status    string `json:"status,omitempty"`
	Model     string `json:"model,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// PhaseIndicatorEvent records a oneshot phase state update.
type PhaseIndicatorEvent struct {
	RunID   string `json:"run_id,omitempty"`
	Phase   string `json:"phase,omitempty"`
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

// HistoryLoadedEvent carries previously recorded prompt strings for display.
type HistoryLoadedEvent struct {
	Prompts []string `json:"prompts"`
}

// SteerReceivedEvent is emitted when a between-turn steering message is consumed.
type SteerReceivedEvent struct {
	Text string `json:"text"`
}

// DisplayFilePayload is the payload for EventTypeDisplayFile.
type DisplayFilePayload struct {
	Path    string          `json:"path"`
	Offset  int             `json:"offset,omitempty"`
	Limit   int             `json:"limit,omitempty"`
	Preview PreviewDocument `json:"preview"`
}

// ModeChangedEvent is emitted when the execution mode changes.
type ModeChangedEvent struct {
	Mode string `json:"mode"`
}

// SandboxStatusEvent is emitted when the sandbox status is determined at startup.
type SandboxStatusEvent struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ConfigWarningEvent carries a user-facing configuration warning message.
type ConfigWarningEvent struct {
	Message string `json:"message"`
}

// MCPServerState is the display-only view of one configured MCP server inside
// an MCPStatusEvent. The map key in MCPStatusEvent.Servers is the server's
// config key.
type MCPServerState struct {
	// State is the server's connection outcome: "connecting", "connected",
	// "reconnecting", "failed", "unavailable" or "disabled".
	State string `json:"state"`
	// Transport is the server's transport, e.g. "stdio".
	Transport string `json:"transport,omitempty"`
	// Tools lists every tool this server advertised, in advertised order, with
	// its access outcome; connected only, may be empty.
	Tools []MCPAdvertisedTool `json:"tools,omitempty"`
	// Error is the failure text; failed/unavailable only.
	Error string `json:"error,omitempty"`
}

// MCPAdvertisedTool is the display-only view of one tool a connected MCP server
// advertised, with its access outcome after filtering or approval denial.
type MCPAdvertisedTool struct {
	// Name is the MCP-native advertised tool name.
	Name string `json:"name"`
	// Outcome is "registered", "filtered" or "denied".
	Outcome string `json:"outcome"`
}

// MCPToolOrigin identifies the MCP server a registry tool name came from.
type MCPToolOrigin struct {
	// Server is the config key of the MCP server that provided the tool.
	Server string `json:"server"`
	// Tool is the original, unsanitised tool name as the server reported it.
	Tool string `json:"tool"`
}

// MCPStatusEvent is the payload for EventTypeMCPStatus: an immutable snapshot
// of the MCP surface (whether MCP is enabled, every configured server's live
// state, and the registry's MCP tool origins) that the TUI can rebuild its MCP
// display from without touching the manager or registry.
type MCPStatusEvent struct {
	Enabled bool                      `json:"enabled"`
	Servers map[string]MCPServerState `json:"servers,omitempty"`
	Origins map[string]MCPToolOrigin  `json:"origins,omitempty"`
}
