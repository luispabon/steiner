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
	return Event{
		Type:      EventTypeContextReport,
		Timestamp: time.Now().UTC(),
		Payload: ContextReportEvent{
			Title:   strings.TrimSpace(title),
			Content: strings.TrimSpace(content),
			Display: classifyContextReportDisplay(content),
		},
	}
}
