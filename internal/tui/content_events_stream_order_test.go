package tui

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// orderedSegments flattens the buffer into "text:<body>" / "tool" entries so
// tests can assert on chronology without depending on render details.
func orderedSegments(b *contentBuffer) []string {
	var got []string
	for _, seg := range b.segments {
		switch seg.kind {
		case segmentAssistantMarkdown:
			got = append(got, "text:"+seg.text)
		case segmentToolCall, segmentToolCallGroup:
			got = append(got, "tool")
		}
	}
	return got
}

func newOrderBuffer() *contentBuffer {
	return &contentBuffer{styles: testStyles(theme.AccentAmber), collapseState: make(map[int]bool)}
}

// streamedTurn replays one model call: chunks, the finalized message the agent
// emits after the stream, then optional tool calls.
func streamedTurn(b *contentBuffer, turn int, chunks []string, message string, toolIDs ...string) {
	for _, chunk := range chunks {
		b.AppendEvent(output.NewAssistantChunkEventWithSource(turn, chunk, output.ChunkSourceAssistant))
	}
	b.AppendEvent(output.NewAssistantMessageEvent(turn, "assistant", message))
	for _, id := range toolIDs {
		b.AppendEvent(output.NewToolCallStartedEvent(turn, "read", id, map[string]any{"path": "main.go"}))
		b.AppendEvent(output.NewToolCallFinishedEvent(turn, "read", id, "ok", nil))
	}
}

func TestStreamedNarrationRendersBeforeToolCardWithoutDuplication(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		build func(b *contentBuffer)
		want  []string
	}{
		{
			name: "narration then tool then result prose",
			build: func(b *contentBuffer) {
				streamedTurn(b, 1, []string{"I will ", "read it."}, "I will read it.", "call-1")
				streamedTurn(b, 2, []string{"Done."}, "Done.")
			},
			want: []string{"text:I will read it.", "tool", "text:Done."},
		},
		{
			name: "narration before each of two tool turns",
			build: func(b *contentBuffer) {
				streamedTurn(b, 1, []string{"First."}, "First.", "call-1")
				streamedTurn(b, 2, []string{"Second."}, "Second.", "call-2")
			},
			want: []string{"text:First.", "tool", "text:Second.", "tool"},
		},
		{
			name: "tool-only turn between narrated turns",
			build: func(b *contentBuffer) {
				streamedTurn(b, 1, []string{"Plan."}, "Plan.", "call-1")
				streamedTurn(b, 2, nil, "", "call-2")
				streamedTurn(b, 3, []string{"Result."}, "Result.")
			},
			want: []string{"text:Plan.", "tool", "text:Result."},
		},
		{
			name: "message-only turn after a streamed turn still renders",
			build: func(b *contentBuffer) {
				streamedTurn(b, 1, []string{"Streamed."}, "Streamed.", "call-1")
				streamedTurn(b, 2, nil, "Unstreamed.")
			},
			want: []string{"text:Streamed.", "tool", "text:Unstreamed."},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newOrderBuffer()
			tc.build(b)
			b.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))
			got := orderedSegments(b)
			// Tool cards may regroup; compare after collapsing adjacent tools.
			if !reflect.DeepEqual(collapseAdjacentTools(got), collapseAdjacentTools(tc.want)) {
				t.Errorf("segments = %q, want %q", got, tc.want)
			}
			if b.hadChunks {
				t.Error("hadChunks leaked past the finalized message")
			}
		})
	}
}

func collapseAdjacentTools(in []string) []string {
	var out []string
	for _, s := range in {
		if s == "tool" && len(out) > 0 && out[len(out)-1] == "tool" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func TestStreamedNarrationFlushesBeforeToolStartsWithoutFinalMessage(t *testing.T) {
	t.Parallel()
	// A tool start arriving while chunks are still buffered must flush them
	// first, so narration never lands below the card it announced.
	b := newOrderBuffer()
	b.AppendEvent(output.NewAssistantChunkEventWithSource(1, "About to read.", output.ChunkSourceAssistant))
	b.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call-1", map[string]any{"path": "main.go"}))
	got := collapseAdjacentTools(orderedSegments(b))
	want := []string{"text:About to read.", "tool"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("segments = %q, want %q", got, want)
	}
}
