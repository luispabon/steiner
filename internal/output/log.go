package output

import (
	"encoding/json"
	"fmt"
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
	EventTypeRunStarted         = "run_started"
	EventTypeRunFinished        = "run_finished"
	EventTypeTurnStarted        = "turn_started"
	EventTypeTurnFinished       = "turn_finished"
	EventTypeAssistantMessage   = "assistant_message"
	EventTypeAssistantChunk     = "assistant_chunk"
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
	payload.Summary, payload.Action = stopReasonSummary(reason, turn, payload.Error)
	return Event{
		Type:      EventTypeStopReason,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func stopReasonSummary(reason string, turn int, errText string) (string, string) {
	switch strings.TrimSpace(reason) {
	case "complete":
		if turn > 0 {
			return fmt.Sprintf("run complete after %d turn%s", turn, pluralSuffix(turn)), ""
		}
		return "run complete", ""
	case "max_turns":
		summary := "stopped after reaching the max turn limit"
		if turn > 0 {
			summary = fmt.Sprintf("stopped after %d turn%s: reached the max turn limit", turn, pluralSuffix(turn))
		}
		return summary, "increase limits.max_turns or continue in a new prompt"
	case "max_tokens":
		summary := "stopped after reaching the max token limit"
		if turn > 0 {
			summary = fmt.Sprintf("stopped at turn %d: reached the max token limit", turn)
		}
		return summary, "increase limits.max_tokens or reduce prompt and tool output size"
	case "cancelled":
		if turn > 0 {
			return fmt.Sprintf("run cancelled at turn %d", turn), "inspect /history for retained diagnostics or retry when you are ready to continue"
		}
		return "run cancelled", "inspect /history for retained diagnostics or retry when you are ready to continue"
	case "error":
		if strings.TrimSpace(errText) != "" {
			return "run failed", "inspect the reported error and retry"
		}
		return "run failed", "inspect the reported error and retry"
	default:
		if strings.TrimSpace(reason) == "" {
			return "", ""
		}
		return "stopped: " + reason, ""
	}
}

func pluralSuffix(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
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

func NewRunStartedEvent(mode, model, prompt string, maxTurns, maxTokens int) Event {
	return Event{
		Type:      EventTypeRunStarted,
		Timestamp: time.Now().UTC(),
		Payload: RunStartedEvent{
			Mode:      mode,
			Model:     model,
			Prompt:    prompt,
			MaxTurns:  maxTurns,
			MaxTokens: maxTokens,
		},
	}
}

func NewRunFinishedEvent(turn int, reason, summary, nextAction string, err error) Event {
	payload := RunFinishedEvent{
		Turn:       turn,
		Reason:     reason,
		Summary:    summary,
		NextAction: nextAction,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return Event{
		Type:      EventTypeRunFinished,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func NewTurnStartedEvent(turn int, model string, messageCount int) Event {
	return Event{
		Type:      EventTypeTurnStarted,
		Timestamp: time.Now().UTC(),
		Payload: TurnStartedEvent{
			Turn:         turn,
			Model:        model,
			MessageCount: messageCount,
		},
	}
}

func NewTurnFinishedEvent(turn, toolCalls int, finishReason, reply string, err error) Event {
	payload := TurnFinishedEvent{
		Turn:         turn,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Reply:        reply,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return Event{
		Type:      EventTypeTurnFinished,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func NewAssistantMessageEvent(turn int, role, content string) Event {
	return Event{
		Type:      EventTypeAssistantMessage,
		Timestamp: time.Now().UTC(),
		Payload: AssistantMessageEvent{
			Turn:    turn,
			Role:    role,
			Content: content,
		},
	}
}

func NewAssistantChunkEvent(turn int, content string) Event {
	return Event{
		Type:      EventTypeAssistantChunk,
		Timestamp: time.Now().UTC(),
		Payload: AssistantChunkEvent{
			Turn:    turn,
			Content: content,
		},
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
