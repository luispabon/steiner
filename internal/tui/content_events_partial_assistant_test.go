package tui

import (
	"errors"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// TestPartialAssistantMessageAfterModelCallFinishedDoesNotDuplicate pins the
// run-exit partial commit reaching the TUI after the streaming call already
// ended: the finalized message must not repeat the streamed block, and it must
// clear hadChunks so a later non-streamed message still renders.
func TestPartialAssistantMessageAfterModelCallFinishedDoesNotDuplicate(t *testing.T) {
	t.Parallel()
	b := newOrderBuffer()

	b.AppendEvent(output.NewAssistantChunkEventWithSource(1, "the report so far", output.ChunkSourceAssistant))
	b.AppendEvent(output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{
		Turn: 1, Model: "test-model", Err: errors.New("stream interrupted after partial output"),
	}))
	b.AppendEvent(output.NewAssistantMessageEvent(1, "assistant", "the report so far"))

	want := []string{"text:the report so far"}
	if got := orderedSegments(b); !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %q, want %q (the partial must not duplicate the streamed block)", got, want)
	}
	if b.hadChunks {
		t.Fatal("hadChunks = true after the partial message, want it cleared")
	}

	b.AppendEvent(output.NewAssistantMessageEvent(2, "assistant", "a later unstreamed message"))
	want = append(want, "text:a later unstreamed message")
	if got := orderedSegments(b); !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %q, want %q (a later non-streamed message must render)", got, want)
	}
}
