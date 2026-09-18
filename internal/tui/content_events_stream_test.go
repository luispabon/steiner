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

	// Exact segment count: "waste" chunk, thinking block, "ful" chunk
	if len(buffer.segments) != 3 {
		t.Fatalf("segments count = %d, want 3", len(buffer.segments))
	}

	// First segment: "waste" assistant markdown (arrived before thinking chunk)
	if buffer.segments[0].kind != segmentAssistantMarkdown {
		t.Errorf("segment 0 kind = %v, want segmentAssistantMarkdown", buffer.segments[0].kind)
	}
	if buffer.segments[0].text != "waste" {
		t.Errorf("segment 0 text = %q, want 'waste'", buffer.segments[0].text)
	}

	// Second segment: thinking block
	if buffer.segments[1].kind != segmentThinkingBlock {
		t.Errorf("segment 1 kind = %v, want segmentThinkingBlock", buffer.segments[1].kind)
	}
	if buffer.segments[1].thinkData == nil || buffer.segments[1].thinkData.body != "considering options" {
		body := ""
		if buffer.segments[1].thinkData != nil {
			body = buffer.segments[1].thinkData.body
		}
		t.Errorf("segment 1 thinking body = %q, want 'considering options'", body)
	}

	// Third segment: "ful" assistant markdown (arrived after thinking chunk)
	if buffer.segments[2].kind != segmentAssistantMarkdown {
		t.Errorf("segment 2 kind = %v, want segmentAssistantMarkdown", buffer.segments[2].kind)
	}
	if buffer.segments[2].text != "ful" {
		t.Errorf("segment 2 text = %q, want 'ful'", buffer.segments[2].text)
	}
}

func TestThinkingChunkFlushesAssistantBufferEvenWithOpenThinkingSegment(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		segments:      make([]contentSegment, 0),
		collapseState: make(map[int]bool),
	}

	// Scenario: answer -> thinking1 -> answer -> thinking2.
	// If a thinking block from phase 1 is still open (not finalized),
	// and we enter a new answer phase, and THEN get a thinking chunk,
	// the old check (liveThinkingSegment() == nil) would fail and the
	// answer would be lost into the stream buffer.

	// Stream answer text
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, "answer1", output.ChunkSourceAssistant))
	if buffer.streamingPhase != "answer" {
		t.Fatalf("after answer chunk: phase=%q, want answer", buffer.streamingPhase)
	}

	// Stream thinking (creates thinking segment, phase -> thinking)
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "thinking1", output.ChunkSourceAssistant))
	if buffer.streamingPhase != "thinking" {
		t.Fatalf("after thinking chunk: phase=%q, want thinking", buffer.streamingPhase)
	}

	// Now DON'T finalize the thinking block; instead stream more answer
	// (This simulates a turn where thinking and answer are interleaved)
	buffer.AppendEvent(output.NewAssistantChunkEventWithSource(1, "answer2", output.ChunkSourceAssistant))
	if buffer.streamingPhase != "answer" {
		t.Fatalf("after second answer chunk: phase=%q, want answer", buffer.streamingPhase)
	}

	// At this point:
	// - liveThinkingSegment() != nil (thinking1 is still open in segments)
	// - streamingPhase == "answer" (we just started answer phase again)
	// - streamBuffer has "answer2"

	// Now stream ANOTHER thinking chunk while the old thinking segment is still open
	buffer.AppendEvent(output.NewThinkingChunkEventWithSource(1, "thinking2", output.ChunkSourceAssistant))

	// Finalize
	buffer.finishStreaming()

	// Expected segments in order:
	// 1. "answer1" (assistant markdown)
	// 2. "thinking1" (thinking block)
	// 3. "answer2" (assistant markdown) -- this is the key one that used to get lost
	// 4. "thinking2" (thinking block)

	for i, seg := range buffer.segments {
		t.Logf("segment %d: kind=%v", i, seg.kind)
		if seg.kind == segmentAssistantMarkdown {
			t.Logf("  text=%q", seg.text)
		} else if seg.kind == segmentThinkingBlock && seg.thinkData != nil {
			t.Logf("  body=%q", seg.thinkData.body)
		}
	}

	if len(buffer.segments) < 4 {
		t.Fatalf("segment count = %d, want at least 4", len(buffer.segments))
	}

	if buffer.segments[0].kind != segmentAssistantMarkdown || buffer.segments[0].text != "answer1" {
		t.Errorf("segment 0: kind=%v text=%q, want AssistantMarkdown 'answer1'", buffer.segments[0].kind, buffer.segments[0].text)
	}

	if buffer.segments[1].kind != segmentThinkingBlock {
		t.Errorf("segment 1: kind=%v, want thinking block", buffer.segments[1].kind)
	}
	if buffer.segments[1].thinkData != nil && buffer.segments[1].thinkData.body != "thinking1" {
		t.Errorf("segment 1 thinking body: %q, want 'thinking1'", buffer.segments[1].thinkData.body)
	}

	// The critical segment - answer2 must exist as its own segment
	if buffer.segments[2].kind != segmentAssistantMarkdown || buffer.segments[2].text != "answer2" {
		t.Errorf("segment 2: kind=%v text=%q, want AssistantMarkdown 'answer2' (this got lost in the bug)", buffer.segments[2].kind, buffer.segments[2].text)
	}

	if buffer.segments[3].kind != segmentThinkingBlock {
		t.Errorf("segment 3: kind=%v, want thinking block", buffer.segments[3].kind)
	}
	if buffer.segments[3].thinkData != nil && buffer.segments[3].thinkData.body != "thinking2" {
		t.Errorf("segment 3 thinking body: %q, want 'thinking2'", buffer.segments[3].thinkData.body)
	}
}
