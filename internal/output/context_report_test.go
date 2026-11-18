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

func TestWithContextReportAgentPreservesDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		construct   func(title, content string) Event
		agentID     string
		content     string
		wantDisplay string
		wantScroll  bool
	}{
		{name: "orchestrator marker keeps inline display", construct: NewOverlayReportEvent, agentID: "", content: "short", wantDisplay: ContextReportDisplayInline},
		{name: "agent marker keeps overlay display", construct: NewOverlayReportEvent, agentID: "agent-1", content: "a\nb", wantDisplay: ContextReportDisplayOverlay},
		{name: "agent marker keeps scroll to end", construct: NewOverlayReportEventAtEnd, agentID: "agent-1", content: "short", wantDisplay: ContextReportDisplayOverlay, wantScroll: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base, ok := tt.construct("Title", tt.content).Payload.(ContextReportEvent)
			if !ok {
				t.Fatal("constructor payload is not a ContextReportEvent")
			}
			event := WithContextReportAgent(tt.construct("Title", tt.content), tt.agentID)
			if event.Type != EventTypeContextReport {
				t.Fatalf("type = %q, want %q", event.Type, EventTypeContextReport)
			}
			got, ok := event.Payload.(ContextReportEvent)
			if !ok {
				t.Fatalf("payload = %T, want ContextReportEvent", event.Payload)
			}
			if got.Agent == nil {
				t.Fatal("agent marker is nil, want non-nil")
			}
			if got.Agent.ID != tt.agentID {
				t.Fatalf("agent ID = %q, want %q", got.Agent.ID, tt.agentID)
			}
			if got.Title != base.Title || got.Content != base.Content {
				t.Fatalf("title/content = %q/%q, want %q/%q", got.Title, got.Content, base.Title, base.Content)
			}
			if got.Display != tt.wantDisplay {
				t.Fatalf("display = %q, want %q", got.Display, tt.wantDisplay)
			}
			if got.ScrollToEnd != tt.wantScroll {
				t.Fatalf("scroll to end = %v, want %v", got.ScrollToEnd, tt.wantScroll)
			}
		})
	}
}

func TestWithContextReportAgentJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		agentID string
		want    string
	}{
		{
			name:    "agent ID emitted",
			agentID: "agent-1",
			want:    `{"title":"T","content":"C","display":"inline","agent":{"id":"agent-1"}}`,
		},
		{
			name:    "orchestrator marker emitted with empty object",
			agentID: "",
			want:    `{"title":"T","content":"C","display":"inline","agent":{}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event := WithContextReportAgent(NewOverlayReportEvent("T", "C"), tt.agentID)
			data, err := json.Marshal(event.Payload)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if string(data) != tt.want {
				t.Fatalf("json = %s, want %s", data, tt.want)
			}
		})
	}
}

func TestWithContextReportAgentLeavesOtherPayloadsUnchanged(t *testing.T) {
	t.Parallel()
	event := Event{Type: EventTypeContextReport, Payload: "not a report"}
	got := WithContextReportAgent(event, "agent-1")
	if got.Type != event.Type || got.Payload != event.Payload {
		t.Fatalf("event = %#v, want unchanged %#v", got, event)
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
