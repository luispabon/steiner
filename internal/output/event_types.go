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
	// EventTypeTurnStarted marks the start of a model turn.
	EventTypeTurnStarted = "turn_started"
	// EventTypeTurnFinished marks the end of a model turn.
	EventTypeTurnFinished = "turn_finished"
	// EventTypeAssistantMessage records a completed assistant message.
	EventTypeAssistantMessage = "assistant_message"
	// EventTypeAssistantChunk records a streamed assistant chunk.
	EventTypeAssistantChunk = "assistant_chunk"
	// EventTypeThinkingChunk records a streamed reasoning chunk.
	EventTypeThinkingChunk = "thinking_chunk"
	// EventTypeProviderDiagnostic records provider retry or transport diagnostics.
	EventTypeProviderDiagnostic = "provider_diagnostic"
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

	// EventTypeDisplayFile is emitted when the agent wants the TUI to display a
	// file to the user. The event payload carries an explicit preview document so
	// the UI can render the slice without the model-visible tool result or
	// conversation history containing file contents.
	EventTypeDisplayFile = "display_file"
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
	AgentID string `json:"agent_id,omitempty"`
}

// MarshalJSON omits empty scope metadata so top-level events keep their
// existing JSON shape.
func (e Event) MarshalJSON() ([]byte, error) {
	if e.Scope.AgentID == "" {
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
	Turn         int    `json:"turn"`
	Model        string `json:"model,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	ToolCalls    int    `json:"tool_calls,omitempty"`
	TotalTokens  int    `json:"total_tokens,omitempty"`
	Error        string `json:"error,omitempty"`
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
	Turn    int    `json:"turn"`
	Tool    string `json:"tool,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Preview string `json:"preview,omitempty"`
	Allowed bool   `json:"allowed"`
	Message string `json:"message,omitempty"`
}

// StopReasonEvent is the payload for EventTypeStopReason.
type StopReasonEvent struct {
	Reason  string `json:"reason"`
	Turn    int    `json:"turn,omitempty"`
	Error   string `json:"error,omitempty"`
	Summary string `json:"summary,omitempty"`
	Action  string `json:"action,omitempty"`
}

// UserInputEvent captures user input forwarded into the event stream.
type UserInputEvent struct {
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"`
}

// APIRequestEvent captures the provider request payload sent for a turn.
type APIRequestEvent struct {
	Model       string                  `json:"model,omitempty"`
	Messages    []provider.Message      `json:"messages,omitempty"`
	Tools       []provider.ToolSpec     `json:"tools,omitempty"`
	MaxTokens   *int                    `json:"max_tokens,omitempty"`
	Blocks      []prompt.ContextBlock   `json:"blocks,omitempty"`
	ModelBudget prompt.ModelTokenBudget `json:"model_budget,omitempty"`
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

// TurnStartedEvent marks the beginning of a model turn.
type TurnStartedEvent struct {
	Turn         int    `json:"turn"`
	Model        string `json:"model,omitempty"`
	MessageCount int    `json:"message_count,omitempty"`
}

// TurnFinishedEvent records the terminal state of a model turn.
type TurnFinishedEvent struct {
	Turn         int    `json:"turn"`
	ToolCalls    int    `json:"tool_calls,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	Reply        string `json:"reply,omitempty"`
	Error        string `json:"error,omitempty"`
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
	Turn        int    `json:"turn,omitempty"`
	Severity    string `json:"severity,omitempty"`
	Message     string `json:"message,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
	Delay       string `json:"delay,omitempty"`
	Partial     bool   `json:"partial,omitempty"`
}

// DelegationStartedEvent records the start of a delegated child task.
type DelegationStartedEvent struct {
	AgentID     string `json:"agent_id"`
	TaskPreview string `json:"task_preview"`
}

// DelegationCompleteEvent records a successful delegated child task.
type DelegationCompleteEvent struct {
	AgentID       string `json:"agent_id"`
	Status        string `json:"status"`
	TurnCount     int    `json:"turn_count"`
	TokenCount    int    `json:"token_count"`
	ToolCallCount int    `json:"tool_call_count"`
	Output        string `json:"output,omitempty"`
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

// HistoryLoadedEvent carries previously recorded prompt strings for display.
type HistoryLoadedEvent struct {
	Prompts []string `json:"prompts"`
}

// DisplayFilePayload is the payload for EventTypeDisplayFile.
type DisplayFilePayload struct {
	Path    string          `json:"path"`
	Offset  int             `json:"offset,omitempty"`
	Limit   int             `json:"limit,omitempty"`
	Preview PreviewDocument `json:"preview"`
}
