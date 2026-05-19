package output

import (
	"strings"
	"time"
)

// ContextReportEvent carries overlay report content for the TUI.
type ContextReportEvent struct {
	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`
}

// NewContextReportEvent creates a context report event from the given content.
func NewContextReportEvent(content string) Event {
	return NewOverlayReportEvent("Context Report", content)
}

// NewConfigReportEvent creates a config report event from the given content.
func NewConfigReportEvent(content string) Event {
	return NewOverlayReportEvent("Config", content)
}

// NewOverlayReportEvent creates an overlay report event from the given title and content.
func NewOverlayReportEvent(title, content string) Event {
	return Event{
		Type:      EventTypeContextReport,
		Timestamp: time.Now().UTC(),
		Payload: ContextReportEvent{
			Title:   strings.TrimSpace(title),
			Content: strings.TrimSpace(content),
		},
	}
}
