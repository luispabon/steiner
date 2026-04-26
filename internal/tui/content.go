package tui

import (
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

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
	segmentUser
	segmentThinkingBlock
	segmentToolCall
	segmentApprovalPill
	segmentCompactionBanner
	segmentInterrupted
)

type thinkingBlockData struct {
	preview   string // first 80 chars
	collapsed bool   // default true
	body      string // full content
	streaming bool   // true while chunks are still arriving
}

type toolCallSegment struct {
	tool                     string // "bash", "read", "write", "edit", "glob", "grep", "todo", etc.
	args                     string // summarized args, ~60 chars max
	meta                     string // "exit 0", "184 lines", etc.
	bodyKind                 string // "bash", "diff", "file", "plain"
	body                     string // raw result text
	callID                   string // for matching started→finished
	collapsed                bool   // default true
	hasError                 bool   // set when ToolCallFinished carries an error
	rawArgs                  map[string]any
	writeTargetExistedBefore *bool
	preview                  output.ToolPreview
}

type approvalPillData struct {
	tool     string
	mode     string
	preview  string
	resolved bool
	accepted bool
}

type compactionBannerData struct {
	label    string
	subtitle string
	finished bool
	summary  string
	progress float64 // 0.0-1.0 fill ratio for in-progress bar (if known)
	pct      int     // percentage label for in-progress (if known)
	msgCount int     // number of messages compacted (for finished summary)
}

type contentSegment struct {
	kind           contentSegmentKind
	text           string
	thinkData      *thinkingBlockData    // non-nil only for segmentThinkingBlock
	toolData       *toolCallSegment      // non-nil only for segmentToolCall
	approvalData   *approvalPillData     // non-nil only for segmentApprovalPill
	compactionData *compactionBannerData // non-nil only for segmentCompactionBanner
}

type contentBuffer struct {
	segments          []contentSegment
	streaming         bool
	hadChunks         bool
	streamBuffer      string
	renderer          *glamour.TermRenderer
	renderWidth       int
	styles            theme.Styles
	glamourStyleSheet glamour.TermRendererOption
	collapseState     map[int]bool // segment index → collapsed (for tool calls and thinking)
	segmentHeights    []int        // rendered line count per segment (recomputed in String())
	showThinking      bool         // from prefs; when false skip thinking segments
	inCompaction      bool         // when true skip thinking chunks from compaction
	streamingPhase    string       // "thinking" | "tool" | "answer" | ""
	tickCount         int          // incremented by 500ms tick, used for cursor blink
}

