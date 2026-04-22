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

type StreamOption func(*Stream)

type Stream struct {
	mu        sync.Mutex
	w         io.Writer
	theme     Theme
	streaming Channel
}

func NewStream(w io.Writer, options ...StreamOption) *Stream {
	stream := &Stream{
		w:     w,
		theme: defaultTheme(w),
	}
	for _, option := range options {
		if option != nil {
			option(stream)
		}
	}
	return stream
}

func WithTheme(theme Theme) StreamOption {
	return func(stream *Stream) {
		if stream != nil {
			stream.theme = theme
		}
	}
}

func (s *Stream) Println(args ...any) {
	if s == nil || s.w == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finishStreamingLocked()
	fmt.Fprintln(s.w, args...)
}

func (s *Stream) Printf(format string, args ...any) {
	if s == nil || s.w == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.w, format, args...)
}

func (s *Stream) Emit(event Event) {
	if s == nil || s.w == nil {
		return
	}
	rendered := renderEvent(event)
	s.Render(rendered)
}

func (s *Stream) Render(segment Segment) {
	if s == nil || s.w == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renderLocked(segment)
}

func (s *Stream) WriteAssistant(text string) {
	s.Render(Segment{Channel: ChannelAssistant, Text: text})
}

func (s *Stream) WriteAssistantChunk(text string) {
	s.Render(Segment{Channel: ChannelAssistant, Text: text, Streaming: true})
}

func (s *Stream) FinishAssistant() {
	if s == nil || s.w == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streaming == ChannelAssistant {
		s.finishStreamingLocked()
	}
}

func (s *Stream) renderLocked(segment Segment) {
	if segment.Channel == "" {
		segment.Channel = ChannelStatus
	}
	if segment.Streaming {
		s.renderStreamingLocked(segment)
		return
	}
	s.finishStreamingLocked()
	line := formatSegment(segment)
	if line == "" {
		return
	}
	fmt.Fprintln(s.w, s.decorate(segment.Channel, line))
}

func (s *Stream) renderStreamingLocked(segment Segment) {
	if s.streaming != "" && s.streaming != segment.Channel {
		s.finishStreamingLocked()
	}
	if s.streaming == "" {
		label := segment.Label
		if label == "" {
			label = string(segment.Channel)
		}
		_, _ = io.WriteString(s.w, s.decorate(segment.Channel, label+"> "))
		s.streaming = segment.Channel
	}
	_, _ = io.WriteString(s.w, segment.Text)
}

func (s *Stream) finishStreamingLocked() {
	if s.streaming == "" {
		return
	}
	_, _ = io.WriteString(s.w, "\n")
	s.streaming = ""
}

func (s *Stream) decorate(channel Channel, text string) string {
	style := s.theme.style(channel)
	if !s.theme.Enabled {
		return text
	}
	return style.LabelPrefix + text + style.LabelSuffix
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

func (s *Stream) Themed(channel Channel, text string) string {
	return s.decorate(channel, text)
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
