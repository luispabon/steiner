package provider

import (
	"strconv"
	"strings"
	"testing"
)

// TestCodexStreamedTextExtendsToFinalSnapshot pins an adapter property the
// agent relies on: text emitted as deltas before the terminal snapshot must be
// a prefix of that snapshot's content. The agent emits an extension as a chunk
// but cannot place text that diverges from what already streamed, so a
// violation means display loss (#832). Agent-side emission is covered by
// TestStreamedDeltasThenSnapshotEmitTextOnce in internal/agent.
func TestCodexStreamedTextExtendsToFinalSnapshot(t *testing.T) {
	msgDelta := func(item string, index int, text string) string {
		return `{"type":"response.output_text.delta","item_id":"` + item + `","output_index":` + strconv.Itoa(index) + `,"content_index":0,"delta":"` + text + `"}`
	}
	msgDone := func(item string, index int, phase, text string) string {
		return `{"type":"response.output_item.done","output_index":` + strconv.Itoa(index) + `,"item":{"type":"message","id":"` + item + `","phase":"` + phase + `","content":[{"type":"output_text","text":"` + text + `"}]}}`
	}
	callDone := func(item string, index int) string {
		return `{"type":"response.output_item.done","output_index":` + strconv.Itoa(index) + `,"item":{"type":"function_call","id":"` + item + `","call_id":"c-` + item + `","name":"read","arguments":"{}"}}`
	}
	completed := func(items ...string) string {
		return `{"type":"response.completed","response":{"output":[` + strings.Join(items, ",") + `],"usage":{}}}`
	}
	msgItem := func(item, phase, text string) string {
		return `{"type":"message","id":"` + item + `","phase":"` + phase + `","content":[{"type":"output_text","text":"` + text + `"}]}`
	}
	callItem := func(item string) string {
		return `{"type":"function_call","id":"` + item + `","call_id":"c-` + item + `","name":"read","arguments":"{}"}`
	}

	tests := []struct {
		name   string
		frames []string
		// divergent marks a known gap: the completion snapshot contains text
		// ordered before what already streamed (an earlier output index seen
		// only at completion, e.g. lost frames). The agent logs it; recovering
		// it would reorder display chronology, which #832 defers until raw
		// wire order is evidenced. Flip this when that decision is made.
		divergent bool
	}{
		{
			name: "commentary streamed then tool call",
			frames: []string{
				msgDelta("m1", 0, "I will read"), msgDelta("m1", 0, " it."), msgDone("m1", 0, "commentary", "I will read it."),
				callDone("f1", 1),
				completed(msgItem("m1", "commentary", "I will read it."), callItem("f1")),
			},
		},
		{
			name: "commentary only in item done, then tool call",
			frames: []string{
				msgDone("m1", 0, "commentary", "I will read it."),
				callDone("f1", 1),
				completed(msgItem("m1", "commentary", "I will read it."), callItem("f1")),
			},
		},
		{
			name: "commentary streamed, final answer only in completed",
			frames: []string{
				msgDelta("m1", 0, "Working."), msgDone("m1", 0, "commentary", "Working."),
				completed(msgItem("m1", "commentary", "Working."), msgItem("m2", "final_answer", "All done.")),
			},
		},
		{
			name: "both messages streamed",
			frames: []string{
				msgDelta("m1", 0, "Working."), msgDone("m1", 0, "commentary", "Working."),
				msgDelta("m2", 1, "All done."), msgDone("m2", 1, "final_answer", "All done."),
				completed(msgItem("m1", "commentary", "Working."), msgItem("m2", "final_answer", "All done.")),
			},
		},
		{
			name:      "earlier output index first seen only at completion",
			divergent: true,
			frames: []string{
				msgDelta("m2", 1, "All done."), msgDone("m2", 1, "final_answer", "All done."),
				completed(msgItem("m1", "commentary", "Working."), msgItem("m2", "final_answer", "All done.")),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.frames = append(tc.frames, `[DONE]`)
			chunks := reviewLedgerFramesFollowup(t, tc.frames...)
			if len(chunks) == 0 {
				t.Fatal("no chunks produced")
			}
			var streamed strings.Builder
			for _, chunk := range chunks[:len(chunks)-1] {
				streamed.WriteString(chunk.Delta.Content)
			}
			final := chunks[len(chunks)-1]
			if !final.Done {
				t.Fatalf("last chunk Done = false, chunks = %d", len(chunks))
			}
			isPrefix := strings.HasPrefix(final.Delta.Content, streamed.String())
			if isPrefix == tc.divergent {
				t.Errorf("streamed %q, final %q: prefix = %v, want %v", streamed.String(), final.Delta.Content, isPrefix, !tc.divergent)
			}
		})
	}
}
