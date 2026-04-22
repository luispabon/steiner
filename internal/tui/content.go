package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

type contentBuffer struct {
	lines     []string
	streaming bool
}

func (b *contentBuffer) AppendEvent(event output.Event) {
	switch event.Type {
	case output.EventTypeAssistantChunk:
		if payload, ok := event.Payload.(output.AssistantChunkEvent); ok {
			b.appendAssistantChunk(payload.Content)
			return
		}
	case output.EventTypeStopReason:
		b.finishStreaming()
		if line := formatTUIEvent(event); line != "" {
			b.lines = append(b.lines, line)
			return
		}
	case output.EventTypeApprovalRequested:
		b.finishStreaming()
		b.lines = append(b.lines, formatApprovalEvent(event))
		return
	case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied,
		output.EventTypeAssistantMessage,
		output.EventTypeRunStarted, output.EventTypeRunFinished,
		output.EventTypeTurnStarted, output.EventTypeTurnFinished,
		output.EventTypeModelCallStarted, output.EventTypeModelCallFinished,
		output.EventTypeToolCallStarted, output.EventTypeToolCallFinished,
		output.EventTypeContextDiagnostics, output.EventTypeAPIRequest,
		output.EventTypeAPIResponse, output.EventTypeUserInput:
		return
	default:
		if _, ok := event.Payload.(output.StopReasonEvent); ok {
			b.finishStreaming()
			if line := formatTUIEvent(event); line != "" {
				b.lines = append(b.lines, line)
			}
			return
		}
		if _, ok := event.Payload.(output.RunFinishedEvent); ok {
			return
		}
		if _, ok := event.Payload.(output.TurnFinishedEvent); ok {
			return
		}
	}
	b.finishStreaming()
	if line := strings.TrimSpace(output.FormatEvent(event)); line != "" {
		b.lines = append(b.lines, line)
	}
}

func formatTUIEvent(event output.Event) string {
	switch event.Type {
	case output.EventTypeRunFinished, output.EventTypeTurnFinished:
		return ""
	}
	text := strings.TrimSpace(output.FormatEvent(event))
	if strings.HasPrefix(text, "status: run") {
		return ""
	}
	switch payload := event.Payload.(type) {
	case output.StopReasonEvent:
		if payload.Error != "" {
			return "error: " + payload.Error
		}
		if payload.Reason != "" && payload.Reason != "complete" && payload.Reason != "max_turns" && payload.Reason != "max_tokens" {
			return "status: " + payload.Reason
		}
		return ""
	case output.RunFinishedEvent:
		return ""
	case output.AssistantMessageEvent:
		return ""
	}
	if event.Type == output.EventTypeStopReason {
		return ""
	}
	return ""
}

func formatApprovalEvent(event output.Event) string {
	if payload, ok := event.Payload.(output.ApprovalEvent); ok {
		return "approval: " + payload.Tool + " " + payload.Mode + " (yes/no)"
	}
	return "approval requested"
}

func (b *contentBuffer) AppendLine(line string) {
	b.finishStreaming()
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	b.lines = append(b.lines, line)
}

func (b *contentBuffer) Clear() {
	b.lines = nil
	b.streaming = false
}

func (b *contentBuffer) String() string {
	if len(b.lines) == 0 {
		return ""
	}
	return strings.Join(b.lines, "\n")
}

func (b *contentBuffer) appendAssistantChunk(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if !b.streaming || len(b.lines) == 0 {
		b.lines = append(b.lines, "assistant> "+text)
		b.streaming = true
		return
	}
	b.lines[len(b.lines)-1] += text
}

func (b *contentBuffer) finishStreaming() {
	b.streaming = false
}
