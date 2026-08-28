package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// TestContentStringPrefixCache verifies that the settled-prefix cache produces
// exactly the output of the uncached full walk across the four invalidation
// hazards: append-only streaming, retroactive mutation of a settled segment, a
// width change, and a showThinking toggle. Each case warms the prefix cache,
// applies the changing condition, and compares the rendered output against a
// cold reference buffer built from the same content.
func TestContentStringPrefixCache(t *testing.T) {
	useTrueColor(t)
	styles := testStyles(theme.AccentAmber)

	const warmWidth = 80

	cases := []struct {
		name        string
		renderWidth int // 0 means warmWidth
		build       func(b *contentBuffer)
		mutate      func(b *contentBuffer)
	}{
		{
			name: "append-only streaming",
			build: func(b *contentBuffer) {
				b.segments = append(b.segments,
					contentSegment{kind: segmentPlain, text: "settled one", renderDirty: true},
					contentSegment{kind: segmentPlain, text: "settled two", renderDirty: true},
					contentSegment{kind: segmentUserMarkdown, text: "user question", renderDirty: true},
				)
			},
			// Appending past a user segment exercises the prefix/tail boundary
			// separator (\n\n margin) plus the streaming preview.
			mutate: func(b *contentBuffer) {
				b.streaming = true
				b.streamBuffer = "streamed tail"
				b.segments = append(b.segments, contentSegment{kind: segmentPlain, text: "appended after user margin", renderDirty: true})
			},
		},
		{
			name: "retroactive mutation of settled segment",
			build: func(b *contentBuffer) {
				b.segments = append(b.segments,
					contentSegment{kind: segmentPlain, text: "original first line", renderDirty: true},
					contentSegment{kind: segmentPlain, text: "middle line", renderDirty: true},
					contentSegment{kind: segmentPlain, text: "last line", renderDirty: true},
				)
			},
			mutate: func(b *contentBuffer) {
				b.segments[0].text = "mutated first line"
				b.segments[0].renderDirty = true
				b.gen++ // production mutation sites bump gen
			},
		},
		{
			name:        "width change mid-stream",
			renderWidth: 52,
			build: func(b *contentBuffer) {
				b.segments = append(b.segments,
					contentSegment{kind: segmentUserMarkdown, text: strings.Repeat("word ", 30), renderDirty: true},
					contentSegment{kind: segmentPlain, text: "short second line", renderDirty: true},
				)
			},
			mutate: func(b *contentBuffer) {
				b.streaming = true
				b.streamBuffer = "streamed tail"
			},
		},
		{
			name: "showThinking toggle",
			build: func(b *contentBuffer) {
				b.segments = append(b.segments,
					contentSegment{kind: segmentPlain, text: "visible first", renderDirty: true},
					contentSegment{kind: segmentThinkingBlock, thinkData: &thinkingBlockData{body: "hidden thought body", collapsed: true}, renderDirty: true},
					contentSegment{kind: segmentPlain, text: "visible last", renderDirty: true},
				)
			},
			mutate: func(b *contentBuffer) {
				// Mirror the toggle handler's dirty-marking (model_update.go) but
				// skip its gen bump: this proves the prefix cache invalidates on
				// the showThinking key alone, not just via the generation counter.
				b.showThinking = false
				for i := range b.segments {
					if b.segments[i].kind == segmentThinkingBlock {
						b.segments[i].renderDirty = true
					}
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			renderWidth := tc.renderWidth
			if renderWidth == 0 {
				renderWidth = warmWidth
			}

			// Warm the settled prefix: cold render, then a dirty frame whose
			// all-clean tail folds the whole buffer into the prefix cache.
			b := &contentBuffer{styles: styles, collapseState: make(map[int]bool), showThinking: true}
			tc.build(b)
			b.String(warmWidth)
			b.streaming = true
			b.streamBuffer = "warm preview"
			b.String(warmWidth)
			b.streaming = false
			b.streamBuffer = ""

			// Apply the changing condition.
			tc.mutate(b)

			// Cold reference: identical content, no caches; the first render is
			// the uncached full walk.
			ref := &contentBuffer{styles: styles, collapseState: make(map[int]bool), showThinking: true}
			tc.build(ref)
			tc.mutate(ref)

			got := b.String(renderWidth)
			want := ref.String(renderWidth)
			if got != want {
				t.Fatalf("prefix-cached output differs from uncached render:\n--- got ---\n%q\n--- want ---\n%q", got, want)
			}
		})
	}
}

// TestContentStringPrefixCacheToolCallFinished exercises a real production
// mutation of a settled segment (ToolCallFinishedEvent updating the tool-call
// segment inside the prefix) and verifies the cached output matches a cold
// render.
func TestContentStringPrefixCacheToolCallFinished(t *testing.T) {
	useTrueColor(t)
	originalNanoNow := nanoNow
	nanoNow = func() int64 { return 1_000_000_000 }
	defer func() { nanoNow = originalNanoNow }()
	styles := testStyles(theme.AccentAmber)

	build := func() *contentBuffer {
		b := &contentBuffer{styles: styles, collapseState: make(map[int]bool), showThinking: true}
		b.segments = append(b.segments, contentSegment{kind: segmentPlain, text: "intro", renderDirty: true})
		b.AppendEvent(output.NewToolCallStartedEvent(1, "read", "call_1", map[string]any{"file_path": "main.go"}))
		b.AppendEvent(output.NewAssistantChunkEventWithSource(1, "after the tool call", output.ChunkSourceAssistant))
		b.finishStreaming() // settle the buffered chunk into a segment
		return b
	}

	// Warm the prefix over all three segments.
	b := build()
	b.String(80)
	b.streaming = true
	b.streamBuffer = "warm preview"
	b.String(80)
	b.streaming = false
	b.streamBuffer = ""

	// A real event mutates the settled tool-call segment (applyFinishedToolCallResult).
	b.AppendEvent(output.NewToolCallFinishedEvent(1, "read", "call_1", "tool result body", nil))
	got := b.String(80)

	ref := build()
	ref.AppendEvent(output.NewToolCallFinishedEvent(1, "read", "call_1", "tool result body", nil))
	want := ref.String(80)

	if got != want {
		t.Fatalf("prefix-cached output after ToolCallFinished differs from uncached render:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestActiveDelegationStaysOutsidePrefix verifies hazard 4: a segment with an
// active spinner (active delegation) re-renders every frame and must never be
// folded into the settled prefix.
func TestActiveDelegationStaysOutsidePrefix(t *testing.T) {
	useTrueColor(t)
	styles := testStyles(theme.AccentAmber)

	b := &contentBuffer{styles: styles, collapseState: make(map[int]bool), showThinking: true}
	b.segments = append(b.segments, contentSegment{kind: segmentPlain, text: "settled", renderDirty: true})

	b.String(80)
	b.streaming = true
	b.streamBuffer = "warm preview"
	b.String(80)
	b.streaming = false
	b.streamBuffer = ""
	if b.prefixCacheLen != 1 {
		t.Fatalf("prefixCacheLen = %d after warm, want 1 (settled segment folded)", b.prefixCacheLen)
	}

	b.AppendEvent(output.NewDelegationStartedEvent("agent_1", "task"))

	for i := 0; i < 3; i++ {
		_ = b.String(80)
		if b.prefixCacheLen != 1 {
			t.Fatalf("prefixCacheLen = %d after frame %d, want 1 (active delegation must stay in the tail)", b.prefixCacheLen, i)
		}
	}

	// After the delegation completes the buffer settles and the prefix folds
	// over it again.
	b.AppendEvent(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "agent_1",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    10,
		ToolCallCount: 0,
		Output:        "done",
	}))
	_ = b.String(80)
	_ = b.String(80)
	if b.prefixCacheLen != 2 {
		t.Fatalf("prefixCacheLen = %d after delegation completes, want 2 (prefix should re-fold)", b.prefixCacheLen)
	}
}