func (b *contentBuffer) AppendEvent(event output.Event) {
	switch event.Type {
	case output.EventTypeThinkingChunk:
		if b.inCompaction {
			return
		}
		if payload, ok := event.Payload.(output.ThinkingChunkEvent); ok {
			b.appendThinkingChunk(payload.Content)
			return
		}
	case output.EventTypeAssistantChunk:
		if b.inCompaction {
			return
		}
		if payload, ok := event.Payload.(output.AssistantChunkEvent); ok {
			b.finalizeThinkingBlock()
			b.appendAssistantChunk(payload.Content)
			return
		}
	case output.EventTypeApprovalRequested:
		b.finishStreaming()
		if payload, ok := event.Payload.(output.ApprovalEvent); ok {
			seg := contentSegment{
				kind: segmentApprovalPill,
				approvalData: &approvalPillData{
					tool:    payload.Tool,
					mode:    payload.Mode,
					preview: payload.Preview,
				},
			}
			b.segments = append(b.segments, seg)
		} else {
			b.appendStyled(formatApprovalEvent(event), segmentApproval)
		}
		return
	case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
		b.finishStreaming()
		// Find the last unresolved approval pill and mark it resolved
		accepted := event.Type == output.EventTypeApprovalAccepted
		for i := len(b.segments) - 1; i >= 0; i-- {
			if b.segments[i].kind == segmentApprovalPill && b.segments[i].approvalData != nil {
				if !b.segments[i].approvalData.resolved {
					b.segments[i].approvalData.resolved = true
					b.segments[i].approvalData.accepted = accepted
					return
				}
			}
		}
		// Fallback
		b.appendStyled(formatApprovalEvent(event), segmentApproval)
		return
	case output.EventTypeDelegationStarted, output.EventTypeDelegationComplete, output.EventTypeDelegationFailed:
		b.finishStreaming()
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	case output.EventTypeToolCallStarted:
		b.finishStreaming()
		b.streamingPhase = "tool"
		if payload, ok := event.Payload.(output.ToolCallStartedEvent); ok {
			tc := &toolCallSegment{
				tool:                     strings.ToLower(payload.Tool),
				args:                     summarizeArgs(payload.Tool, payload.Arguments),
				callID:                   payload.CallID,
				collapsed:                true,
				rawArgs:                  cloneToolArguments(payload.Arguments),
				writeTargetExistedBefore: payload.WriteTargetExistedBefore,
			}
			b.segments = append(b.segments, contentSegment{
				kind:     segmentToolCall,
				toolData: tc,
			})
		} else {
			b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
		}
		return

	case output.EventTypeToolCallFinished:
		b.finishStreaming()
		if payload, ok := event.Payload.(output.ToolCallFinishedEvent); ok {
			// Find the last segmentToolCall with matching callID (or just last tool call)
			for i := len(b.segments) - 1; i >= 0; i-- {
				if b.segments[i].kind == segmentToolCall && b.segments[i].toolData != nil {
					td := b.segments[i].toolData
					if td.callID == "" || td.callID == payload.CallID {
						td.body = payload.Result
						td.meta = formatToolMeta(payload)
						td.hasError = payload.Error != ""
						td.preview = payload.Preview
						if td.preview.Kind == "" {
							td.preview = output.BuildToolPreview(td.tool, td.rawArgs, payload.Result, td.writeTargetExistedBefore)
						}
						if td.preview.Kind != output.ToolPreviewKindPlain {
							td.bodyKind = previewBodyKind(td.tool, td.preview)
						} else {
							td.bodyKind = inferBodyKind(td.tool, payload.Result)
						}
						break
					}
				}
			}
		} else {
			b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
		}
		return
	case output.EventTypeStopReason:
		b.finishStreaming()
		b.appendLine(formatStopReasonEvent(event))
		return
	case output.EventTypeAssistantMessage:
		if b.inCompaction {
			return
		}
		if payload, ok := event.Payload.(output.AssistantMessageEvent); ok && payload.Content != "" && !b.hadChunks {
			b.finishStreaming()
			b.appendMarkdownBlock(payload.Content)
		}
		b.hadChunks = false
		return
	case output.EventTypeContextReport:
		if payload, ok := event.Payload.(output.ContextReportEvent); ok && strings.TrimSpace(payload.Content) != "" {
			b.finishStreaming()
			b.appendMarkdownBlock(payload.Content)
		}
		return
	case output.EventTypeModelCallStarted, output.EventTypeModelCallFinished,
		output.EventTypeContextDiagnostics:
		b.finishStreaming()
		if payload, ok := event.Payload.(output.ContextDiagnosticsEvent); ok {
			switch payload.Kind {
			case "compaction":
				if payload.Severity == "compacting" {
					b.segments = append(b.segments, contentSegment{
						kind: segmentCompactionBanner,
						compactionData: &compactionBannerData{
							label:    "Compacting",
							subtitle: "summarizing context",
							finished: false,
						},
					})
				} else {
					msgCount := payload.CompactedMessages
					var summary string
					switch {
					case payload.SummaryTitle != "":
						summary = payload.SummaryTitle
					case msgCount > 0:
						summary = fmt.Sprintf("%d messages summarized into 1", msgCount)
					case payload.CompactedTurns > 0:
						summary = fmt.Sprintf("%d turns summarized", payload.CompactedTurns)
					default:
						summary = "context compacted"
					}
					b.segments = append(b.segments, contentSegment{
						kind: segmentCompactionBanner,
						compactionData: &compactionBannerData{
							label:    "Context compacted",
							subtitle: summary,
							finished: true,
							summary:  summary,
							msgCount: msgCount,
						},
					})
				}
			case "session_health":
				if b.inCompaction {
					return
				}
				b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentThinking)
			}
		}
		return
	case output.EventTypeUserInput:
		if payload, ok := event.Payload.(output.UserInputEvent); ok && strings.TrimSpace(payload.Content) != "" {
			b.segments = append(b.segments, contentSegment{kind: segmentUser, text: payload.Content})
			if len(b.segments)-1 >= 0 {
				b.collapseState[len(b.segments)-1] = false // user segments never collapsed
			}
		}
		return
	case output.EventTypeRunStarted, output.EventTypeRunFinished,
		output.EventTypeTurnStarted, output.EventTypeTurnFinished,
		output.EventTypeAPIRequest, output.EventTypeAPIResponse:
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

func formatDelegationEvent(event output.Event) string {
	switch event.Type {
	case output.EventTypeDelegationStarted:
		if payload, ok := event.Payload.(output.DelegationStartedEvent); ok {
			return "delegate: starting " + payload.AgentID
		}
		return "delegate: starting"
	case output.EventTypeDelegationComplete:
		if payload, ok := event.Payload.(output.DelegationCompleteEvent); ok {
			return "delegate: complete " + payload.AgentID + " (" + pluralTurns(payload.TurnCount) + ")"
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

func (b *contentBuffer) AppendLine(line string) {
	b.finishStreaming()
	b.appendLine(line)
}

func (b *contentBuffer) AppendUser(text string) {
	b.finishStreaming()
	idx := len(b.segments)
	b.segments = append(b.segments, contentSegment{kind: segmentUser, text: text})
	b.collapseState[idx] = false
}

func (b *contentBuffer) Clear() {
	b.segments = nil
	b.segmentHeights = nil
	b.streamBuffer = ""
	b.streaming = false
	b.streamingPhase = ""
	b.collapseState = make(map[int]bool)
}

func (b *contentBuffer) String(width int) string {
	b.segmentHeights = make([]int, len(b.segments))
	parts := make([]string, 0, len(b.segments)+2)
	for i, segment := range b.segments {
		if segment.kind == segmentThinkingBlock && !b.showThinking {
			b.segmentHeights[i] = 0
			continue
		}
		rendered := b.renderSegment(segment, width)
		b.segmentHeights[i] = strings.Count(rendered, "\n")
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}
	if preview := b.inProgressPreview(); preview != "" {
		parts = append(parts, preview)
	}
	parts = append(parts, b.streamingIndicatorView())
	return strings.Join(parts, "")
}

// liveThinkingSegment returns the thinkingBlockData of the last segment if it is
// a thinking block that is still receiving streamed chunks, or nil otherwise.
func (b *contentBuffer) liveThinkingSegment() *thinkingBlockData {
	if len(b.segments) == 0 {
		return nil
	}
	last := &b.segments[len(b.segments)-1]
	if last.kind == segmentThinkingBlock && last.thinkData != nil && last.thinkData.streaming {
		return last.thinkData
	}
	return nil
}

func (b *contentBuffer) appendThinkingChunk(text string) {
	if text == "" {
		return
	}
	b.streaming = true
	b.hadChunks = true
	b.streamingPhase = "thinking"

	if td := b.liveThinkingSegment(); td != nil {
		td.body += text
		runes := []rune(td.body)
		if len(runes) > 80 {
			td.preview = string(runes[:80])
		} else {
			td.preview = td.body
		}
	} else {
		runes := []rune(text)
		preview := string(runes)
		if len(runes) > 80 {
			preview = string(runes[:80])
		}
		idx := len(b.segments)
		b.segments = append(b.segments, contentSegment{
			kind: segmentThinkingBlock,
			thinkData: &thinkingBlockData{
				preview:   preview,
				collapsed: true,
				streaming: true,
				body:      text,
			},
		})
		b.collapseState[idx] = true
	}
}

func (b *contentBuffer) finalizeThinkingBlock() {
	if td := b.liveThinkingSegment(); td != nil {
		td.streaming = false
	}
}

func (b *contentBuffer) appendAssistantChunk(text string) {
	if text == "" {
		return
	}
	b.streaming = true
	b.hadChunks = true
	b.streamBuffer += text
	if b.streamingPhase == "" || b.streamingPhase == "thinking" {
		b.streamingPhase = "answer"
	}

	// Disabled for now because it mangles Glamour rendering
	// @todo investigate and fix, don't delete the commented out code!
	// b.flushCompletedBlocks()
}

func (b *contentBuffer) finishStreaming() {
	if !b.streaming {
		return
	}
	b.finalizeThinkingBlock()
	if strings.TrimSpace(b.streamBuffer) != "" {
		b.appendMarkdownBlock(b.streamBuffer)
	}
	b.streamBuffer = ""
	b.streaming = false
	b.streamingPhase = ""
}

func (b *contentBuffer) inProgressPreview() string {
	preview := strings.TrimRight(b.streamBuffer, "\n")
	if strings.TrimSpace(preview) == "" {
		return ""
	}
	cursor := ""
	if b.tickCount%2 == 0 {
		cursor = "█"
	}
	return b.styles.AssistantProse.Render(preview+cursor) + "\n"
}

func (b *contentBuffer) streamingIndicatorView() string {
	if b.streamingPhase == "" {
		return ""
	}
	label := "thinking…"
	if b.streamingPhase == "tool" {
		label = "running tool…"
	}
	activeDot := b.tickCount % 3
	dots := make([]string, 3)
	for i := range dots {
		if i == activeDot {
			dots[i] = b.styles.Accent.Render("·")
		} else {
			dots[i] = b.styles.FgMute.Render("·")
		}
	}
	return strings.Join(dots, "  ") + "  " + b.styles.FgMute.Render(label) + "\n"
}

func (b *contentBuffer) AppendInterrupted() {
	b.finishStreaming()
	b.segments = append(b.segments, contentSegment{kind: segmentInterrupted})
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
		rendered := b.renderMarkdown(segment.text, width)
		if strings.TrimSpace(rendered) != "" {
			return strings.TrimRight(rendered, "\n") + "\n\n"
		}
		return b.styles.AssistantProse.Render(segment.text) + "\n\n"
	case segmentAssistantProse:
		return b.styles.AssistantProse.Render(segment.text) + "\n\n"
	case segmentApproval:
		return b.styles.ApprovalHighlight.Render(segment.text) + "\n"
	case segmentTool:
		return b.styles.ToolBlock.Render(segment.text) + "\n"
	case segmentThinking:
		return b.styles.ThinkingBlock.Render(segment.text) + "\n"
	case segmentUser:
		lines := strings.Split(strings.TrimRight(segment.text, "\n"), "\n")
		contentWidth := width - 1
		if contentWidth < 2 {
			contentWidth = 2
		}
		bar := b.styles.UserBar.Render("│")
		pad := bar + b.styles.UserBg.Width(contentWidth).Render("")
		var sb strings.Builder
		sb.WriteString(pad + "\n")
		textWidth := contentWidth - 3 // 2 left + 1 right padding
		if textWidth < 1 {
			textWidth = 1
		}
		for _, line := range lines {
			// Wrap text at textWidth without bg to get visual lines, then render each with bg+indent
			wrapped := lipgloss.NewStyle().Width(textWidth).Render(line)
			for _, vl := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
				vl = strings.TrimRight(vl, " ")
				content := b.styles.UserBg.Width(contentWidth).Render("  " + vl)
				sb.WriteString(bar + content + "\n")
			}
		}
		sb.WriteString(pad + "\n")
		sb.WriteString("\n")
		return sb.String()
	case segmentThinkingBlock:
		if segment.thinkData == nil {
			return ""
		}
		td := segment.thinkData
		if td.collapsed {
			runes := []rune(td.preview)
			if len(runes) > 60 {
				runes = runes[:60]
			}
			return b.styles.ThinkingBar.Render("▸ Thinking · "+string(runes)+"…") + "\n"
		}
		var sb strings.Builder
		sb.WriteString(b.styles.ThinkingBar.Render("▾ Thinking") + "\n")
		bar := b.styles.ThinkingBar.Render("▎")
		for _, line := range strings.Split(strings.TrimRight(td.body, "\n"), "\n") {
			sb.WriteString(bar + " " + b.styles.ThinkingBar.Render(line) + "\n")
		}
		return sb.String()
	case segmentToolCall:
		if segment.toolData == nil {
			return ""
		}
		return b.renderToolCall(segment.toolData, width)
	case segmentApprovalPill:
		if segment.approvalData == nil {
			return ""
		}
		return b.renderApprovalPill(segment.approvalData, width)
	case segmentCompactionBanner:
		if segment.compactionData == nil {
			return ""
		}
		return b.renderCompactionBanner(segment.compactionData, width)
	case segmentInterrupted:
		return b.styles.FgMute.Render("interrupted") + "\n\n"
	default:
		return b.styles.AssistantProse.Render(segment.text) + "\n"
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

// summarizeArgs extracts a human-readable summary from tool arguments
func summarizeArgs(tool string, args map[string]any) string {
	if args == nil {
		return tool
	}
	// Try common arg keys in order
	for _, key := range []string{"command", "path", "file_path", "pattern", "query", "description"} {
		if v, ok := args[key]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 60 {
				s = s[:57] + "..."
			}
			return s
		}
	}
	// Fallback: first value
	for _, v := range args {
		s := fmt.Sprintf("%v", v)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return s
	}
	return tool
}

// formatToolMeta builds the right-aligned meta string for a finished tool call
func formatToolMeta(payload output.ToolCallFinishedEvent) string {
	if payload.Error != "" {
		return "error"
	}
	lines := strings.Count(payload.Result, "\n") + 1
	if strings.TrimSpace(payload.Result) == "" {
		lines = 0
	}
	if lines > 0 {
		return fmt.Sprintf("%d lines", lines)
	}
	return "done"
}

// previewBodyKind determines how to render the tool body using structured preview data first.
func previewBodyKind(tool string, preview output.ToolPreview) string {
	switch preview.Kind {
	case output.ToolPreviewKindEditDiff:
		return "diff"
	case output.ToolPreviewKindFileWrite, output.ToolPreviewKindReadFile:
		return "file"
	case output.ToolPreviewKindPlain:
		if strings.EqualFold(strings.TrimSpace(tool), "bash") {
			return "bash"
		}
		return "plain"
	default:
		if strings.EqualFold(strings.TrimSpace(tool), "bash") {
			return "bash"
		}
		return "plain"
	}
}

// inferBodyKind determines how to render the tool body when only the raw result is available.
func inferBodyKind(tool, body string) string {
	switch tool {
	case "bash":
		return "bash"
	case "read", "read_file":
		return "file"
	case "edit", "write", "write_file":
		if strings.HasPrefix(strings.TrimSpace(body), "@@") || strings.Contains(body, "\n@@") {
			return "diff"
		}
		return "plain"
	default:
		return "plain"
	}
}

func (b *contentBuffer) renderToolCall(tc *toolCallSegment, width int) string {
	chevron := b.styles.FgMute.Render("▸")
	if !tc.collapsed {
		chevron = b.styles.FgMute.Render("▾")
	}

	tagStyle := b.toolTagStyle(tc.tool)
	tag := tagStyle.Render(" " + tc.tool + " ")

	args := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Render(tc.args)
	header := chevron + " " + tag + " " + args
	if tc.meta != "" {
		metaStr := b.styles.FgMute.Render(tc.meta)
		headerWidth := width - lipgloss.Width(metaStr) - 2
		if headerWidth < 1 {
			headerWidth = 1
		}
		header = lipgloss.NewStyle().Width(headerWidth).Render(header) + " " + metaStr
	}

	result := header + "\n"
	if !tc.collapsed {
		result += b.renderToolBody(tc, width)
	}
	return result
}

func (b *contentBuffer) toolTagStyle(tool string) lipgloss.Style {
	switch tool {
	case "bash":
		return b.styles.ToolTagBash
	case "read", "read_file":
		return b.styles.ToolTagRead
	case "write", "write_file", "edit":
		return b.styles.ToolTagWrite
	case "glob", "grep":
		return b.styles.ToolTagGlobGrep
	case "todo", "todowrite", "todoread":
		return b.styles.ToolTagTodo
	default:
		return b.styles.ToolTagDefault
	}
}

func (b *contentBuffer) renderToolBody(tc *toolCallSegment, width int) string {
	const (
		indentStr = "   "
		maxRows   = 20
	)
	rowWidth := width - len(indentStr)
	if rowWidth < 10 {
		rowWidth = 10
	}

	var lines []string
	switch tc.bodyKind {
	case "bash":
		lines = b.buildBashLines(tc)
	case "diff":
		lines = b.buildDiffPreviewLines(tc, rowWidth-lipgloss.Width(indentStr))
	case "file":
		lines = b.buildFilePreviewLines(tc, rowWidth-lipgloss.Width(indentStr))
	default:
		lines = b.buildPlainLines(tc)
	}

	truncated := false
	if len(lines) > maxRows {
		lines = lines[:maxRows]
		truncated = true
	}

	bg := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Width(rowWidth)
	padRow := indentStr + bg.Render("") + "\n"

	var sb strings.Builder
	sb.WriteString(padRow)
	for _, l := range lines {
		sb.WriteString(indentStr + bg.Render(" "+l) + "\n")
	}
	if truncated {
		sb.WriteString(indentStr + bg.Render(" "+b.styles.FgMute.Render("↓ more")) + "\n")
	}
	sb.WriteString(padRow)
	return sb.String()
}

func (b *contentBuffer) buildBashLines(tc *toolCallSegment) []string {
	var lines []string
	dollar := b.styles.Accent.Render("$")
	lines = append(lines, dollar+" "+tc.args)
	if strings.TrimSpace(tc.body) != "" {
		fgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
		for _, l := range strings.Split(strings.TrimRight(tc.body, "\n"), "\n") {
			lines = append(lines, fgStyle.Render(l))
		}
	}
	if tc.hasError {
		lines = append(lines, b.styles.Removed.Render("exit 1"))
	} else {
		lines = append(lines, b.styles.Added.Render("exit 0"))
	}
	return lines
}

func (b *contentBuffer) buildPlainLines(tc *toolCallSegment) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(tc.body, "\n"), "\n") {
		lines = append(lines, b.styles.FgDim.Render(l))
	}
	return lines
}

func (b *contentBuffer) buildFilePreviewLines(tc *toolCallSegment, width int) []string {
	doc := b.previewDocument(tc)
	if doc.Kind == "" {
		return b.buildPlainLines(tc)
	}

	lines := make([]string, 0, len(doc.Lines)+1)
	lines = append(lines, b.renderFileCaption(tc, doc))
	lines = append(lines, b.renderFilePreviewDocument(doc)...)
	if doc.Truncated {
		lines = append(lines, b.styles.FgMute.Render("… output truncated"))
	}
	return lines
}

func (b *contentBuffer) buildDiffPreviewLines(tc *toolCallSegment, width int) []string {
	doc := b.previewDocument(tc)
	if doc.Kind != output.PreviewFormatKindEditDiff {
		return b.buildPlainLines(tc)
	}

	lines := make([]string, 0, len(doc.Lines)+2)
	lines = append(lines, b.renderDiffHeader(doc, width))
	lines = append(lines, b.renderDiffPreviewDocument(doc, width)...)
	if doc.Truncated {
		lines = append(lines, b.styles.FgMute.Render("… output truncated"))
	}
	return lines
}

func (b *contentBuffer) previewDocument(tc *toolCallSegment) output.PreviewDocument {
	switch tc.preview.Kind {
	case output.ToolPreviewKindEditDiff:
		return output.FormatEditDiffPreview(tc.preview.Path, tc.preview.Before, tc.preview.After)
	case output.ToolPreviewKindFileWrite:
		return output.FormatFilePreview(tc.preview.Path, tc.preview.Contents)
	case output.ToolPreviewKindReadFile:
		return output.FormatFilePreview(tc.preview.Path, tc.preview.Contents)
	default:
		return output.PreviewDocument{}
	}
}

func (b *contentBuffer) renderFileCaption(tc *toolCallSegment, doc output.PreviewDocument) string {
	label := "file preview"
	switch tc.preview.Kind {
	case output.ToolPreviewKindFileWrite:
		if tc.preview.Created {
			label = "new file preview"
		} else {
			label = "updated file contents preview"
		}
	case output.ToolPreviewKindReadFile:
		label = "read file preview"
	}
	lineCount := previewContentLineCount(doc)
	if doc.Path != "" {
		return b.styles.FgMute.Render(fmt.Sprintf("%s · %s · %d lines", doc.Path, label, lineCount))
	}
	return b.styles.FgMute.Render(fmt.Sprintf("%s · %d lines", label, lineCount))
}

func (b *contentBuffer) renderFilePreviewDocument(doc output.PreviewDocument) []string {
	lines := make([]string, 0, len(doc.Lines))
	for i, line := range doc.Lines {
		if line.Kind == output.PreviewLineKindTruncated {
			lines = append(lines, b.renderPreviewLine(line))
			continue
		}
		gutter := b.styles.FgMute.Render(fmt.Sprintf("%4d ", i+1))
		lines = append(lines, gutter+b.renderPreviewLine(line))
	}
	return lines
}

func (b *contentBuffer) renderDiffHeader(doc output.PreviewDocument, width int) string {
	added, removed := countPreviewChanges(doc)
	tag := b.styles.ToolTagWrite.Render(" [edit] ")
	path := b.baseTextStyle().Render(doc.Path)
	metrics := b.styles.Added.Render(fmt.Sprintf("+%d", added)) + " " + b.styles.Removed.Render(fmt.Sprintf("-%d", removed))
	header := tag + " " + path
	available := width - lipgloss.Width(metrics) - 1
	if available < 1 {
		available = 1
	}
	header = lipgloss.NewStyle().Width(available).Render(header)
	return b.styles.BgElev2.Render(header + " " + metrics)
}

func (b *contentBuffer) renderDiffPreviewDocument(doc output.PreviewDocument, width int) []string {
	lines := make([]string, 0, len(doc.Lines))
	oldLine, newLine := 1, 1
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", maxInt(1, width)))
	for _, line := range doc.Lines {
		switch line.Kind {
		case output.PreviewLineKindHeader:
			if strings.TrimSpace(line.Prefix) == "@@" {
				lines = append(lines, rule)
			}
		case output.PreviewLineKindContext:
			lines = append(lines, b.renderDiffRow(line, oldLine, " ", theme.Bg))
			oldLine++
			newLine++
		case output.PreviewLineKindRemoved:
			lines = append(lines, b.renderDiffRow(line, oldLine, "-", theme.DiffRemovedBg))
			oldLine++
		case output.PreviewLineKindAdded:
			lines = append(lines, b.renderDiffRow(line, newLine, "+", theme.DiffAddedBg))
			newLine++
		case output.PreviewLineKindTruncated:
			lines = append(lines, b.styles.FgMute.Render("… output truncated"))
		default:
			lines = append(lines, b.renderPreviewLine(line))
		}
	}
	return lines
}

func (b *contentBuffer) renderDiffRow(line output.PreviewLine, lineNo int, sign string, bgColor string) string {
	lineNoStr := b.styles.FgMute.Render(fmt.Sprintf("%4d", lineNo))
	var signStr string
	switch sign {
	case "+":
		signStr = b.styles.Added.Render("+")
	case "-":
		signStr = b.styles.Removed.Render("-")
	default:
		signStr = b.styles.FgMute.Render(" ")
	}
	var raw strings.Builder
	for _, span := range line.Spans {
		raw.WriteString(span.Text)
	}
	rawText := raw.String()
	var body string
	if sign == " " {
		body = b.styles.FgDim.Render(rawText)
	} else {
		body = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Render(rawText)
	}
	bg := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
	return bg.Render(lineNoStr + " " + signStr + " " + body)
}

func (b *contentBuffer) renderPreviewLine(line output.PreviewLine) string {
	var sb strings.Builder
	for _, span := range line.Spans {
		sb.WriteString(b.renderPreviewSpan(span))
	}
	text := sb.String()
	if text == "" {
		return b.styles.FgDim.Render("")
	}
	switch line.Kind {
	case output.PreviewLineKindHeader:
		return b.styles.FgMute.Render(text)
	case output.PreviewLineKindContext:
		return b.baseTextStyle().Render(text)
	case output.PreviewLineKindAdded:
		return b.styles.Added.Render(text)
	case output.PreviewLineKindRemoved:
		return b.styles.Removed.Render(text)
	case output.PreviewLineKindTruncated:
		return b.styles.Warn.Render(text)
	default:
		return text
	}
}

func (b *contentBuffer) renderPreviewSpan(span output.PreviewSpan) string {
	style := b.previewTokenStyle(span.Type)
	return style.Render(span.Text)
}

func (b *contentBuffer) previewTokenStyle(token chroma.TokenType) lipgloss.Style {
	switch {
	case token.InCategory(chroma.Comment):
		return b.styles.FgMute
	case token.InCategory(chroma.Keyword):
		return b.styles.Accent
	case token.InCategory(chroma.Name) && token.InSubCategory(chroma.NameFunction):
		return b.styles.UserBar
	case token.InCategory(chroma.Name) && token.InSubCategory(chroma.NameClass):
		return b.styles.Accent
	case token.InCategory(chroma.Name) && token.InSubCategory(chroma.NameAttribute):
		return b.styles.Accent
	case token.InCategory(chroma.LiteralString):
		return b.styles.Added
	case token.InCategory(chroma.LiteralNumber):
		return b.styles.Warn
	case token.InCategory(chroma.Operator):
		return b.styles.FgFaint
	case token.InCategory(chroma.Punctuation):
		return b.styles.FgMute
	case token.InCategory(chroma.GenericDeleted):
		return b.styles.Removed
	case token.InCategory(chroma.GenericInserted):
		return b.styles.Added
	default:
		return b.baseTextStyle()
	}
}

func countPreviewChanges(doc output.PreviewDocument) (int, int) {
	added := 0
	removed := 0
	for _, line := range doc.Lines {
		switch line.Kind {
		case output.PreviewLineKindAdded:
			added++
		case output.PreviewLineKindRemoved:
			removed++
		}
	}
	return added, removed
}

func cloneToolArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = value
	}
	return cloned
}

