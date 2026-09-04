package output

import (
	"errors"
	"testing"
)

func TestRenderConfigWarningEvent(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "plain message",
			message: "project_context.max_tokens is deprecated",
			want:    "project_context.max_tokens is deprecated",
		},
		{
			name:    "empty message",
			message: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seg := renderEvent(NewConfigWarningEvent(tt.message))
			if seg.Channel != ChannelStatus {
				t.Fatalf("Channel = %q, want %q", seg.Channel, ChannelStatus)
			}
			if seg.Label != "status" {
				t.Fatalf("Label = %q, want %q", seg.Label, "status")
			}
			if seg.Text != tt.want {
				t.Fatalf("Text = %q, want %q", seg.Text, tt.want)
			}
		})
	}
}

func TestRenderRunStartedEvent(t *testing.T) {
	event := NewRunStartedEvent("oneshot", "gpt-4", "analyze this", 10, 4096)
	seg := renderEvent(event)
	if seg.Channel != ChannelStatus {
		t.Fatalf("Channel = %q", seg.Channel)
	}
	if seg.Label != "status" {
		t.Fatalf("Label = %q", seg.Label)
	}
}

func TestRenderAssistantMessageEvent(t *testing.T) {
	event := NewAssistantMessageEvent(1, "assistant", "Response text")
	seg := renderEvent(event)
	if seg.Channel != ChannelAssistant {
		t.Fatalf("Channel = %q, want %q", seg.Channel, ChannelAssistant)
	}
}

func TestRenderAssistantChunkEvent(t *testing.T) {
	event := NewAssistantChunkEventWithSource(1, "chunk", ChunkSourceAssistant)
	seg := renderEvent(event)
	if seg.Channel != ChannelAssistant {
		t.Fatalf("Channel = %q", seg.Channel)
	}
}

func TestRenderThinkingChunkEvent(t *testing.T) {
	event := NewThinkingChunkEventWithSource(1, "thinking text", ChunkSourceAssistant)
	seg := renderEvent(event)
	if seg.Channel != ChannelStatus {
		t.Fatalf("Channel = %q", seg.Channel)
	}
	if seg.Label != "thinking" {
		t.Fatalf("Label = %q", seg.Label)
	}
}

func TestErrorChannelHelper(t *testing.T) {
	ch, label := errorChannel(true)
	if ch != ChannelError || label != "error" {
		t.Fatalf("error case failed")
	}
	ch, label = errorChannel(false)
	if ch != ChannelStatus || label != "status" {
		t.Fatalf("non-error case failed")
	}
}

func TestRenderModelCallFinishedEventWithError(t *testing.T) {
	event := NewModelCallFinishedEvent(ModelCallFinishedParams{
		Turn:  1,
		Model: "gpt-4",
		Err:   errors.New("test error"),
	})
	seg := renderEvent(event)
	if seg.Channel != ChannelError {
		t.Fatalf("Channel = %q, want error", seg.Channel)
	}
}

func TestFormatSegment(t *testing.T) {
	tests := []struct {
		segment  Segment
		wantText string
	}{
		{Segment{Label: "test", Text: "content"}, "test: content"},
		{Segment{Text: "content"}, "content"},
		{Segment{Label: "test", Text: ""}, ""},
		{Segment{Label: "test", Text: "   "}, ""},
	}

	for _, tt := range tests {
		result := formatSegment(tt.segment)
		if result != tt.wantText {
			t.Fatalf("result = %q, want %q", result, tt.wantText)
		}
	}
}
