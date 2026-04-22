package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const markdownRenderPadding = 4

type contentSegmentKind int

const (
	segmentPlain contentSegmentKind = iota
	segmentAssistantProse
	segmentAssistantMarkdown
	segmentApproval
	segmentTool
	segmentThinking
)

type contentSegment struct {
	kind contentSegmentKind
	text string
}

type contentBuffer struct {
	segments          []contentSegment
	streaming         bool
	streamBuffer      string
	renderer          *glamour.TermRenderer
	renderWidth       int
	styles            theme.Styles
	glamourStyleSheet glamour.TermRendererOption
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
		b.appendStyled(formatApprovalEvent(event), segmentApproval)
		return
	case output.EventTypeToolCallStarted, output.EventTypeToolCallFinished:
		b.finishStreaming()
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
		return
	case output.EventTypeModelCallStarted, output.EventTypeModelCallFinished,
		output.EventTypeContextDiagnostics:
		b.finishStreaming()
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentThinking)
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

func (b *contentBuffer) String(width int) string {
	parts := make([]string, 0, len(b.segments)+1)
	for _, segment := range b.segments {
		if rendered := b.renderSegment(segment, width); rendered != "" {
			parts = append(parts, rendered)
		}
	}
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
	return b.styles.AssistantProse.Render("assistant> " + preview)
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
	if isMarkdownLikeBlock(block) {
		b.segments = append(b.segments, contentSegment{kind: segmentAssistantMarkdown, text: block})
		return
	}
	b.segments = append(b.segments, contentSegment{kind: segmentAssistantProse, text: block})
}

func (b *contentBuffer) renderSegment(segment contentSegment, width int) string {
	switch segment.kind {
	case segmentAssistantMarkdown:
		rendered := strings.TrimSpace(b.renderMarkdown(segment.text, width))
		if rendered != "" {
			return rendered
		}
		return b.styles.AssistantProse.Render("assistant> " + segment.text)
	case segmentAssistantProse:
		return b.styles.AssistantProse.Render("assistant> " + segment.text)
	case segmentApproval:
		return b.styles.ApprovalHighlight.Render(segment.text)
	case segmentTool:
		return b.styles.ToolBlock.Render(segment.text)
	case segmentThinking:
		return b.styles.ThinkingBlock.Render(segment.text)
	default:
		return segment.text
	}
}

func (b *contentBuffer) renderMarkdown(block string, width int) string {
	renderer := b.markdownRenderer(width)
	if renderer == nil {
		return b.styles.AssistantProse.Render("assistant> " + block)
	}
	rendered, err := renderer.Render(block)
	if err != nil {
		return b.styles.AssistantProse.Render("assistant> " + block)
	}
	return rendered
}

func (b *contentBuffer) markdownRenderer(width int) *glamour.TermRenderer {
	renderWidth := maxInt(1, width-markdownRenderPadding)
	if b.renderer != nil && b.renderWidth == renderWidth {
		return b.renderer
	}
	opts := []glamour.TermRendererOption{
		glamour.WithWordWrap(renderWidth),
		glamour.WithPreservedNewLines(),
	}
	if b.glamourStyleSheet != nil {
		opts = append([]glamour.TermRendererOption{b.glamourStyleSheet}, opts...)
	} else {
		opts = append([]glamour.TermRendererOption{glamour.WithStandardStyle("dark")}, opts...)
	}
	renderer, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return nil
	}
	b.renderer = renderer
	b.renderWidth = renderWidth
	return renderer
}

func (b *contentBuffer) appendLine(line string) {
	if shouldSuppressLine(line) {
		return
	}
	b.segments = append(b.segments, contentSegment{kind: segmentPlain, text: line})
}

func (b *contentBuffer) appendStyled(line string, kind contentSegmentKind) {
	if shouldSuppressLine(line) {
		return
	}
	b.segments = append(b.segments, contentSegment{kind: kind, text: line})
}

func isMarkdownLikeBlock(block string) bool {
	trimmed := strings.TrimSpace(block)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "```") || strings.Contains(trimmed, "~~~") {
		return true
	}
	if strings.Contains(trimmed, "\n#") || strings.HasPrefix(trimmed, "#") {
		return true
	}
	if strings.Contains(trimmed, "\n- ") || strings.Contains(trimmed, "\n* ") || strings.Contains(trimmed, "\n+ ") {
		return true
	}
	if strings.Contains(trimmed, "\n|") || strings.Contains(trimmed, "|") {
		return true
	}
	if strings.Contains(trimmed, "`") || strings.Contains(trimmed, "**") || strings.Contains(trimmed, "__") || strings.Contains(trimmed, "_") {
		return true
	}
	return false
}
