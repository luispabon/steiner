package output

import (
	"fmt"
	"io"
	"os"
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
}

type StreamOption func(*PlainRenderer)

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
	r.Render(renderEvent(event))
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

type InspectionSnapshot struct {
	TotalDiagnostics   int
	ContextDiagnostics int
	LastStopReason     string
	LastBudget         string
	LastCompaction     string
	Recent             []string
	RecentContext      []string
}

func SummarizeInspection(events []Event, recentLimit int) InspectionSnapshot {
	if recentLimit < 0 {
		recentLimit = 0
	}

	summary := InspectionSnapshot{
		TotalDiagnostics: len(events),
	}
	if len(events) == 0 {
		return summary
	}

	recent := make([]string, 0, minInt(len(events), recentLimit))
	recentContext := make([]string, 0, minInt(len(events), recentLimit))

	for _, event := range events {
		if line := FormatEvent(event); strings.TrimSpace(line) != "" {
			recent = appendRecentLine(recent, line, recentLimit)
			if isContextDiagnosticEvent(event) {
				recentContext = appendRecentLine(recentContext, line, recentLimit)
			}
		}

		switch payload := event.Payload.(type) {
		case StopReasonEvent:
			if segment := renderEvent(event); strings.TrimSpace(segment.Text) != "" {
				summary.LastStopReason = segment.Text
			}
		case ContextDiagnosticsEvent:
			summary.ContextDiagnostics++
			segment := renderEvent(event)
			if strings.TrimSpace(segment.Text) == "" {
				continue
			}
			switch payload.Kind {
			case "budget":
				summary.LastBudget = segment.Text
			case "compaction":
				summary.LastCompaction = segment.Text
			}
		}
	}

	summary.Recent = recent
	summary.RecentContext = recentContext
	return summary
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

func appendRecentLine(lines []string, line string, limit int) []string {
	if limit == 0 {
		return lines
	}
	lines = append(lines, line)
	if len(lines) > limit {
		return append([]string(nil), lines[len(lines)-limit:]...)
	}
	return lines
}

func isContextDiagnosticEvent(event Event) bool {
	_, ok := event.Payload.(ContextDiagnosticsEvent)
	return ok
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