func previewContentLineCount(doc output.PreviewDocument) int {
	count := len(doc.Lines)
	if count == 0 {
		return 0
	}
	last := doc.Lines[count-1]
	if last.Kind == output.PreviewLineKindTruncated {
		count--
	}
	return count
}

func (b *contentBuffer) baseTextStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
}

func (b *contentBuffer) renderApprovalPill(ad *approvalPillData, width int) string {
	const indent = "   "
	innerWidth := width - len(indent)
	if innerWidth < 10 {
		innerWidth = 10
	}

	label := "approval required"
	if ad.tool != "" {
		label = ad.tool
		if ad.mode != "" {
			label += " · " + ad.mode
		}
	}

	if ad.resolved {
		bar := b.styles.FgMute.Render("│")
		var statusStr string
		if ad.accepted {
			statusStr = b.styles.Added.Render("✓ approved")
		} else {
			statusStr = b.styles.Removed.Render("✗ denied")
		}
		statusW := lipgloss.Width(statusStr)
		// bar(1) + space(1) + question + space(1) + status = innerWidth
		qW := innerWidth - 3 - statusW
		if qW < 0 {
			qW = 0
		}
		question := lipgloss.NewStyle().Width(qW).Render(b.styles.FgDim.Render(label))
		return indent + bar + " " + question + " " + statusStr + "\n"
	}

	// Unresolved: accent bar + bg-elev background + question in fg + styled buttons
	bar := b.styles.Accent.Render("│")

	accentSoft := lipgloss.Color(theme.AccentSoft)
	bgElev2C := lipgloss.Color(theme.BgElev2)
	fgMuteC := lipgloss.Color(theme.FgMute)
	fgDimC := lipgloss.Color(theme.FgDim)
	accentC := lipgloss.Color(theme.AccentAmber)

	btnApprove := lipgloss.NewStyle().Background(accentSoft).Foreground(fgMuteC).Render("[y]") +
		lipgloss.NewStyle().Background(accentSoft).Foreground(accentC).Render(" approve")
	btnDeny := lipgloss.NewStyle().Background(bgElev2C).Foreground(fgMuteC).Render("[n]") +
		lipgloss.NewStyle().Background(bgElev2C).Foreground(fgDimC).Render(" deny")
	btnAlways := lipgloss.NewStyle().Background(bgElev2C).Foreground(fgMuteC).Render("[a]") +
		lipgloss.NewStyle().Background(bgElev2C).Foreground(fgDimC).Render(" always")
	buttons := btnApprove + " " + btnDeny + " " + btnAlways
	buttonsW := lipgloss.Width(buttons)

	// bg-elev region: innerWidth - 1 (bar)
	bgW := innerWidth - 1
	// space(1) + question + space(1) + buttons fills bgW
	qW := bgW - 2 - buttonsW
	if qW < 0 {
		qW = 0
	}

	question := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Foreground(lipgloss.Color(theme.Fg)).
		Width(qW).Render(label)

	bgContent := " " + question + " " + buttons
	bgRow := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Width(bgW).Render(bgContent)

	return indent + bar + bgRow + "\n"
}

