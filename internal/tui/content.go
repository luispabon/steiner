package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"

	"github.com/luispabon/steiner/internal/output"
)

const markdownRenderWidth = 80

type contentBuffer struct {
	segments     []string
	streaming    bool
	streamBuffer string
	renderer     *glamour.TermRenderer
}

func (b *contentBuffer) AppendEvent(event output.Event) {
	switch event.Type {
	case output.EventTypeAssistantChunk:
		if payload, ok := event.Payload.(output.AssistantChunkEvent); ok {
			b.appendAssistantChunk(payload.Content)
			return
		}
	case output.EventTypeApprovalRequested, output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
		b.finishStreaming()
		b.appendStyled(formatApprovalEvent(event), approvalHighlightStyle)
		return
	case output.EventTypeToolCallStarted, output.EventTypeToolCallFinished:
		b.finishStreaming()
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), toolBlockStyle)
		return
	case output.EventTypeModelCallStarted, output.EventTypeModelCallFinished,
		output.EventTypeContextDiagnostics:
		b.finishStreaming()
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), thinkingBlockStyle)
		return
	case output.EventTypeStopReason:
		b.finishStreaming()
		b.appendLine(formatStopReasonEvent(event))
		return
	case output.EventTypeAssistantMessage,
		output.EventTypeRunStarted, output.EventTypeRunFinished,
		output.EventTypeTurnStarted, output.EventTypeTurnFinished,
		output.EventTypeAPIRequest, output.EventTypeAPIResponse,
		output.EventTypeUserInput:
		return
	}

	b.finishStreaming()
	line := strings.TrimSpace(output.FormatEvent(event))
	if shouldSuppressLine(line) {
		return
	}
	b.appendLine(line)
}

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

func (b *contentBuffer) AppendLine(line string) {
	b.finishStreaming()
	b.appendLine(line)
}

func (b *contentBuffer) Clear() {
	b.segments = nil
	b.streamBuffer = ""
	b.streaming = false
}

func (b *contentBuffer) String() string {
	parts := make([]string, 0, len(b.segments)+1)
	parts = append(parts, b.segments...)
	if preview := b.inProgressPreview(); preview != "" {
		parts = append(parts, preview)
	}
	return strings.Join(parts, "\n")
}

func (b *contentBuffer) appendAssistantChunk(text string) {
	if text == "" {
		return
	}
	b.streaming = true
	b.streamBuffer += text
	b.flushCompletedBlocks()
}

func (b *contentBuffer) finishStreaming() {
	if !b.streaming {
		return
	}
	if strings.TrimSpace(b.streamBuffer) != "" {
		b.appendMarkdownBlock(b.streamBuffer)
	}
	b.streamBuffer = ""
	b.streaming = false
}

func (b *contentBuffer) inProgressPreview() string {
	preview := strings.TrimRight(b.streamBuffer, "\n")
	if strings.TrimSpace(preview) == "" {
		return ""
	}
	return assistantProseStyle.Render(preview)
}

func (b *contentBuffer) flushCompletedBlocks() {
	for {
		block, rest, ok := nextCompleteMarkdownBlock(b.streamBuffer)
		if !ok {
			return
		}
		b.appendMarkdownBlock(block)
		b.streamBuffer = rest
	}
}

func nextCompleteMarkdownBlock(buffer string) (string, string, bool) {
	if buffer == "" {
		return "", "", false
	}

	if end, ok := completeFencedBlockEnd(buffer); ok {
		return buffer[:end], buffer[end:], true
	}

	if idx := strings.Index(buffer, "\n\n"); idx >= 0 {
		end := idx + 2
		for end < len(buffer) && buffer[end] == '\n' {
			end++
		}
		return buffer[:end], buffer[end:], true
	}

	line, rest, ok := cutFirstLine(buffer)
	if !ok {
		return "", "", false
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return line, rest, true
	}
	if isStandaloneMarkdownLine(trimmed) && strings.HasSuffix(line, "\n") {
		return line, rest, true
	}
	return "", "", false
}

func completeFencedBlockEnd(buffer string) (int, bool) {
	line, _, ok := cutFirstLine(buffer)
	if !ok {
		return 0, false
	}
	fence := fenceDelimiter(strings.TrimSpace(line))
	if fence == "" {
		return 0, false
	}

	offset := len(line)
	for offset < len(buffer) {
		nextLine, _, ok := cutFirstLine(buffer[offset:])
		if !ok {
			return 0, false
		}
		offset += len(nextLine)
		if matchesFence(strings.TrimSpace(nextLine), fence) {
			for offset < len(buffer) && buffer[offset] == '\n' {
				offset++
			}
			return offset, true
		}
	}
	return 0, false
}

func cutFirstLine(text string) (string, string, bool) {
	if text == "" {
		return "", "", false
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx+1], text[idx+1:], true
	}
	return text, "", true
}

func fenceDelimiter(line string) string {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```"
	case strings.HasPrefix(line, "~~~"):
		return "~~~"
	default:
		return ""
	}
}

func matchesFence(line, fence string) bool {
	return fence != "" && strings.HasPrefix(line, fence)
}

func isStandaloneMarkdownLine(line string) bool {
	if strings.HasPrefix(line, "#") {
		return true
	}
	if strings.HasPrefix(line, "> ") {
		return true
	}
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ ") {
		return true
	}
	for i := 1; i < len(line); i++ {
		if line[i] < '0' || line[i] > '9' {
			return line[i] == '.' && i+1 < len(line) && line[i+1] == ' '
		}
	}
	return false
}

func (b *contentBuffer) appendMarkdownBlock(block string) {
	block = strings.TrimSpace(block)
	if block == "" {
		return
	}
	rendered := strings.TrimSpace(b.renderMarkdown(block))
	if rendered == "" {
		rendered = assistantProseStyle.Render(block)
	}
	b.segments = append(b.segments, rendered)
}

func (b *contentBuffer) renderMarkdown(block string) string {
	renderer := b.markdownRenderer()
	if renderer == nil {
		return assistantProseStyle.Render(block)
	}
	rendered, err := renderer.Render(block)
	if err != nil {
		return assistantProseStyle.Render(block)
	}
	return rendered
}

func (b *contentBuffer) markdownRenderer() *glamour.TermRenderer {
	if b.renderer != nil {
		return b.renderer
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(markdownRenderWidth),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return nil
	}
	b.renderer = renderer
	return renderer
}

func (b *contentBuffer) appendLine(line string) {
	if shouldSuppressLine(line) {
		return
	}
	b.segments = append(b.segments, line)
}

func (b *contentBuffer) appendStyled(line string, style interface{ Render(...string) string }) {
	if shouldSuppressLine(line) {
		return
	}
	b.segments = append(b.segments, style.Render(line))
}
