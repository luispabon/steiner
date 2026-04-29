package output

import (
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// NewHistoryLoadedEvent creates a new history loaded event.
func NewHistoryLoadedEvent(prompts []string) Event {
	return Event{
		Type:      EventTypeHistoryLoaded,
		Timestamp: time.Now().UTC(),
		Payload: HistoryLoadedEvent{
			Prompts: prompts,
		},
	}
}

// NewModelCallStartedEvent creates a new model call started event.
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

// NewModelCallFinishedEvent creates a new model call finished event.
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

// NewToolCallStartedEvent creates a new tool call started event.
func NewToolCallStartedEvent(turn int, toolName, callID string, arguments map[string]any) Event {
	return NewToolCallStartedEventWithPreviewState(turn, toolName, callID, arguments, nil)
}

// NewToolCallStartedEventWithPreviewState creates a new tool call started event with preview state.
func NewToolCallStartedEventWithPreviewState(turn int, toolName, callID string, arguments map[string]any, writeTargetExistedBefore *bool) Event {
	return Event{
		Type:      EventTypeToolCallStarted,
		Timestamp: time.Now().UTC(),
		Payload: ToolCallStartedEvent{
			Turn:                     turn,
			Tool:                     toolName,
			CallID:                   callID,
			Arguments:                arguments,
			WriteTargetExistedBefore: writeTargetExistedBefore,
		},
	}
}

// NewToolCallFinishedEvent creates a new tool call finished event.
func NewToolCallFinishedEvent(turn int, toolName, callID string, result string, err error) Event {
	return NewToolCallFinishedEventWithPreview(turn, toolName, callID, result, err, ToolPreview{})
}

// NewToolCallFinishedEventWithPreview creates a new tool call finished event with preview.
func NewToolCallFinishedEventWithPreview(turn int, toolName, callID string, result string, err error, preview ToolPreview) Event {
	payload := ToolCallFinishedEvent{
		Turn:    turn,
		Tool:    toolName,
		CallID:  callID,
		Result:  result,
		Preview: preview,
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

// NewApprovalRequestedEvent creates a new approval requested event.
func NewApprovalRequestedEvent(turn int, toolName, mode, preview string) Event {
	return Event{
		Type:      EventTypeApprovalRequested,
		Timestamp: time.Now().UTC(),
		Payload: ApprovalEvent{
			Turn:    turn,
			Tool:    toolName,
			Mode:    mode,
			Preview: preview,
		},
	}
}

// NewApprovalAcceptedEvent creates a new approval accepted event.
func NewApprovalAcceptedEvent(turn int, toolName, mode, preview, message string) Event {
	return Event{
		Type:      EventTypeApprovalAccepted,
		Timestamp: time.Now().UTC(),
		Payload: ApprovalEvent{
			Turn:    turn,
			Tool:    toolName,
			Mode:    mode,
			Preview: preview,
			Allowed: true,
			Message: message,
		},
	}
}

// NewApprovalDeniedEvent creates a new approval denied event.
func NewApprovalDeniedEvent(turn int, toolName, mode, preview, message string) Event {
	return Event{
		Type:      EventTypeApprovalDenied,
		Timestamp: time.Now().UTC(),
		Payload: ApprovalEvent{
			Turn:    turn,
			Tool:    toolName,
			Mode:    mode,
			Preview: preview,
			Allowed: false,
			Message: message,
		},
	}
}

// NewStopReasonEvent creates a new stop reason event.
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
			return fmt.Sprintf("run complete after %d turn%s", turn, PluralSuffix(turn, "", "s")), ""
		}
		return "run complete", ""
	case "max_turns":
		summary := "stopped after reaching the max turn limit"
		if turn > 0 {
			summary = fmt.Sprintf("stopped after %d turn%s: reached the max turn limit", turn, PluralSuffix(turn, "", "s"))
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

// NewUserInputEvent creates a new user input event.
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

// NewAPIRequestEvent creates a new API request event.
func NewAPIRequestEvent(model string, messages []provider.Message, tools []provider.ToolSpec, maxTokens *int, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget) Event {
	return Event{
		Type:      EventTypeAPIRequest,
		Timestamp: time.Now().UTC(),
		Payload: APIRequestEvent{
			Model:       model,
			Messages:    cloneProviderMessages(messages),
			Tools:       cloneProviderTools(tools),
			MaxTokens:   cloneOptionalInt(maxTokens),
			Blocks:      append([]prompt.ContextBlock(nil), blocks...),
			ModelBudget: budget,
		},
	}
}

// NewAPIResponseEvent creates a new API response event.
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

// NewRunStartedEvent creates a new run started event.
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

// NewRunFinishedEvent creates a new run finished event.
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

// NewTurnStartedEvent creates a new turn started event.
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

// NewTurnFinishedEvent creates a new turn finished event.
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

// NewAssistantMessageEvent creates a new assistant message event.
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

// NewThinkingChunkEvent creates a new thinking chunk event.
func NewThinkingChunkEvent(turn int, content string) Event {
	return Event{
		Type:      EventTypeThinkingChunk,
		Timestamp: time.Now().UTC(),
		Payload: ThinkingChunkEvent{
			Turn:    turn,
			Content: content,
		},
	}
}

// NewAssistantChunkEvent creates a new assistant chunk event.
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

// NewDelegationStartedEvent creates a new delegation started event.
func NewDelegationStartedEvent(agentID, taskPreview string) Event {
	return Event{
		Type:      EventTypeDelegationStarted,
		Timestamp: time.Now().UTC(),
		Payload: DelegationStartedEvent{
			AgentID:     agentID,
			TaskPreview: TruncateWithEllipsis(taskPreview, 120),
		},
	}
}

// NewDelegationCompleteEvent creates a new delegation complete event.
func NewDelegationCompleteEvent(agentID, status string, turns, tokens int) Event {
	return Event{
		Type:      EventTypeDelegationComplete,
		Timestamp: time.Now().UTC(),
		Payload: DelegationCompleteEvent{
			AgentID:    agentID,
			Status:     status,
			TurnCount:  turns,
			TokenCount: tokens,
		},
	}
}

// NewDisplayFileEvent creates a DisplayFile event that instructs the TUI to
// render the file at path. An optional language hint can be supplied for
// syntax highlighting; pass an empty string to let the TUI infer from the
// file extension. File contents must not be passed here — the TUI loads them.
func NewDisplayFileEvent(path, language string) Event {
	return Event{
		Type:      EventTypeDisplayFile,
		Timestamp: time.Now().UTC(),
		Payload: DisplayFilePayload{
			Path:     path,
			Language: language,
		},
	}
}

// NewDelegationFailedEvent creates a new delegation failed event.
func NewDelegationFailedEvent(agentID, taskPreview, errMsg string) Event {
	return Event{
		Type:      EventTypeDelegationFailed,
		Timestamp: time.Now().UTC(),
		Payload: DelegationFailedEvent{
			AgentID:     agentID,
			TaskPreview: TruncateWithEllipsis(taskPreview, 120),
			Error:       errMsg,
		},
	}
}