func (b *contentBuffer) renderCompactionBanner(cd *compactionBannerData, width int) string {
	if cd.finished {
		var note string
		switch {
		case cd.msgCount > 0:
			note = fmt.Sprintf("Context compacted. %d messages summarized into 1.", cd.msgCount)
		case cd.summary != "":
			note = "Context compacted. " + cd.summary + "."
		default:
			note = "Context compacted."
		}
		return b.styles.FgMute.Render(note) + "\n"
	}

	// In-progress banner: warn left bar + bg-elev row, full transcript width
	if width < 10 {
		width = 10
	}
	bar := b.styles.Warn.Render("│")
	bgW := width - 1

	compacting := b.styles.Warn.Render(" Compacting")
	subtitle := b.styles.FgDim.Render(" " + cd.subtitle + " ")

	// Show pct% only when real progress data is known
	pctStr := ""
	if cd.pct > 0 {
		pctStr = fmt.Sprintf("%d%% ", cd.pct)
	}
	pctLabel := b.styles.FgDim.Render(pctStr)

	fixedW := lipgloss.Width(compacting) + lipgloss.Width(subtitle) + lipgloss.Width(pctLabel)
	barW := bgW - fixedW
	if barW < 0 {
		barW = 0
	}

	// Use real progress if known; otherwise animate a sweep using tickCount
	var filled int
	if cd.progress > 0 {
		filled = int(float64(barW) * cd.progress)
	} else if barW > 0 {
		// Sweep 0→barW and repeat; each tick = 500ms
		filled = b.tickCount % (barW + 1)
	}
	if filled > barW {
		filled = barW
	}
	filledBar := b.styles.Warn.Render(strings.Repeat("█", filled))
	emptyBar := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev2)).Render(strings.Repeat(" ", barW-filled))
	progressBar := filledBar + emptyBar

	rowContent := compacting + subtitle + progressBar + pctLabel
	row := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Width(bgW).Render(rowContent)

	return bar + row + "\n"
}
