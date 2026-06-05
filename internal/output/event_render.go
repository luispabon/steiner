package output

import (
	"fmt"
	"reflect"
	"strings"
)

func renderEvent(event Event) Segment {
	if renderer, ok := eventRenderers[reflect.TypeOf(event.Payload)]; ok {
		return renderer(event)
	}
	return renderUnknownEvent(event)
}

var eventRenderers = map[reflect.Type]func(Event) Segment{
	reflect.TypeOf(RunStartedEvent{}): func(event Event) Segment {
		return renderRunStartedEvent(event.Payload.(RunStartedEvent))
	},
	reflect.TypeOf(TurnStartedEvent{}): func(event Event) Segment {
		return renderTurnStartedEvent(event.Payload.(TurnStartedEvent))
	},
	reflect.TypeOf(TurnFinishedEvent{}): func(event Event) Segment {
		return renderTurnFinishedEvent(event.Payload.(TurnFinishedEvent))
	},
	reflect.TypeOf(AssistantMessageEvent{}): func(event Event) Segment {
		return renderAssistantMessageEvent(event.Payload.(AssistantMessageEvent))
	},
	reflect.TypeOf(AssistantChunkEvent{}): func(event Event) Segment {
		return renderAssistantChunkEvent(event.Payload.(AssistantChunkEvent))
	},
	reflect.TypeOf(ThinkingChunkEvent{}): func(event Event) Segment {
		return renderThinkingChunkEvent(event.Payload.(ThinkingChunkEvent))
	},
	reflect.TypeOf(ProviderDiagnosticEvent{}): func(event Event) Segment {
		return renderProviderDiagnosticEvent(event.Payload.(ProviderDiagnosticEvent))
	},
	reflect.TypeOf(ContextReportEvent{}): func(event Event) Segment {
		payload := event.Payload.(ContextReportEvent)
		return Segment{Channel: ChannelAssistant, Label: "context", Text: payload.Content}
	},
	reflect.TypeOf(DisplayFilePayload{}): func(event Event) Segment {
		return renderDisplayFileEvent(event.Payload.(DisplayFilePayload))
	},
	reflect.TypeOf(ModelCallStartedEvent{}): func(event Event) Segment {
		return renderModelCallStartedEvent(event.Payload.(ModelCallStartedEvent))
	},
	reflect.TypeOf(ModelCallFinishedEvent{}): func(event Event) Segment {
		return renderModelCallFinishedEvent(event.Payload.(ModelCallFinishedEvent))
	},
	reflect.TypeOf(ToolCallStartedEvent{}): func(event Event) Segment {
		return renderToolCallStartedEvent(event.Payload.(ToolCallStartedEvent))
	},
	reflect.TypeOf(ToolCallFinishedEvent{}): func(event Event) Segment {
		return renderToolCallFinishedEvent(event.Payload.(ToolCallFinishedEvent))
	},
	reflect.TypeOf(ApprovalEvent{}): func(event Event) Segment {
		return renderApprovalEvent(event, event.Payload.(ApprovalEvent))
	},
	reflect.TypeOf(WorkflowHandoffEvent{}): func(event Event) Segment {
		return renderWorkflowHandoffEvent(event.Payload.(WorkflowHandoffEvent))
	},
	reflect.TypeOf(StopReasonEvent{}): func(event Event) Segment {
		return renderStopReasonEvent(event.Payload.(StopReasonEvent))
	},
	reflect.TypeOf(UserInputEvent{}): func(event Event) Segment {
		return renderUserInputEvent(event.Payload.(UserInputEvent))
	},
	reflect.TypeOf(APIRequestEvent{}): func(event Event) Segment {
		return renderAPIRequestEvent(event.Payload.(APIRequestEvent))
	},
	reflect.TypeOf(APIResponseEvent{}): func(event Event) Segment {
		return renderAPIResponseEvent(event.Payload.(APIResponseEvent))
	},
	reflect.TypeOf(ContextDiagnosticsEvent{}): func(event Event) Segment {
		payload := event.Payload.(ContextDiagnosticsEvent)
		return Segment{Channel: ChannelStatus, Label: "context", Text: formatContextDiagnosticsEvent(payload)}
	},
}

