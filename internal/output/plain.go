package output

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Channel identifies a rendered output stream.
type Channel string

const (
	// ChannelAssistant renders assistant text.
	ChannelAssistant Channel = "assistant"
	// ChannelStatus renders status text.
	ChannelStatus Channel = "status"
	// ChannelTool renders tool execution text.
	ChannelTool Channel = "tool"
	// ChannelApproval renders approval prompts and decisions.
	ChannelApproval Channel = "approval"
	// ChannelError renders error text.
	ChannelError Channel = "error"
)

// Segment is a single rendered output unit.
type Segment struct {
	Channel   Channel
	Label     string
	Text      string
	Streaming bool
}

// ThemeStyle controls ANSI prefix and suffix decoration for a channel.
type ThemeStyle struct {
	LabelPrefix string
	LabelSuffix string
}

// Theme defines the channel styles used by PlainRenderer.
type Theme struct {
	Enabled   bool
	Assistant ThemeStyle
	Status    ThemeStyle
	Tool      ThemeStyle
	Approval  ThemeStyle
	Error     ThemeStyle
}

// PlainRenderer writes terminal-friendly plain text output.
type PlainRenderer struct {
	mu        sync.Mutex
	w         io.Writer
	theme     Theme
	streaming Channel
	toolCalls map[string]retainedToolCall
	err       error
}

type retainedToolCall struct {
	tool      string
	arguments map[string]any
}

// StreamOption configures a PlainRenderer.
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

// WithTheme overrides the renderer theme.
func WithTheme(theme Theme) StreamOption {
	return func(renderer *PlainRenderer) {
		if renderer != nil {
			renderer.theme = theme
		}
	}
}

// Println writes a newline-terminated line to the renderer output.
func (r *PlainRenderer) Println(args ...any) {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	if !r.finishStreamingLocked() {
		return
	}
	r.writeStringLocked(fmt.Sprintln(args...))
}

// Printf writes formatted text to the renderer output.
func (r *PlainRenderer) Printf(format string, args ...any) {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	r.writeStringLocked(fmt.Sprintf(format, args...))
}

// Render writes a formatted segment.
func (r *PlainRenderer) Render(segment Segment) {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	r.renderLocked(segment)
}

// OnEvent renders a structured event.
func (r *PlainRenderer) OnEvent(event Event) {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	r.onEventLocked(event)
}

// WriteAssistant writes a complete assistant response.
func (r *PlainRenderer) WriteAssistant(text string) {
	r.Render(Segment{Channel: ChannelAssistant, Text: text})
}

// WriteAssistantChunk appends a streaming assistant chunk.
func (r *PlainRenderer) WriteAssistantChunk(text string) {
	r.Render(Segment{Channel: ChannelAssistant, Text: text, Streaming: true})
}

// FinishAssistant closes any active assistant streaming line.
func (r *PlainRenderer) FinishAssistant() {
	if r == nil || r.w == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	if r.streaming == ChannelAssistant {
		r.finishStreamingLocked()
	}
}

// Err reports the first write error observed by the renderer.
func (r *PlainRenderer) Err() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *PlainRenderer) renderLocked(segment Segment) {
	if segment.Channel == "" {
		segment.Channel = ChannelStatus
	}
	if segment.Streaming {
		r.renderStreamingLocked(segment)
		return
	}
	if !r.finishStreamingLocked() {
		return
	}
	line := formatSegment(segment)
	if line == "" {
		return
	}
	r.writeStringLocked(r.decorate(segment.Channel, line) + "\n")
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

	preview := BuildToolPreview(state.tool, state.arguments, payload.Result)
	doc, ok := previewDocumentForToolPayload(preview)
	if !ok {
		return ToolPreview{}, PreviewDocument{}, false
	}
	return preview, doc, true
}
func (r *PlainRenderer) renderStreamingLocked(segment Segment) {
	if r.streaming != "" && r.streaming != segment.Channel {
		if !r.finishStreamingLocked() {
			return
		}
	}
	if r.streaming == "" {
		label := segment.Label
		if label == "" {
			label = string(segment.Channel)
		}
		if !r.writeStringLocked(r.decorate(segment.Channel, label+"> ")) {
			return
		}
		r.streaming = segment.Channel
	}
	r.writeStringLocked(segment.Text)
}

func (r *PlainRenderer) finishStreamingLocked() bool {
	if r.streaming == "" {
		return true
	}
	if !r.writeStringLocked("\n") {
		return false
	}
	r.streaming = ""
	return true
}

func (r *PlainRenderer) writeStringLocked(text string) bool {
	if r.err != nil {
		return false
	}
	if _, err := io.WriteString(r.w, text); err != nil {
		r.err = err
		return false
	}
	return true
}

func (r *PlainRenderer) decorate(channel Channel, text string) string {
	style := r.theme.style(channel)
	if !r.theme.Enabled {
		return text
	}
	return style.LabelPrefix + text + style.LabelSuffix
}

// Themed decorates text for the given channel without writing it.
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
