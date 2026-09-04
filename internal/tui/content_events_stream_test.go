package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// TestAssistantChunkOrderingAcrossThinkingChunk exercises the sequence the
// Codex Responses stream actually produces within one turn: output_text
// deltas and reasoning deltas interleave live (each emitted as its own
// ThinkingChunk/AssistantChunk event, in arrival order), before the turn
// closes with an APIResponse event. It asserts the rendered segment order
// matches arrival order — i.e. that assistant text emitted before a
// reasoning delta is not deferred until after that reasoning block.
func TestAssistantChunkOrderingAcrossThinkingChunk(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, "waste", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "considering options", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, "ful", output.ChunkSourceAssistant))
	buffer.AppendEvent(output.NewAPIResponseEvent(nil, nil, "", nil))

	t.Logf("segment count = %d", len(buffer.segments))
	for i, seg := range buffer.segments {
		switch seg.kind {
		case segmentThinkingBlock:
			t.Logf("segment %d: thinking block, body=%q", i, seg.thinkData.body)
		case segmentAssistantMarkdown:
			t.Logf("segment %d: assistant markdown, text=%q", i, seg.text)
		default:
			t.Logf("segment %d: kind=%v", i, seg.kind)
		}
	}

	if len(buffer.segments) < 2 {
		t.Fatalf("segments count = %d, want at least 2 (one thinking block, one assistant block)", len(buffer.segments))
	}

	firstKind := buffer.segments[0].kind
	if firstKind != segmentAssistantMarkdown {
		t.Errorf("segment 0 kind = %v, want segmentAssistantMarkdown (the \"waste\" chunk arrived before the thinking chunk, so it should render first)", firstKind)
	}
}
