package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

const markdownRenderPadding = 4

func shouldSuppressLine(line string) bool {
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return true
	}
	if strings.HasPrefix(line, "status: run") {
		return true
	}
	if strings.HasPrefix(line, "api:") {
		return true
	}
	if strings.HasPrefix(line, "turn ") {
		return true
	}
	return false
}

func formatStopReasonEvent(event output.Event) string {
	payload, ok := event.Payload.(output.StopReasonEvent)
	if !ok {
		return ""
	}
	if payload.Error != "" {
		return "error: " + payload.Error
	}
	if payload.Reason != "" && payload.Reason != "complete" && payload.Reason != "max_turns" && payload.Reason != "max_tokens" {
		return "status: " + payload.Reason
	}
	return ""
}

func formatApprovalEvent(event output.Event) string {
	if payload, ok := event.Payload.(output.ApprovalEvent); ok {
		parts := []string{"approval"}
		if payload.Tool != "" {
			parts = append(parts, payload.Tool)
		}
		if payload.Mode != "" {
			parts = append(parts, payload.Mode)
		}
		switch event.Type {
		case output.EventTypeApprovalRequested:
			parts = append(parts, "(yes/no)")
		case output.EventTypeApprovalAccepted:
			parts = append(parts, "accepted")
		case output.EventTypeApprovalDenied:
			parts = append(parts, "denied")
		}
		return strings.Join(parts, " ")
	}
	return "approval requested"
}

func formatDelegationEvent(event output.Event) string {
	switch event.Type {
	case output.EventTypeDelegationStarted:
		if payload, ok := event.Payload.(output.DelegationStartedEvent); ok {
			return "delegate: starting " + payload.AgentID
		}
		return "delegate: starting"
	case output.EventTypeDelegationComplete:
		if payload, ok := event.Payload.(output.DelegationCompleteEvent); ok {
			summary := "delegate: complete " + payload.AgentID + " (" + pluralTurns(payload.TurnCount)
			if payload.ToolCallCount > 0 {
				summary += ", " + pluralToolCalls(payload.ToolCallCount)
			}
			summary += ")"
			return summary
		}
		return "delegate: complete"
	case output.EventTypeDelegationFailed:
		if payload, ok := event.Payload.(output.DelegationFailedEvent); ok {
			return "delegate: failed " + payload.AgentID
		}
		return "delegate: failed"
	default:
		return "delegate: " + event.Type
	}
}

func pluralTurns(count int) string {
	if count == 1 {
		return "1 turn"
	}
	return fmt.Sprintf("%d turns", count)
}

func pluralToolCalls(count int) string {
	if count == 1 {
		return "1 tool call"
	}
	return fmt.Sprintf("%d tool calls", count)
}

func (b *contentBuffer) appendLine(line string) {
	if shouldSuppressLine(line) {
		return
	}
	b.segments = append(b.segments, contentSegment{kind: segmentPlain, text: line, renderDirty: true})
}

func (b *contentBuffer) appendStyled(line string, kind contentSegmentKind) {
	if shouldSuppressLine(line) {
		return
	}
	b.segments = append(b.segments, contentSegment{kind: kind, text: line, renderDirty: true})
}