func renderRunStartedEvent(payload RunStartedEvent) Segment {
	parts := []string{"run started"}
	if payload.Mode != "" {
		parts = append(parts, fmt.Sprintf("mode=%s", payload.Mode))
	}
	if payload.Model != "" {
		parts = append(parts, fmt.Sprintf("model=%s", payload.Model))
	}
	if payload.Prompt != "" {
		parts = append(parts, fmt.Sprintf("prompt=%s", payload.Prompt))
	}
	if payload.MaxTurns > 0 {
		parts = append(parts, fmt.Sprintf("turn_limit=%d", payload.MaxTurns))
	}
	if payload.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("token_limit=%d", payload.MaxTokens))
	}
	return Segment{Channel: ChannelStatus, Label: "status", Text: strings.Join(parts, " ")}
}

func renderTurnStartedEvent(payload TurnStartedEvent) Segment {
	parts := []string{
		fmt.Sprintf("turn=%d started", payload.Turn),
	}
	if payload.MessageCount > 0 {
		parts = append(parts, fmt.Sprintf("messages=%d", payload.MessageCount))
	}
	if payload.Model != "" {
		parts = append(parts, fmt.Sprintf("model=%s", payload.Model))
	}
	return Segment{Channel: ChannelStatus, Label: "status", Text: strings.Join(parts, " ")}
}

func renderTurnFinishedEvent(payload TurnFinishedEvent) Segment {
	parts := []string{
		fmt.Sprintf("turn=%d finished", payload.Turn),
	}
	if payload.ToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("tool_calls=%d", payload.ToolCalls))
	}
	if payload.FinishReason != "" {
		parts = append(parts, fmt.Sprintf("finish=%s", payload.FinishReason))
	}
	if payload.Reply != "" {
		parts = append(parts, fmt.Sprintf("reply=%s", payload.Reply))
	}
	channel := ChannelStatus
	if payload.Error != "" {
		channel = ChannelError
		parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
	}
	return Segment{Channel: channel, Label: string(channel), Text: strings.Join(parts, " ")}
}

func renderAssistantMessageEvent(payload AssistantMessageEvent) Segment {
	parts := []string{}
	if payload.Turn > 0 {
		parts = append(parts, fmt.Sprintf("turn=%d", payload.Turn))
	}
	if payload.Role != "" {
		parts = append(parts, fmt.Sprintf("role=%s", payload.Role))
	}
	if payload.Content != "" {
		parts = append(parts, fmt.Sprintf("content=%s", payload.Content))
	}
	return Segment{Channel: ChannelAssistant, Label: "assistant", Text: strings.Join(parts, " ")}
}

func renderAssistantChunkEvent(payload AssistantChunkEvent) Segment {
	parts := []string{}
	if payload.Turn > 0 {
		parts = append(parts, fmt.Sprintf("turn=%d", payload.Turn))
	}
	if payload.Source != "" {
		parts = append(parts, fmt.Sprintf("source=%s", payload.Source))
	}
	if payload.Content != "" {
		parts = append(parts, fmt.Sprintf("chunk=%s", payload.Content))
	}
	return Segment{Channel: ChannelAssistant, Label: "assistant", Text: strings.Join(parts, " ")}
}

func renderThinkingChunkEvent(payload ThinkingChunkEvent) Segment {
	parts := []string{}
	if payload.Turn > 0 {
		parts = append(parts, fmt.Sprintf("turn=%d", payload.Turn))
	}
	if payload.Source != "" {
		parts = append(parts, fmt.Sprintf("source=%s", payload.Source))
	}
	if payload.Content != "" {
		parts = append(parts, fmt.Sprintf("thinking=%s", payload.Content))
	}
	return Segment{Channel: ChannelStatus, Label: "thinking", Text: strings.Join(parts, " ")}
}

