package output

import (
	"strings"
	"time"
)

const (
	// ContextReportDisplayOverlay instructs the TUI to render the report in the overlay.
	ContextReportDisplayOverlay = "overlay"
	// ContextReportDisplayInline instructs the TUI to render the report inline in the transcript.
	ContextReportDisplayInline = "inline"
)

// ContextReportEvent carries overlay report content for the TUI.
// The Display field is a hint from the producer: "overlay" or "inline".
// Short single-line content defaults to "inline"; long or multi-line content
// defaults to "overlay".
type ContextReportEvent struct {
	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`
	Display string `json:"display,omitempty"`
	// ScrollToEnd asks the TUI to open an overlay scrolled to its end.
	ScrollToEnd bool `json:"scroll_to_end,omitempty"`
	// Agent names the request scope the report describes. It is nil for
	// reports that are not agent-scoped.
	Agent *ContextReportAgent `json:"agent,omitempty"`
}

// ContextReportAgent identifies the request scope of a context report. An
// empty ID marks the orchestrator.
type ContextReportAgent struct {
	ID string `json:"id,omitempty"`
}

// WithContextReportAgent marks a context report event with the agent scope it
// describes. An empty agentID marks the orchestrator. Events whose payload is
// not a ContextReportEvent are returned unchanged.
func WithContextReportAgent(event Event, agentID string) Event {
	report, ok := event.Payload.(ContextReportEvent)
	if !ok {
		return event
	}
	report.Agent = &ContextReportAgent{ID: agentID}
	event.Payload = report
	return event
}

// classifyContextReportDisplay determines the display mode for a context report.
func classifyContextReportDisplay(content string) string {
	if strings.Contains(content, "\n") || len(content) > 100 {
		return ContextReportDisplayOverlay
	}
	return ContextReportDisplayInline
}

// NewOverlayReportEvent creates an overlay report event from the given title and content.
func NewOverlayReportEvent(title, content string) Event {
	return newContextReportEvent(title, content, classifyContextReportDisplay(content), false)
}

// NewOverlayReportEventAtEnd creates an overlay report event that the TUI opens
// scrolled to its end. The display is always overlay, regardless of content length.
func NewOverlayReportEventAtEnd(title, content string) Event {
	return newContextReportEvent(title, content, ContextReportDisplayOverlay, true)
}

// NewInlineReportEvent creates an inline report event that stays in the
// transcript regardless of content length.
func NewInlineReportEvent(title, content string) Event {
	return newContextReportEvent(title, content, ContextReportDisplayInline, false)
}

func newContextReportEvent(title, content, display string, scrollToEnd bool) Event {
	return Event{
		Type:      EventTypeContextReport,
		Timestamp: time.Now().UTC(),
		Payload: ContextReportEvent{
			Title:       strings.TrimSpace(title),
			Content:     strings.TrimSpace(content),
			Display:     display,
			ScrollToEnd: scrollToEnd,
		},
	}
}
