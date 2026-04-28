package output

import (
	"fmt"
	"strings"
)

func renderEvent(event Event) Segment {
	switch payload := event.Payload.(type) {
	case RunStartedEvent:
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
	case TurnStartedEvent:
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
	case TurnFinishedEvent:
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
	case AssistantMessageEvent:
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
	case AssistantChunkEvent:
		parts := []string{}
		if payload.Turn > 0 {
			parts = append(parts, fmt.Sprintf("turn=%d", payload.Turn))
		}
		if payload.Content != "" {
			parts = append(parts, fmt.Sprintf("chunk=%s", payload.Content))
		}
		return Segment{Channel: ChannelAssistant, Label: "assistant", Text: strings.Join(parts, " ")}
	case ThinkingChunkEvent:
		parts := []string{}
		if payload.Turn > 0 {
			parts = append(parts, fmt.Sprintf("turn=%d", payload.Turn))
		}
		if payload.Content != "" {
			parts = append(parts, fmt.Sprintf("thinking=%s", payload.Content))
		}
		return Segment{Channel: ChannelStatus, Label: "thinking", Text: strings.Join(parts, " ")}
	case ContextReportEvent:
		return Segment{Channel: ChannelAssistant, Label: "context", Text: payload.Content}
	case ModelCallStartedEvent:
		return Segment{
			Channel: ChannelStatus,
			Label:   "status",
			Text:    fmt.Sprintf("model turn=%d started messages=%d model=%s", payload.Turn, payload.MessageCount, fallback(payload.Model, "unknown")),
		}
	case ModelCallFinishedEvent:
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
	case ToolCallStartedEvent:
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
	case ToolCallFinishedEvent:
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
	case ApprovalEvent:
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
	case StopReasonEvent:
		parts := []string{}
		if payload.Summary != "" {
			parts = append(parts, payload.Summary)
		} else if payload.Reason != "" {
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
	case UserInputEvent:
		parts := []string{}
		if payload.Mode != "" {
			parts = append(parts, fmt.Sprintf("mode=%s", payload.Mode))
		}
		if payload.Content != "" {
			parts = append(parts, fmt.Sprintf("content=%s", payload.Content))
		}
		return Segment{Channel: ChannelStatus, Label: "input", Text: strings.Join(parts, " ")}
	case APIRequestEvent:
		parts := []string{}
		if payload.Model != "" {
			parts = append(parts, fmt.Sprintf("model=%s", payload.Model))
		}
		return Segment{Channel: ChannelStatus, Label: "api", Text: joinOrFallback(parts, "request")}
	case APIResponseEvent:
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
	case ContextDiagnosticsEvent:
		return Segment{Channel: ChannelStatus, Label: "context", Text: formatContextDiagnosticsEvent(payload)}
	default:
		if event.Type == "" {
			return Segment{}
		}
		if event.Payload == nil {
			return Segment{Channel: ChannelStatus, Label: "status", Text: event.Type}
		}
		return Segment{Channel: ChannelStatus, Label: "status", Text: fmt.Sprintf("%s %s", event.Type, CompactJSON(event.Payload))}
	}
}

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
