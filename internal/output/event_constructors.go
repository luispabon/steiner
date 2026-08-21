package output

import (
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// WithAgentScope attaches child-agent transcript scope metadata to an event.
func WithAgentScope(event Event, agentID string) Event {
	if agentID == "" {
		return event
	}
	event.Scope.AgentID = agentID
	return event
}

func newEvent(eventType string, payload any) Event {
	return Event{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

func newApprovalEvent(eventType string, turn int, tool, callID, mode, preview, message, kind, server, toolName string, allowed bool) Event {
	return newEvent(eventType, ApprovalEvent{
		Turn:     turn,
		Tool:     tool,
		CallID:   callID,
		Mode:     mode,
		Preview:  preview,
		Allowed:  allowed,
		Message:  message,
		Kind:     kind,
		Server:   server,
		ToolName: toolName,
	})
}

func newWorkflowHandoffEvent(eventType string, next, target, message, decision, submission string) Event {
	return newEvent(eventType, WorkflowHandoffEvent{
		Next:       strings.TrimSpace(next),
		Target:     strings.TrimSpace(target),
		Message:    strings.TrimSpace(message),
		Decision:   decision,
		Submission: strings.TrimSpace(submission),
	})
}

// NewHistoryLoadedEvent creates a new history loaded event.
func NewHistoryLoadedEvent(prompts []string) Event {
	return newEvent(EventTypeHistoryLoaded, HistoryLoadedEvent{Prompts: prompts})
}

// NewModelCallStartedEvent creates a new model call started event.
func NewModelCallStartedEvent(turn int, model string, messageCount int) Event {
	return newEvent(EventTypeModelCallStarted, ModelCallStartedEvent{
		Turn:         turn,
		Model:        model,
		MessageCount: messageCount,
	})
}

// ModelCallFinishedParams holds the arguments for NewModelCallFinishedEvent.
type ModelCallFinishedParams struct {
	Turn             int
	Model            string
	FinishReason     string
	ToolCalls        int
	CompletionTokens int
	Err              error
	DurationMs       int64
	TTFTMs           int64
	OutputTPS        float64
}

// NewModelCallFinishedEvent creates a new model call finished event.
func NewModelCallFinishedEvent(p ModelCallFinishedParams) Event {
	payload := ModelCallFinishedEvent{
		Turn:             p.Turn,
		Model:            p.Model,
		FinishReason:     p.FinishReason,
		ToolCalls:        p.ToolCalls,
		CompletionTokens: p.CompletionTokens,
		DurationMs:       p.DurationMs,
		TTFTMs:           p.TTFTMs,
		OutputTPS:        p.OutputTPS,
	}
	if p.Err != nil {
		payload.Error = p.Err.Error()
	}
	return newEvent(EventTypeModelCallFinished, payload)
}

// NewToolCallStartedEvent creates a new tool call started event.
func NewToolCallStartedEvent(turn int, toolName, callID string, arguments map[string]any) Event {
	return newEvent(EventTypeToolCallStarted, ToolCallStartedEvent{
		Turn:      turn,
		Tool:      toolName,
		CallID:    callID,
		Arguments: arguments,
	})
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
	return newEvent(EventTypeToolCallFinished, payload)
}

// NewApprovalRequestedEvent creates a new approval requested event.
func NewApprovalRequestedEvent(turn int, tool, callID, mode, preview, kind, server, toolName string) Event {
	return newApprovalEvent(EventTypeApprovalRequested, turn, tool, callID, mode, preview, "", kind, server, toolName, false)
}

// NewApprovalAcceptedEvent creates a new approval accepted event.
func NewApprovalAcceptedEvent(turn int, tool, callID, mode, preview, message, kind, server, toolName string) Event {
	return newApprovalEvent(EventTypeApprovalAccepted, turn, tool, callID, mode, preview, message, kind, server, toolName, true)
}

// NewApprovalDeniedEvent creates a new approval denied event.
func NewApprovalDeniedEvent(turn int, tool, callID, mode, preview, message, kind, server, toolName string) Event {
	return newApprovalEvent(EventTypeApprovalDenied, turn, tool, callID, mode, preview, message, kind, server, toolName, false)
}

// NewWorkflowHandoffRequestedEvent creates a new workflow handoff request event.
func NewWorkflowHandoffRequestedEvent(next, target, message, submission string) Event {
	return newWorkflowHandoffEvent(EventTypeWorkflowHandoffRequested, next, target, message, "", submission)
}

// NewWorkflowHandoffAcceptedEvent creates a workflow handoff accepted event.
func NewWorkflowHandoffAcceptedEvent(next, target, message string) Event {
	return newWorkflowHandoffEvent(EventTypeWorkflowHandoffAccepted, next, target, message, "accepted", "")
}

// NewWorkflowHandoffDeclinedEvent creates a workflow handoff declined event.
func NewWorkflowHandoffDeclinedEvent(next, target, message string) Event {
	return newWorkflowHandoffEvent(EventTypeWorkflowHandoffDeclined, next, target, message, "declined", "")
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
	payload.Summary, payload.Action = stopReasonSummary(reason, turn)
	return newEvent(EventTypeStopReason, payload)
}

func stopReasonSummary(reason string, turn int) (string, string) {
	switch strings.TrimSpace(reason) {
	case "complete":
		if turn > 0 {
			return fmt.Sprintf("run complete after %d turn%s", turn, PluralSuffix(turn, "", "s")), ""
		}
		return "run complete", ""
	case "workflow_handoff":
		return "workflow handoff accepted", "clear the current conversation and start the next workflow"
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
	return newEvent(EventTypeUserInput, UserInputEvent{
		Content: content,
		Mode:    mode,
	})
}

// NewAPIRequestEvent creates a new API request event.
func NewAPIRequestEvent(model string, messages []provider.Message, tools []provider.ToolSpec, maxTokens *int, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget) Event {
	return newEvent(EventTypeAPIRequest, APIRequestEvent{
		Model:    model,
		Messages: provider.CloneMessages(messages),
		Tools:    provider.CloneTools(tools),
		MaxTokens: func() *int {
			if maxTokens == nil {
				return nil
			}
			cloned := *maxTokens
			return &cloned
		}(),
		Blocks:      append([]prompt.ContextBlock(nil), blocks...),
		ModelBudget: budget,
	})
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
	return newEvent(EventTypeAPIResponse, payload)
}

// NewRunStartedEvent creates a new run started event.
func NewRunStartedEvent(mode, model, prompt string, maxTurns, maxTokens int) Event {
	return newEvent(EventTypeRunStarted, RunStartedEvent{
		Mode:      mode,
		Model:     model,
		Prompt:    prompt,
		MaxTurns:  maxTurns,
		MaxTokens: maxTokens,
	})
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
	return newEvent(EventTypeRunFinished, payload)
}

// NewOneshotFinishedEvent creates a new oneshot finished event.
func NewOneshotFinishedEvent(runID string, err error) Event {
	payload := OneshotFinishedEvent{
		RunID: runID,
	}
	if err != nil {
		payload.Err = err.Error()
	}
	return newEvent(EventTypeOneshotFinished, payload)
}

// NewAssistantMessageEvent creates a new assistant message event.
func NewAssistantMessageEvent(turn int, role, content string) Event {
	return newEvent(EventTypeAssistantMessage, AssistantMessageEvent{
		Turn:    turn,
		Role:    role,
		Content: content,
	})
}

// NewThinkingChunkEventWithSource creates a new thinking chunk event with an
// explicit chunk source.
func NewThinkingChunkEventWithSource(turn int, content string, source ChunkSource) Event {
	return newEvent(EventTypeThinkingChunk, ThinkingChunkEvent{
		Turn:    turn,
		Content: content,
		Source:  source,
	})
}

// NewAssistantChunkEventWithSource creates a new assistant chunk event with an
// explicit chunk source.
func NewAssistantChunkEventWithSource(turn int, content string, source ChunkSource) Event {
	return newEvent(EventTypeAssistantChunk, AssistantChunkEvent{
		Turn:    turn,
		Content: content,
		Source:  source,
	})
}

// NewDelegationStartedEvent creates a new delegation started event.
func NewDelegationStartedEvent(agentID, taskPreview string, callID ...string) Event {
	payload := DelegationStartedEvent{
		AgentID:     agentID,
		TaskPreview: TruncateWithEllipsis(taskPreview, 120),
	}
	if len(callID) > 0 {
		payload.CallID = callID[0]
	}
	return newEvent(EventTypeDelegationStarted, payload)
}

// NewDelegationStartedEventWithModel creates a delegation started event that
// records the resolved model alias assigned to the child.
func NewDelegationStartedEventWithModel(agentID, taskPreview, callID, modelAlias string) Event {
	payload := DelegationStartedEvent{
		AgentID:     agentID,
		TaskPreview: TruncateWithEllipsis(taskPreview, 120),
		CallID:      callID,
		ModelAlias:  strings.TrimSpace(modelAlias),
	}
	return newEvent(EventTypeDelegationStarted, payload)
}

// NewDelegationCacheWaitingEvent creates the event marking a gated delegation follower.
func NewDelegationCacheWaitingEvent(agentID, callID string, deadline time.Time) Event {
	return newEvent(EventTypeDelegationCacheWaiting, DelegationCacheWaitingEvent{
		AgentID:          agentID,
		CallID:           callID,
		DeadlineUnixNano: deadline.UnixNano(),
	})
}

// DelegationCompleteParams holds the arguments for NewDelegationCompleteEvent.
type DelegationCompleteParams struct {
	AgentID           string
	Status            string
	TurnCount         int
	TokenCount        int
	ToolCallCount     int
	Output            string
	InputTokens       int
	CacheReadTokens   int
	CacheCreateTokens int
}

// NewDelegationCompleteEvent creates a new delegation complete event.
func NewDelegationCompleteEvent(p DelegationCompleteParams) Event {
	return newEvent(EventTypeDelegationComplete, DelegationCompleteEvent(p))
}

// NewDisplayFileEvent creates a DisplayFile event with an explicit preview
// payload for the TUI to render.
func NewDisplayFileEvent(payload DisplayFilePayload) Event {
	return newEvent(EventTypeDisplayFile, payload)
}

// NewDelegationExtensionEvent creates a delegation_extension event when the
// delegate auto-extends past its original max_turns budget.
func NewDelegationExtensionEvent(agentID string, extension, maxExtensions int) Event {
	return newEvent(EventTypeDelegationExtension, DelegationExtensionEvent{
		AgentID:       agentID,
		Extension:     extension,
		MaxExtensions: maxExtensions,
	})
}

// NewSteerReceivedEvent creates an Event for a consumed steer message.
func NewSteerReceivedEvent(text string) Event {
	return newEvent(EventTypeSteerReceived, SteerReceivedEvent{Text: text})
}

// NewDelegationFailedEvent creates a new delegation failed event.
func NewDelegationFailedEvent(agentID, taskPreview, errMsg string) Event {
	return newEvent(EventTypeDelegationFailed, DelegationFailedEvent{
		AgentID:     agentID,
		TaskPreview: TruncateWithEllipsis(taskPreview, 120),
		Error:       errMsg,
	})
}

// NewAdvisorStartedEvent creates an advisor_started event.
func NewAdvisorStartedEvent(model string, useNumber, maxUses int, question string, files []string) Event {
	return newEvent(EventTypeAdvisorStarted, AdvisorStartedEvent{
		Model:     strings.TrimSpace(model),
		UseNumber: useNumber,
		MaxUses:   maxUses,
		Question:  strings.TrimSpace(question),
		Files:     append([]string(nil), files...),
	})
}

// NewAdvisorCompleteEvent creates an advisor_complete event.
func NewAdvisorCompleteEvent(model string, useNumber, maxUses int, note string, truncated bool, err error, cacheReadTokens, cacheCreateTokens, inputTokens int) Event {
	payload := AdvisorCompleteEvent{
		Model:             strings.TrimSpace(model),
		UseNumber:         useNumber,
		MaxUses:           maxUses,
		Note:              strings.TrimSpace(note),
		Truncated:         truncated,
		CacheReadTokens:   cacheReadTokens,
		CacheCreateTokens: cacheCreateTokens,
		InputTokens:       inputTokens,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return newEvent(EventTypeAdvisorComplete, payload)
}

// NewAdvisorBudgetExhaustedEvent creates an advisor_budget_exhausted event.
func NewAdvisorBudgetExhaustedEvent(model string, used, maxUses int, message, question string, files []string) Event {
	return newEvent(EventTypeAdvisorBudgetExhausted, AdvisorBudgetExhaustedEvent{
		Model:    strings.TrimSpace(model),
		Used:     used,
		MaxUses:  maxUses,
		Message:  strings.TrimSpace(message),
		Question: strings.TrimSpace(question),
		Files:    append([]string(nil), files...),
	})
}

// NewPhaseTransitionEvent creates a phase_transition event.
func NewPhaseTransitionEvent(runID, from, to, status, model, sessionID string) Event {
	return newEvent(EventTypePhaseTransition, PhaseTransitionEvent{
		RunID:     strings.TrimSpace(runID),
		From:      strings.TrimSpace(from),
		To:        strings.TrimSpace(to),
		Status:    strings.TrimSpace(status),
		Model:     strings.TrimSpace(model),
		SessionID: strings.TrimSpace(sessionID),
	})
}

// NewPhaseIndicatorEvent creates a phase_indicator event.
func NewPhaseIndicatorEvent(runID, phase, state, message string) Event {
	return newEvent(EventTypePhaseIndicator, PhaseIndicatorEvent{
		RunID:   strings.TrimSpace(runID),
		Phase:   strings.TrimSpace(phase),
		State:   strings.TrimSpace(state),
		Message: strings.TrimSpace(message),
	})
}

// NewModeChangedEvent creates a mode_changed event.
func NewModeChangedEvent(mode string) Event {
	return newEvent(EventTypeModeChanged, ModeChangedEvent{
		Mode: strings.TrimSpace(mode),
	})
}

// NewSandboxStatusEvent creates a sandbox_status event.
func NewSandboxStatusEvent(status, message string) Event {
	return newEvent(EventTypeSandboxStatus, SandboxStatusEvent{
		Status:  strings.TrimSpace(status),
		Message: strings.TrimSpace(message),
	})
}

// NewConfigWarningEvent creates a config_warning event.
func NewConfigWarningEvent(message string) Event {
	return newEvent(EventTypeConfigWarning, ConfigWarningEvent{Message: strings.TrimSpace(message)})
}

// NewMCPStatusEvent creates an mcp_status snapshot event carrying an immutable
// view of the MCP surface: whether MCP is enabled, every configured server's
// live state keyed by server name, and the registry's MCP tool origins.
func NewMCPStatusEvent(enabled bool, servers map[string]MCPServerState, origins map[string]MCPToolOrigin) Event {
	return newEvent(EventTypeMCPStatus, MCPStatusEvent{
		Enabled: enabled,
		Servers: servers,
		Origins: origins,
	})
}
