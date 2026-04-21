package output

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"
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
	EventTypeContextDiagnostics = "context_diagnostics"
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
	Turn      int            `json:"turn"`
	Tool      string         `json:"tool,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ToolCallFinishedEvent struct {
	Turn   int    `json:"turn"`
	Tool   string `json:"tool,omitempty"`
	CallID string `json:"call_id,omitempty"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type ApprovalEvent struct {
	Turn    int    `json:"turn"`
	Tool    string `json:"tool,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Allowed bool   `json:"allowed"`
	Message string `json:"message,omitempty"`
}

type StopReasonEvent struct {
	Reason string `json:"reason"`
	Turn   int    `json:"turn,omitempty"`
	Error  string `json:"error,omitempty"`
}

type UserInputEvent struct {
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"`
}

type APIRequestEvent struct {
	Model    string `json:"model,omitempty"`
	Messages any    `json:"messages,omitempty"`
	Tools    any    `json:"tools,omitempty"`
}

type APIResponseEvent struct {
	Message      any    `json:"message,omitempty"`
	Usage        any    `json:"usage,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`
	Error        string `json:"error,omitempty"`
}

func NewModelCallStartedEvent(turn int, model string, messageCount int) Event {
	return Event{
		Type:      EventTypeModelCallStarted,
		Timestamp: time.Now().UTC(),
		Payload: ModelCallStartedEvent{
			Turn:         turn,
			Model:        model,
			MessageCount: messageCount,
		},
	}
}

func NewModelCallFinishedEvent(turn int, model, finishReason string, toolCalls, totalTokens int, err error) Event {
	payload := ModelCallFinishedEvent{
		Turn:         turn,
		Model:        model,
		FinishReason: finishReason,
		ToolCalls:    toolCalls,
		TotalTokens:  totalTokens,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return Event{
		Type:      EventTypeModelCallFinished,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func NewToolCallStartedEvent(turn int, toolName, callID string, arguments map[string]any) Event {
	return Event{
		Type:      EventTypeToolCallStarted,
		Timestamp: time.Now().UTC(),
		Payload: ToolCallStartedEvent{
			Turn:      turn,
			Tool:      toolName,
			CallID:    callID,
			Arguments: arguments,
		},
	}
}

func NewToolCallFinishedEvent(turn int, toolName, callID string, result string, err error) Event {
	payload := ToolCallFinishedEvent{
		Turn:   turn,
		Tool:   toolName,
		CallID: callID,
		Result: result,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return Event{
		Type:      EventTypeToolCallFinished,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func NewApprovalRequestedEvent(turn int, toolName, mode string) Event {
	return Event{
		Type:      EventTypeApprovalRequested,
		Timestamp: time.Now().UTC(),
		Payload: ApprovalEvent{
			Turn: turn,
			Tool: toolName,
			Mode: mode,
		},
	}
}

func NewApprovalAcceptedEvent(turn int, toolName, mode, message string) Event {
	return Event{
		Type:      EventTypeApprovalAccepted,
		Timestamp: time.Now().UTC(),
		Payload: ApprovalEvent{
			Turn:    turn,
			Tool:    toolName,
			Mode:    mode,
			Allowed: true,
			Message: message,
		},
	}
}

func NewApprovalDeniedEvent(turn int, toolName, mode, message string) Event {
	return Event{
		Type:      EventTypeApprovalDenied,
		Timestamp: time.Now().UTC(),
		Payload: ApprovalEvent{
			Turn:    turn,
			Tool:    toolName,
			Mode:    mode,
			Allowed: false,
			Message: message,
		},
	}
}

func NewStopReasonEvent(turn int, reason string, err error) Event {
	payload := StopReasonEvent{
		Reason: reason,
		Turn:   turn,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return Event{
		Type:      EventTypeStopReason,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func NewUserInputEvent(content, mode string) Event {
	return Event{
		Type:      EventTypeUserInput,
		Timestamp: time.Now().UTC(),
		Payload: UserInputEvent{
			Content: content,
			Mode:    mode,
		},
	}
}

func NewAPIRequestEvent(model string, messages, tools any) Event {
	return Event{
		Type:      EventTypeAPIRequest,
		Timestamp: time.Now().UTC(),
		Payload: APIRequestEvent{
			Model:    model,
			Messages: messages,
			Tools:    tools,
		},
	}
}

func NewAPIResponseEvent(message, usage any, finishReason string, err error) Event {
	payload := APIResponseEvent{
		Message:      message,
		Usage:        usage,
		FinishReason: finishReason,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return Event{
		Type:      EventTypeAPIResponse,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func CompactJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func SetupLogger(level string) *slog.Logger {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	return slog.New(handler)
}

func parseLevel(level string) slog.Leveler {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace":
		return slog.Level(-8)
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}
