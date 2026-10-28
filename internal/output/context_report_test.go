package output

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContextReportConstructors(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 101)
	tests := []struct {
		name        string
		construct   func(title, content string) Event
		content     string
		wantDisplay string
		wantScroll  bool
	}{
		{name: "overlay short single line stays inline", construct: NewOverlayReportEvent, content: "short", wantDisplay: ContextReportDisplayInline},
		{name: "overlay multi line is overlay", construct: NewOverlayReportEvent, content: "a\nb", wantDisplay: ContextReportDisplayOverlay},
		{name: "at end short content is overlay", construct: NewOverlayReportEventAtEnd, content: "short", wantDisplay: ContextReportDisplayOverlay, wantScroll: true},
		{name: "at end long content is overlay", construct: NewOverlayReportEventAtEnd, content: long, wantDisplay: ContextReportDisplayOverlay, wantScroll: true},
		{name: "inline short content", construct: NewInlineReportEvent, content: "short", wantDisplay: ContextReportDisplayInline},
		{name: "inline multi line stays inline", construct: NewInlineReportEvent, content: "a\nb", wantDisplay: ContextReportDisplayInline},
		{name: "inline long content stays inline", construct: NewInlineReportEvent, content: long, wantDisplay: ContextReportDisplayInline},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Surrounding spaces only: the overlay classifier sees untrimmed content.
			event := tt.construct("  Title  ", "  "+tt.content+"  ")
			if event.Type != EventTypeContextReport {
				t.Fatalf("type = %q, want %q", event.Type, EventTypeContextReport)
			}
			got, ok := event.Payload.(ContextReportEvent)
			if !ok {
				t.Fatalf("payload = %T, want ContextReportEvent", event.Payload)
			}
			want := ContextReportEvent{Title: "Title", Content: tt.content, Display: tt.wantDisplay, ScrollToEnd: tt.wantScroll}
			if got != want {
				t.Fatalf("payload = %#v, want %#v", got, want)
			}
		})
	}
}

func TestContextReportEventScrollToEndJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		event ContextReportEvent
		want  string
	}{
		{
			name:  "scroll to end emitted when true",
			event: ContextReportEvent{Title: "T", Content: "C", Display: ContextReportDisplayOverlay, ScrollToEnd: true},
			want:  `{"title":"T","content":"C","display":"overlay","scroll_to_end":true}`,
		},
		{
			name:  "scroll to end omitted when false",
			event: ContextReportEvent{Title: "T", Content: "C", Display: ContextReportDisplayInline},
			want:  `{"title":"T","content":"C","display":"inline"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if string(data) != tt.want {
				t.Fatalf("json = %s, want %s", data, tt.want)
			}
		})
	}
}
