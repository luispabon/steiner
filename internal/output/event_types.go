package output

import (
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

const (
	EventTypeModelCallStarted   = "model_call_started"
	EventTypeModelCallFinished  = "model_call_finished"
	EventTypeToolCallStarted    = "tool_call_started"
	EventTypeToolCallFinished   = "tool_call_finished"
	EventTypeApprovalRequested  = "approval_requested"
	EventTypeApprovalAccepted   = "approval_accepted"
	EventTypeApprovalDenied     = "approval_denied"
	EventTypeStopReason         = "stop_reason"
	EventTypeUserInput          = "user_input"
	EventTypeAPIRequest         = "api_request"
	EventTypeAPIResponse        = "api_response"
	EventTypeRunStarted         = "run_started"
	EventTypeRunFinished        = "run_finished"
	EventTypeTurnStarted        = "turn_started"
	EventTypeTurnFinished       = "turn_finished"
	EventTypeAssistantMessage   = "assistant_message"
	EventTypeAssistantChunk     = "assistant_chunk"
	EventTypeThinkingChunk      = "thinking_chunk"
	EventTypeContextReport      = "context_report"
	EventTypeHistoryLoaded      = "history_loaded"
	EventTypeContextDiagnostics = "context_diagnostics"
	EventTypeDelegationStarted  = "delegation_started"
	EventTypeDelegationComplete = "delegation_complete"
	EventTypeDelegationFailed   = "delegation_failed"
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

type AssistantChunkEvent struct {
	Turn    int    `json:"turn,omitempty"`
	Content string `json:"content,omitempty"`
}

type ThinkingChunkEvent struct {
	Turn    int    `json:"turn,omitempty"`
	Content string `json:"content,omitempty"`
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
}

type DelegationFailedEvent struct {
	AgentID     string `json:"agent_id"`
	TaskPreview string `json:"task_preview"`
	Error       string `json:"error"`
}

type HistoryLoadedEvent struct {
	Prompts []string `json:"prompts"`
}