func renderProviderDiagnosticEvent(payload ProviderDiagnosticEvent) Segment {
	parts := []string{"provider"}
	if payload.Turn > 0 {
		parts = append(parts, fmt.Sprintf("turn=%d", payload.Turn))
	}
	if payload.Severity != "" {
		parts = append(parts, payload.Severity)
	}
	if payload.Attempt > 0 {
		parts = append(parts, fmt.Sprintf("attempt=%d", payload.Attempt))
	}
	if payload.MaxAttempts > 0 {
		parts = append(parts, fmt.Sprintf("max=%d", payload.MaxAttempts))
	}
	if payload.Delay != "" {
		parts = append(parts, fmt.Sprintf("delay=%s", payload.Delay))
	}
	if payload.Partial {
		parts = append(parts, "partial=true")
	}
	if payload.Message != "" {
		parts = append(parts, fmt.Sprintf("message=%s", payload.Message))
	}
	return Segment{Channel: ChannelStatus, Label: "status", Text: strings.Join(parts, " ")}
}

func renderDisplayFileEvent(payload DisplayFilePayload) Segment {
	parts := []string{"display"}
	if payload.Path != "" {
		parts = append(parts, fmt.Sprintf("path=%s", payload.Path))
	}
	if payload.Preview.Language != "" {
		parts = append(parts, fmt.Sprintf("syntax=%s", payload.Preview.Language))
	}
	if payload.Offset > 0 {
		parts = append(parts, fmt.Sprintf("offset=%d", payload.Offset))
	}
	if payload.Limit > 0 {
		parts = append(parts, fmt.Sprintf("limit=%d", payload.Limit))
	}
	return Segment{Channel: ChannelStatus, Label: "status", Text: strings.Join(parts, " ")}
}

func renderModelCallStartedEvent(payload ModelCallStartedEvent) Segment {
	return Segment{
		Channel: ChannelStatus,
		Label:   "status",
		Text:    fmt.Sprintf("model turn=%d started messages=%d model=%s", payload.Turn, payload.MessageCount, fallback(payload.Model, "unknown")),
	}
}

func renderModelCallFinishedEvent(payload ModelCallFinishedEvent) Segment {
	parts := []string{
		fmt.Sprintf("model turn=%d finished", payload.Turn),
	}
	if payload.Model != "" {
		parts = append(parts, fmt.Sprintf("model=%s", payload.Model))
	}
	if payload.FinishReason != "" {
		parts = append(parts, fmt.Sprintf("finish=%s", payload.FinishReason))
	}
	if payload.ToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("tool_calls=%d", payload.ToolCalls))
	}
	if payload.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens=%d", payload.TotalTokens))
	}
	channel := ChannelStatus
	if payload.Error != "" {
		channel = ChannelError
		parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
	}
	return Segment{Channel: channel, Label: string(channel), Text: strings.Join(parts, " ")}
}

func renderToolCallStartedEvent(payload ToolCallStartedEvent) Segment {
	parts := []string{
		fmt.Sprintf("turn=%d start", payload.Turn),
	}
	if payload.Tool != "" {
		parts = append(parts, fmt.Sprintf("tool=%s", payload.Tool))
	}
	if payload.CallID != "" {
		parts = append(parts, fmt.Sprintf("id=%s", payload.CallID))
	}
	if len(payload.Arguments) > 0 {
		parts = append(parts, fmt.Sprintf("args=%s", CompactJSON(payload.Arguments)))
	}
	return Segment{Channel: ChannelTool, Label: "tool", Text: strings.Join(parts, " ")}
}

func renderToolCallFinishedEvent(payload ToolCallFinishedEvent) Segment {
	parts := []string{
		fmt.Sprintf("turn=%d end", payload.Turn),
	}
	if payload.Tool != "" {
		parts = append(parts, fmt.Sprintf("tool=%s", payload.Tool))
	}
	if payload.CallID != "" {
		parts = append(parts, fmt.Sprintf("id=%s", payload.CallID))
	}
	if payload.Result != "" {
		parts = append(parts, fmt.Sprintf("result=%s", payload.Result))
	}
	channel := ChannelTool
	label := "tool"
	if payload.Error != "" {
		channel = ChannelError
		label = "error"
		parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
	}
	return Segment{Channel: channel, Label: label, Text: strings.Join(parts, " ")}
}

