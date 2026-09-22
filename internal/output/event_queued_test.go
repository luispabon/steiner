package output

import (
	"strings"
	"testing"
)

func TestNewToolCallQueuedEvent(t *testing.T) {
	t.Parallel()
	args := map[string]any{"type": "code"}
	event := NewToolCallQueuedEvent(2, "sub_agent", "call-1", args)
	if event.Type != EventTypeToolCallQueued {
		t.Fatalf("Type = %q, want %q", event.Type, EventTypeToolCallQueued)
	}
	payload, ok := event.Payload.(ToolCallQueuedEvent)
	if !ok {
		t.Fatalf("Payload = %T, want ToolCallQueuedEvent", event.Payload)
	}
	if payload.Turn != 2 || payload.Tool != "sub_agent" || payload.CallID != "call-1" {
		t.Fatalf("payload = %#v, want turn=2 tool=sub_agent call=call-1", payload)
	}
	if payload.Arguments["type"] != "code" {
		t.Fatalf("Arguments = %#v, want type=code", payload.Arguments)
	}

	segment := renderEvent(event)
	if segment.Channel != ChannelTool {
		t.Fatalf("render channel = %q, want %q", segment.Channel, ChannelTool)
	}
	if !strings.Contains(segment.Text, "queued") || !strings.Contains(segment.Text, "sub_agent") {
		t.Fatalf("rendered text = %q, want queued sub_agent", segment.Text)
	}
}
