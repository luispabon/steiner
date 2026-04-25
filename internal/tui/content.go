package tui

import (
	"fmt"
	"strings"

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
)

type thinkingBlockData struct {
	preview   string // first 80 chars
	collapsed bool   // default true
	body      string // full content
}

type toolCallSegment struct {
	tool                     string // "bash", "read", "write", "edit", "glob", "grep", "todo", etc.
	args                     string // summarized args, ~60 chars max
	meta                     string // "exit 0", "184 lines", etc.
	bodyKind                 string // "bash", "read", "plain"
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
	streamingPhase    string       // "thinking" | "tool" | "answer" | ""
	tickCount         int          // incremented by 500ms tick, used for cursor blink
}

func (b *contentBuffer) AppendEvent(event output.Event) {
	switch event.Type {
	case output.EventTypeAssistantChunk:
		if payload, ok := event.Payload.(output.AssistantChunkEvent); ok {
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
						td.bodyKind = previewBodyKind(td.tool, td.preview)
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
				// Replace with compaction banner
				summary := ""
				if payload.SummaryTitle != "" {
					summary = payload.SummaryTitle
				} else {
					summary = fmt.Sprintf("compacted %d turns → %d retained", payload.CompactedTurns, payload.RetainedTurns)
				}
				b.segments = append(b.segments, contentSegment{
					kind: segmentCompactionBanner,
					compactionData: &compactionBannerData{
						label:    "Context compacted",
						subtitle: summary,
						finished: true,
						summary:  summary,
					},
				})
			case "session_health":
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

func (b *contentBuffer) appendAssistantChunk(text string) {
	if text == "" {
		return
	}
	b.streaming = true
	b.hadChunks = true
	b.streamBuffer += text
	if b.streamingPhase == "" {
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
	dots := []string{"•", "•", "•"}
	active := b.tickCount % 3
	label := "thinking…"
	if b.streamingPhase == "tool" {
		label = "running tool…"
	}
	// stagger dot brightness by making active dot FgDim colored (style as accent)
	// For terminal, simplify: just show dots + label, varying count by tickCount
	visibleDots := active + 1
	return b.styles.FgMute.Render(strings.Join(dots[:visibleDots], " ")+" "+label) + "\n"
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
		// Check collapse state by finding segment index
		// Use a simple approach: render based on thinkData.collapsed field
		td := segment.thinkData
		collapsed := td.collapsed
		if collapsed {
			preview := td.preview
			if len(preview) > 80 {
				preview = preview[:80]
			}
			return b.styles.ThinkingBar.Render("▸ Thinking · "+preview+"…") + "\n"
		}
		bar := b.styles.ThinkingBar.Render("│")
		body := b.styles.FgDim.Render(td.body)
		return bar + " " + body + "\n"
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

// previewBodyKind determines how to render the tool body without inferring edit/write previews from result text.
func previewBodyKind(tool string, preview output.ToolPreview) string {
	switch tool {
	case "bash":
		return "bash"
	}
	switch preview.Kind {
	case output.ToolPreviewKindReadFile:
		return "read"
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
	if !tc.collapsed && tc.body != "" {
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
	contentWidth := rowWidth - 2 // 1 cell padding each side
	if contentWidth < 8 {
		contentWidth = 8
	}

	lines := b.buildBodyLines(tc, contentWidth)
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

func (b *contentBuffer) buildBodyLines(tc *toolCallSegment, width int) []string {
	switch tc.bodyKind {
	case "bash":
		return b.buildBashLines(tc)
	case "read":
		return b.buildReadLines(tc)
	default:
		return b.buildPlainLines(tc)
	}
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

func (b *contentBuffer) buildReadLines(tc *toolCallSegment) []string {
	var lines []string
	bodyLines := strings.Split(strings.TrimRight(tc.body, "\n"), "\n")
	caption := tc.args + " · " + fmt.Sprintf("%d lines", len(bodyLines))
	lines = append(lines, b.styles.FgMute.Render(caption))
	fgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	for i, l := range bodyLines {
		gutter := b.styles.FgMute.Render(fmt.Sprintf("%4d ", i+1))
		lines = append(lines, gutter+fgStyle.Render(l))
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

func (b *contentBuffer) renderApprovalPill(ad *approvalPillData, width int) string {
	if ad.resolved {
		// Dashed resolved state
		status := "✗ denied"
		if ad.accepted {
			status = "✓ approved"
		}
		label := ad.tool
		if ad.mode != "" {
			label += " · " + ad.mode
		}
		line := b.styles.FgDim.Render(label + " — " + status)
		// Approximate dashed border with · chars
		dash := strings.Repeat("·", maxInt(0, width-4))
		return b.styles.FgFaint.Render(dash) + "\n" + "  " + line + "\n"
	}

	// Unresolved: left accent bar + content
	bar := b.styles.AccentLine.Render("│")

	// Build question text
	label := "approval required"
	if ad.tool != "" {
		label = ad.tool
		if ad.mode != "" {
			label += " · " + ad.mode
		}
	}

	// Buttons right-aligned
	buttons := b.styles.FgMute.Render("[y]") + " approve  " +
		b.styles.FgMute.Render("[n]") + " deny  " +
		b.styles.FgMute.Render("[a]") + " always"

	contentWidth := width - 3 // account for bar + space
	if contentWidth < 10 {
		contentWidth = 10
	}
	questionLine := lipgloss.NewStyle().Width(contentWidth-lipgloss.Width(buttons)).Render(label) + buttons

	return bar + " " + questionLine + "\n"
}

func (b *contentBuffer) renderCompactionBanner(cd *compactionBannerData, width int) string {
	if cd.finished {
		// Italic system note
		note := "✦ " + cd.summary
		return b.styles.FgDim.Italic(true).Render(note) + "\n"
	}
	// In-progress banner (not currently triggered, but render it anyway)
	bar := strings.Repeat("─", maxInt(0, width-2))
	header := b.styles.Warn.Render("Compacting") + " " + b.styles.FgDim.Render(cd.subtitle)
	return b.styles.FgMute.Render(bar) + "\n" + header + "\n"
}
