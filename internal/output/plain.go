package output

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
)

type Channel string

const (
	ChannelAssistant Channel = "assistant"
	ChannelStatus    Channel = "status"
	ChannelTool      Channel = "tool"
	ChannelApproval  Channel = "approval"
	ChannelError     Channel = "error"
)

type Segment struct {
	Channel   Channel
	Label     string
	Text      string
	Streaming bool
}

type ThemeStyle struct {
	LabelPrefix string
	LabelSuffix string
}

type Theme struct {
	Enabled   bool
	Assistant ThemeStyle
	Status    ThemeStyle
	Tool      ThemeStyle
	Approval  ThemeStyle
	Error     ThemeStyle
}

type PlainRenderer struct {
	mu        sync.Mutex
	w         io.Writer
	theme     Theme
	streaming Channel
	toolCalls map[string]retainedToolCall
}

type retainedToolCall struct {
	tool                        string
	arguments                   map[string]any
	writeTargetExistedBefore    bool
	hasWriteTargetExistedBefore bool
}

type StreamOption func(*PlainRenderer)

// NewPlainRenderer creates a new plain-text renderer that writes to w.
func NewPlainRenderer(w io.Writer, options ...StreamOption) *PlainRenderer {
	renderer := &PlainRenderer{
		w:     w,
		theme: defaultTheme(w),
	}
	for _, option := range options {
		if option != nil {
			option(renderer)
		}
	}
	return renderer
}

func WithTheme(theme Theme) StreamOption {
	return func(renderer *PlainRenderer) {
		if renderer != nil {
			renderer.theme = theme
		}
	}
}

func (r *PlainRenderer) Println(args ...any) {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishStreamingLocked()
	fmt.Fprintln(r.w, args...)
}

func (r *PlainRenderer) Printf(format string, args ...any) {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.w, format, args...)
}

func (r *PlainRenderer) Render(segment Segment) {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renderLocked(segment)
}

func (r *PlainRenderer) OnEvent(event Event) {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onEventLocked(event)
}

func (r *PlainRenderer) WriteAssistant(text string) {
	r.Render(Segment{Channel: ChannelAssistant, Text: text})
}

func (r *PlainRenderer) WriteAssistantChunk(text string) {
	r.Render(Segment{Channel: ChannelAssistant, Text: text, Streaming: true})
}

func (r *PlainRenderer) FinishAssistant() {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streaming == ChannelAssistant {
		r.finishStreamingLocked()
	}
}

func (r *PlainRenderer) renderLocked(segment Segment) {
	if segment.Channel == "" {
		segment.Channel = ChannelStatus
	}
	if segment.Streaming {
		r.renderStreamingLocked(segment)
		return
	}
	r.finishStreamingLocked()
	line := formatSegment(segment)
	if line == "" {
		return
	}
	fmt.Fprintln(r.w, r.decorate(segment.Channel, line))
}

func (r *PlainRenderer) onEventLocked(event Event) {
	switch payload := event.Payload.(type) {
	case ToolCallStartedEvent:
		r.rememberToolCallLocked(payload)
	case ToolCallFinishedEvent:
		defer r.forgetToolCallLocked(payload.CallID)
	}

	segment := renderEvent(event)
	if segment.Channel != "" || segment.Label != "" || strings.TrimSpace(segment.Text) != "" {
		r.renderLocked(segment)
	}

	if payload, ok := event.Payload.(ToolCallFinishedEvent); ok && payload.Error == "" {
		if preview, doc, ok := r.previewRenderDataLocked(payload); ok {
			r.renderPreviewDocumentLocked(preview, doc)
		}
	}
	if payload, ok := event.Payload.(DisplayFilePayload); ok {
		r.renderDisplayFilePreviewLocked(payload)
	}
}

