package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

type contentBuffer struct {
	lines     []string
	streaming bool
}

func (b *contentBuffer) AppendEvent(event output.Event) {
	switch payload := event.Payload.(type) {
	case output.AssistantChunkEvent:
		b.appendAssistantChunk(payload.Content)
	default:
		b.finishStreaming()
		if line := strings.TrimSpace(output.FormatEvent(event)); line != "" {
			b.lines = append(b.lines, line)
		}
	}
}

func (b *contentBuffer) AppendLine(line string) {
	b.finishStreaming()
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	b.lines = append(b.lines, line)
}

func (b *contentBuffer) Clear() {
	b.lines = nil
	b.streaming = false
}

func (b *contentBuffer) String() string {
	if len(b.lines) == 0 {
		return ""
	}
	return strings.Join(b.lines, "\n")
}

func (b *contentBuffer) appendAssistantChunk(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if !b.streaming || len(b.lines) == 0 {
		b.lines = append(b.lines, "assistant> "+text)
		b.streaming = true
		return
	}
	b.lines[len(b.lines)-1] += text
}

func (b *contentBuffer) finishStreaming() {
	b.streaming = false
}
