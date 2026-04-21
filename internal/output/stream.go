package output

import (
	"fmt"
	"io"
	"strings"
)

type Stream struct {
	w io.Writer
}

func NewStream(w io.Writer) *Stream {
	return &Stream{w: w}
}

func (s *Stream) Println(args ...any) {
	if s == nil || s.w == nil {
		return
	}
	fmt.Fprintln(s.w, args...)
}

func (s *Stream) Printf(format string, args ...any) {
	if s == nil || s.w == nil {
		return
	}
	fmt.Fprintf(s.w, format, args...)
}

func (s *Stream) Emit(event Event) {
	if s == nil || s.w == nil {
		return
	}
	fmt.Fprintln(s.w, formatEvent(event))
}

func formatEvent(event Event) string {
	switch payload := event.Payload.(type) {
	case ModelCallStartedEvent:
		return fmt.Sprintf("model turn=%d start messages=%d model=%s", payload.Turn, payload.MessageCount, payload.Model)
	case ModelCallFinishedEvent:
		parts := []string{
			fmt.Sprintf("model turn=%d end", payload.Turn),
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
		if payload.Error != "" {
			parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
		}
		return strings.Join(parts, " ")
	case ToolCallStartedEvent:
		parts := []string{
			fmt.Sprintf("tool turn=%d start", payload.Turn),
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
		return strings.Join(parts, " ")
	case ToolCallFinishedEvent:
		parts := []string{
			fmt.Sprintf("tool turn=%d end", payload.Turn),
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
		if payload.Error != "" {
			parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
		}
		return strings.Join(parts, " ")
	case ApprovalEvent:
		status := "requested"
		switch event.Type {
		case EventTypeApprovalAccepted:
			status = "accepted"
		case EventTypeApprovalDenied:
			status = "denied"
		}
		parts := []string{
			fmt.Sprintf("approval turn=%d %s", payload.Turn, status),
		}
		if payload.Tool != "" {
			parts = append(parts, fmt.Sprintf("tool=%s", payload.Tool))
		}
		if payload.Mode != "" {
			parts = append(parts, fmt.Sprintf("mode=%s", payload.Mode))
		}
		if payload.Message != "" {
			parts = append(parts, fmt.Sprintf("message=%s", payload.Message))
		}
		return strings.Join(parts, " ")
	case StopReasonEvent:
		parts := []string{
			fmt.Sprintf("stop reason=%s", payload.Reason),
		}
		if payload.Turn > 0 {
			parts = append(parts, fmt.Sprintf("turn=%d", payload.Turn))
		}
		if payload.Error != "" {
			parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
		}
		return strings.Join(parts, " ")
	case UserInputEvent:
		parts := []string{"user input"}
		if payload.Mode != "" {
			parts = append(parts, fmt.Sprintf("mode=%s", payload.Mode))
		}
		if payload.Content != "" {
			parts = append(parts, fmt.Sprintf("content=%s", payload.Content))
		}
		return strings.Join(parts, " ")
	case APIRequestEvent:
		parts := []string{"api request"}
		if payload.Model != "" {
			parts = append(parts, fmt.Sprintf("model=%s", payload.Model))
		}
		return strings.Join(parts, " ")
	case APIResponseEvent:
		parts := []string{"api response"}
		if payload.FinishReason != "" {
			parts = append(parts, fmt.Sprintf("finish=%s", payload.FinishReason))
		}
		if payload.Error != "" {
			parts = append(parts, fmt.Sprintf("error=%s", payload.Error))
		}
		return strings.Join(parts, " ")
	case ContextDiagnosticsEvent:
		return formatContextDiagnosticsEvent(payload)
	default:
		if event.Type == "" {
			return ""
		}
		if payload == nil {
			return event.Type
		}
		return fmt.Sprintf("%s %s", event.Type, CompactJSON(payload))
	}
}