func (r *PlainRenderer) rememberToolCallLocked(payload ToolCallStartedEvent) {
	if r == nil || payload.CallID == "" {
		return
	}
	if r.toolCalls == nil {
		r.toolCalls = make(map[string]retainedToolCall)
	}
	state := retainedToolCall{
		tool:      payload.Tool,
		arguments: cloneMap(payload.Arguments),
	}
	if payload.WriteTargetExistedBefore != nil {
		state.writeTargetExistedBefore = *payload.WriteTargetExistedBefore
		state.hasWriteTargetExistedBefore = true
	}
	r.toolCalls[payload.CallID] = state
}

func (r *PlainRenderer) forgetToolCallLocked(callID string) {
	if r == nil || callID == "" || len(r.toolCalls) == 0 {
		return
	}
	delete(r.toolCalls, callID)
}

func (r *PlainRenderer) previewRenderDataLocked(payload ToolCallFinishedEvent) (ToolPreview, PreviewDocument, bool) {
	if payload.Error != "" {
		return ToolPreview{}, PreviewDocument{}, false
	}
	if payload.Preview.Kind != ToolPreviewKindPlain {
		if doc, ok := previewDocumentForToolPayload(payload.Preview); ok {
			return payload.Preview, doc, true
		}
	}

	state, ok := r.toolCalls[payload.CallID]
	if !ok {
		return ToolPreview{}, PreviewDocument{}, false
	}

	var existedBefore *bool
	if state.hasWriteTargetExistedBefore {
		existed := state.writeTargetExistedBefore
		existedBefore = &existed
	}
	preview := BuildToolPreview(state.tool, state.arguments, payload.Result, existedBefore)
	doc, ok := previewDocumentForToolPayload(preview)
	if !ok {
		return ToolPreview{}, PreviewDocument{}, false
	}
	return preview, doc, true
}

func previewDocumentForToolPayload(preview ToolPreview) (PreviewDocument, bool) {
	switch preview.Kind {
	case ToolPreviewKindEditDiff:
		return FormatEditDiffPreview(preview.Path, preview.Before, preview.After), true
	case ToolPreviewKindFileWrite:
		return FormatFilePreview(preview.Path, preview.Contents), true
	case ToolPreviewKindReadFile:
		return FormatFilePreview(preview.Path, normalizeReadPreviewContents(preview.Contents, 0)), true
	default:
		return PreviewDocument{}, false
	}
}

func normalizeReadPreviewContents(contents string, startLine int) string {
	if contents == "" {
		return contents
	}

	lines := strings.Split(contents, "\n")
	if len(lines) == 0 {
		return contents
	}

	if startLine > 0 {
		current := startLine
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if !hasReadLineNumberPrefixForLine(line, current) {
				return contents
			}
			current++
		}
		return stripReadPreviewContents(lines, startLine)
	}

	hasNumberedPrefix := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !hasReadLineNumberPrefix(line) {
			return contents
		}
		hasNumberedPrefix = true
	}
	if !hasNumberedPrefix {
		return contents
	}
	return stripReadPreviewContents(lines, 1)
}

func stripReadPreviewContents(lines []string, startLine int) string {
	out := make([]string, len(lines))
	current := startLine
	for i, line := range lines {
		out[i] = stripReadLineNumberPrefixForLine(line, current)
		current++
	}
	return strings.Join(out, "\n")
}

func hasReadLineNumberPrefix(line string) bool {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	start := i
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == start {
		return false
	}
	return i < len(line) && (line[i] == ' ' || line[i] == '\t')
}

func hasReadLineNumberPrefixForLine(line string, lineNumber int) bool {
	prefix := strconv.Itoa(lineNumber)
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if !strings.HasPrefix(line[i:], prefix) {
		return false
	}
	i += len(prefix)
	return i == len(line) || line[i] == ' ' || line[i] == '\t'
}

func stripReadLineNumberPrefixForLine(line string, lineNumber int) string {
	prefix := strconv.Itoa(lineNumber)
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if strings.HasPrefix(line[i:], prefix) {
		i += len(prefix)
	}
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[i:]
}

