package output

import (
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

const (
	EventTypeModelCallStarted    = "model_call_started"
	EventTypeModelCallFinished   = "model_call_finished"
	EventTypeToolCallStarted     = "tool_call_started"
	EventTypeToolCallFinished    = "tool_call_finished"
	EventTypeApprovalRequested   = "approval_requested"
	EventTypeApprovalAccepted    = "approval_accepted"
	EventTypeApprovalDenied      = "approval_denied"
	EventTypeStopReason          = "stop_reason"
	EventTypeUserInput           = "user_input"
	EventTypeAPIRequest          = "api_request"
	EventTypeAPIResponse         = "api_response"
	EventTypeRunStarted          = "run_started"
	EventTypeRunFinished         = "run_finished"
	EventTypeTurnStarted         = "turn_started"
	EventTypeTurnFinished        = "turn_finished"
	EventTypeAssistantMessage    = "assistant_message"
	EventTypeAssistantChunk      = "assistant_chunk"
	EventTypeThinkingChunk       = "thinking_chunk"
	EventTypeProviderDiagnostic  = "provider_diagnostic"
	EventTypeContextReport       = "context_report"
	EventTypeHistoryLoaded       = "history_loaded"
	EventTypeContextDiagnostics  = "context_diagnostics"
	EventTypeDelegationStarted   = "delegation_started"
	EventTypeDelegationComplete  = "delegation_complete"
	EventTypeDelegationFailed    = "delegation_failed"
	EventTypeDelegationExtension = "delegation_extension"

	// EventTypeDisplayFile is emitted when the agent wants the TUI to display a
	// file to the user. The event payload carries an explicit preview document so
	// the UI can render the slice without the model-visible tool result or
	// conversation history containing file contents.
	EventTypeDisplayFile = "display_file"

	// EventTypeScratchpadUpdated is emitted when the agent scratchpad state
	// changes so the TUI can reflect the current task state in the sidebar.
	EventTypeScratchpadUpdated = "scratchpad_updated"
)

type Event struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload,omitempty"`
}

type EventSink interface {
	Emit(Event)
}

type SinkFunc func(Event)

func (f SinkFunc) Emit(event Event) {
	if f != nil {
		f(event)
	}
}

type NoopSink struct{}

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

type ModelCallStartedEvent struct {
	Turn         int    `json:"turn"`
	Model        string `json:"model,omitempty"`
	MessageCount int    `json:"message_count,omitempty"`
}

type ModelCallFinishedEvent struct {
	Turn         int    `json:"turn"`
	Model        string `json:"model,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	ToolCalls    int    `json:"tool_calls,omitempty"`
	TotalTokens  int    `json:"total_tokens,omitempty"`
	Error        string `json:"error,omitempty"`
}

type ToolCallStartedEvent struct {
	Turn                     int            `json:"turn"`
	Tool                     string         `json:"tool,omitempty"`
	CallID                   string         `json:"call_id,omitempty"`
	Arguments                map[string]any `json:"arguments,omitempty"`
	WriteTargetExistedBefore *bool          `json:"-"`
}

type ToolCallFinishedEvent struct {
	Turn    int         `json:"turn"`
	Tool    string      `json:"tool,omitempty"`
	CallID  string      `json:"call_id,omitempty"`
	Result  string      `json:"result,omitempty"`
	Error   string      `json:"error,omitempty"`
	Preview ToolPreview `json:"-"`
}

type ApprovalEvent struct {
	Turn    int    `json:"turn"`
	Tool    string `json:"tool,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Preview string `json:"preview,omitempty"`
	Allowed bool   `json:"allowed"`
	Message string `json:"message,omitempty"`
}

type StopReasonEvent struct {
	Reason  string `json:"reason"`
	Turn    int    `json:"turn,omitempty"`
	Error   string `json:"error,omitempty"`
	Summary string `json:"summary,omitempty"`
	Action  string `json:"action,omitempty"`
}

type UserInputEvent struct {
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"`
}

type APIRequestEvent struct {
	Model       string                  `json:"model,omitempty"`
	Messages    []provider.Message      `json:"messages,omitempty"`
	Tools       []provider.ToolSpec     `json:"tools,omitempty"`
	MaxTokens   *int                    `json:"max_tokens,omitempty"`
	Blocks      []prompt.ContextBlock   `json:"blocks,omitempty"`
	ModelBudget prompt.ModelTokenBudget `json:"model_budget,omitempty"`
}

type APIResponseEvent struct {
	Message      any    `json:"message,omitempty"`
	Usage        any    `json:"usage,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	Error        string `json:"error,omitempty"`
}

type RunStartedEvent struct {
	Mode      string `json:"mode,omitempty"`
	Model     string `json:"model,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	MaxTurns  int    `json:"max_turns,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

type RunFinishedEvent struct {
	Turn       int    `json:"turn,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Summary    string `json:"summary,omitempty"`
	NextAction string `json:"next_action,omitempty"`
	Error      string `json:"error,omitempty"`
}

type TurnStartedEvent struct {
	Turn         int    `json:"turn"`
	Model        string `json:"model,omitempty"`
	MessageCount int    `json:"message_count,omitempty"`
}

type TurnFinishedEvent struct {
	Turn         int    `json:"turn"`
	ToolCalls    int    `json:"tool_calls,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	Reply        string `json:"reply,omitempty"`
	Error        string `json:"error,omitempty"`
}

type AssistantMessageEvent struct {
	Turn    int    `json:"turn,omitempty"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type ChunkSource string

const (
	ChunkSourceAssistant         ChunkSource = "assistant"
	ChunkSourceScaffoldInference ChunkSource = "scaffold_inference"
)

type AssistantChunkEvent struct {
	Turn    int         `json:"turn,omitempty"`
	Content string      `json:"content,omitempty"`
	Source  ChunkSource `json:"source,omitempty"`
}

type ThinkingChunkEvent struct {
	Turn    int         `json:"turn,omitempty"`
	Content string      `json:"content,omitempty"`
	Source  ChunkSource `json:"source,omitempty"`
}

type ProviderDiagnosticEvent struct {
	Turn        int    `json:"turn,omitempty"`
	Severity    string `json:"severity,omitempty"`
	Message     string `json:"message,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
	Delay       string `json:"delay,omitempty"`
	Partial     bool   `json:"partial,omitempty"`
}

type DelegationStartedEvent struct {
	AgentID     string `json:"agent_id"`
	TaskPreview string `json:"task_preview"`
}

type DelegationCompleteEvent struct {
	AgentID    string `json:"agent_id"`
	Status     string `json:"status"`
	TurnCount  int    `json:"turn_count"`
	TokenCount int    `json:"token_count"`
	Output     string `json:"output,omitempty"`
}

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

// ScratchpadUpdatedEvent is the payload for EventTypeScratchpadUpdated.
// It carries the current scratchpad fields so the TUI can display them
// in the sidebar without parsing the rendered scratchpad string.
type ScratchpadUpdatedEvent struct {
	Intent    string `json:"intent,omitempty"`
	Decisions string `json:"decisions,omitempty"`
	Open      string `json:"open,omitempty"`
	Next      string `json:"next,omitempty"`
}

// DisplayFilePayload is the payload for EventTypeDisplayFile.
type DisplayFilePayload struct {
	Path    string          `json:"path"`
	Offset  int             `json:"offset,omitempty"`
	Limit   int             `json:"limit,omitempty"`
	Preview PreviewDocument `json:"preview"`
}
