package output

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderedToolFieldsAreBoundedAndValidUTF8(t *testing.T) {
	huge := strings.Repeat("héllo wörld 日本語 ", 60000) // >1 MB, multibyte
	if len(huge) < 1<<20 {
		t.Fatalf("fixture too small: %d", len(huge))
	}
	tests := []struct {
		name  string
		event Event
	}{
		{"started argument", NewToolCallStartedEvent(1, "bash", "c1", map[string]any{"command": huge})},
		{"finished result", NewToolCallFinishedEvent(1, "bash", "c1", huge, nil)},
		{"unknown payload", Event{Type: "mystery", Payload: map[string]any{"data": huge}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatEvent(tt.event)
			if len(got) > 4*maxRenderedToolFieldRunes {
				t.Fatalf("rendered %d bytes, want bounded", len(got))
			}
			if !utf8.ValidString(got) {
				t.Fatalf("rendered text is not valid UTF-8")
			}
			if !strings.Contains(got, "...") {
				t.Fatalf("expected truncation ellipsis in %q", got[:80])
			}
		})
	}
}

func TestPlainRendererPrintfFinishesStreamingLine(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlainRenderer(&buf)
	r.WriteAssistantChunk("partial answer")
	r.Printf("notice %d\n", 1)
	if got, want := buf.String(), "assistant: partial answer\nnotice 1\n"; !strings.HasSuffix(got, "partial answer\nnotice 1\n") {
		t.Fatalf("got %q, want suffix of %q", got, want)
	}
}