func (r *PlainRenderer) renderDisplayFilePreviewLocked(payload DisplayFilePayload) {
	doc := payload.Preview
	if doc.Kind != PreviewFormatKindFile {
		return
	}

	if caption := renderDisplayFileCaption(payload); caption != "" {
		fmt.Fprintln(r.w, r.decorate(ChannelTool, "  "+caption))
	}
	for _, line := range doc.Lines {
		text := renderPreviewLineText(line)
		if text == "" {
			continue
		}
		fmt.Fprintln(r.w, r.decorate(renderPreviewChannel(line), "  "+text))
	}
	if doc.Truncated {
		truncation := fmt.Sprintf("  output truncated after %d lines", doc.LineLimit)
		fmt.Fprintln(r.w, r.decorate(ChannelStatus, truncation))
	}
}

func renderDisplayFileCaption(payload DisplayFilePayload) string {
	label := "display file preview"
	if payload.Preview.Language != "" && payload.Preview.Language != "plain" {
		label = fmt.Sprintf("%s · %s", label, payload.Preview.Language)
	}
	if payload.Path != "" {
		return fmt.Sprintf("%s · %s · %d lines", payload.Path, label, previewDocumentLineCount(payload.Preview))
	}
	return fmt.Sprintf("%s · %d lines", label, previewDocumentLineCount(payload.Preview))
}

func (r *PlainRenderer) renderPreviewDocumentLocked(preview ToolPreview, doc PreviewDocument) {
	if doc.Kind == "" {
		return
	}
	if caption := renderPreviewCaption(preview, doc); caption != "" {
		fmt.Fprintln(r.w, r.decorate(ChannelStatus, "  "+caption))
	}
	for _, line := range doc.Lines {
		if doc.Kind == PreviewFormatKindEditDiff && line.Kind == PreviewLineKindHeader {
			switch strings.TrimSpace(line.Prefix) {
			case "---", "+++":
				continue
			}
		}
		if text := renderPreviewLineText(line); text != "" {
			fmt.Fprintln(r.w, r.decorate(renderPreviewChannel(line), "  "+text))
		}
	}
}

func (r *PlainRenderer) renderStreamingLocked(segment Segment) {
	if r.streaming != "" && r.streaming != segment.Channel {
		r.finishStreamingLocked()
	}
	if r.streaming == "" {
		label := segment.Label
		if label == "" {
			label = string(segment.Channel)
		}
		_, _ = io.WriteString(r.w, r.decorate(segment.Channel, label+"> "))
		r.streaming = segment.Channel
	}
	_, _ = io.WriteString(r.w, segment.Text)
}

func (r *PlainRenderer) finishStreamingLocked() {
	if r.streaming == "" {
		return
	}
	_, _ = io.WriteString(r.w, "\n")
	r.streaming = ""
}

func (r *PlainRenderer) decorate(channel Channel, text string) string {
	style := r.theme.style(channel)
	if !r.theme.Enabled {
		return text
	}
	return style.LabelPrefix + text + style.LabelSuffix
}

func (r *PlainRenderer) Themed(channel Channel, text string) string {
	return r.decorate(channel, text)
}

func defaultTheme(w io.Writer) Theme {
	enabled := supportsANSI(w)
	return Theme{
		Enabled:   enabled,
		Assistant: themeStyle(enabled, "1;38;5;159"),
		Status:    themeStyle(enabled, "38;5;110"),
		Tool:      themeStyle(enabled, "38;5;221"),
		Approval:  themeStyle(enabled, "1;38;5;151"),
		Error:     themeStyle(enabled, "1;38;5;203"),
	}
}

func themeStyle(enabled bool, code string) ThemeStyle {
	if !enabled {
		return ThemeStyle{}
	}
	return ThemeStyle{
		LabelPrefix: "\x1b[" + code + "m",
		LabelSuffix: "\x1b[0m",
	}
}

func (t Theme) style(channel Channel) ThemeStyle {
	switch channel {
	case ChannelAssistant:
		return t.Assistant
	case ChannelTool:
		return t.Tool
	case ChannelApproval:
		return t.Approval
	case ChannelError:
		return t.Error
	case ChannelStatus:
		fallthrough
	default:
		return t.Status
	}
}

func supportsANSI(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