func renderApprovalEvent(event Event, payload ApprovalEvent) Segment {
	status := "requested"
	switch event.Type {
	case EventTypeApprovalAccepted:
		status = "accepted"
	case EventTypeApprovalDenied:
		status = "denied"
	}
	parts := []string{
		fmt.Sprintf("turn=%d %s", payload.Turn, status),
	}
	if payload.Tool != "" {
		parts = append(parts, fmt.Sprintf("tool=%s", payload.Tool))
	}
	if payload.Mode != "" {
		parts = append(parts, fmt.Sprintf("mode=%s", payload.Mode))
	}
	if payload.Preview != "" {
		parts = append(parts, fmt.Sprintf("args=%s", payload.Preview))
	}
	if payload.Message != "" {
		parts = append(parts, fmt.Sprintf("message=%s", payload.Message))
	}
	return Segment{Channel: ChannelApproval, Label: "approval", Text: strings.Join(parts, " ")}
}

func renderWorkflowHandoffEvent(payload WorkflowHandoffEvent) Segment {
	parts := []string{"workflow handoff"}
	if payload.Decision != "" {
		parts = append(parts, payload.Decision)
	}
	if payload.Next != "" {
		parts = append(parts, fmt.Sprintf("next=%s", payload.Next))
	}
	if payload.Target != "" {
		parts = append(parts, fmt.Sprintf("target=%s", payload.Target))
	}
	if payload.Message != "" {
		parts = append(parts, fmt.Sprintf("message=%s", payload.Message))
	}
	return Segment{Channel: ChannelStatus, Label: "handoff", Text: strings.Join(parts, " ")}
}

func renderStopReasonEvent(payload StopReasonEvent) Segment {
	parts := []string{}
	if payload.Summary != "" {
		parts = append(parts, payload.Summary)
	}
	if payload.Summary == "" && payload.Reason != "" {
		parts = append(parts, "reason="+payload.Reason)
	}
	if payload.Action != "" {
		parts = append(parts, "next: "+payload.Action)
	}
	channel := ChannelStatus
	label := "status"
	if payload.Error != "" {
		channel = ChannelError
		label = "error"
		parts = append(parts, "error: "+payload.Error)
	}
	return Segment{Channel: channel, Label: label, Text: strings.Join(parts, " ")}
}

func renderUserInputEvent(payload UserInputEvent) Segment {
	parts := []string{}
	if payload.Mode != "" {
		parts = append(parts, fmt.Sprintf("mode=%s", payload.Mode))
	}
	if payload.Content != "" {
		parts = append(parts, fmt.Sprintf("content=%s", payload.Content))
	}
	return Segment{Channel: ChannelStatus, Label: "input", Text: strings.Join(parts, " ")}
}

func renderAPIRequestEvent(payload APIRequestEvent) Segment {
	parts := []string{}
	if payload.Model != "" {
		parts = append(parts, fmt.Sprintf("model=%s", payload.Model))
	}
	return Segment{Channel: ChannelStatus, Label: "api", Text: joinOrFallback(parts, "request")}
}

func renderAPIResponseEvent(payload APIResponseEvent) Segment {
	parts := []string{}
	if payload.FinishReason != "" {
		parts = append(parts, fmt.Sprintf("finish=%s", payload.FinishReason))
	}
	channel := ChannelStatus
	label := "api"
	if payload.Error != "" {
		channel = ChannelError
		label = "error"
		parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
	}
	text := joinOrFallback(parts, "response")
	return Segment{Channel: channel, Label: label, Text: text}
}

func renderUnknownEvent(event Event) Segment {
	if event.Type == "" {
		return Segment{}
	}
	if event.Payload == nil {
		return Segment{Channel: ChannelStatus, Label: "status", Text: event.Type}
	}
	return Segment{Channel: ChannelStatus, Label: "status", Text: fmt.Sprintf("%s %s", event.Type, CompactJSON(event.Payload))}
}

// FormatEvent renders an event into the plain-text line shown in stream output.
func FormatEvent(event Event) string {
	return formatSegment(renderEvent(event))
}

func formatSegment(segment Segment) string {
	text := strings.TrimSpace(segment.Text)
	if text == "" {
		return ""
	}
	label := strings.TrimSpace(segment.Label)
	if label == "" {
		return text
	}
	return label + ": " + text
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func joinOrFallback(parts []string, fallback string) string {
	if len(parts) == 0 {
		return fallback
	}
	return strings.Join(parts, " ")
}
