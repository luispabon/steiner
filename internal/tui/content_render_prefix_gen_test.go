package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const prefixGenWidth = 80

func newPrefixGenBuffer() *contentBuffer {
	return &contentBuffer{styles: testStyles(theme.AccentAmber), collapseState: make(map[int]bool), showThinking: true}
}

// quietFrame renders until a frame re-renders no segment, so the whole
// buffer is folded into the settled-prefix cache.
func quietFrame(b *contentBuffer) {
	for range 2 {
		b.stringCacheWidth, b.stringCacheBlocks = 0, nil
		b.String(prefixGenWidth)
	}
}

func thinkingDelta(text string) output.Event {
	return output.NewThinkingChunkEventWithSource(1, text, output.ChunkSourceAssistant)
}

func TestThinkingDeltaInTailKeepsPrefixValid(t *testing.T) {
	useTrueColor(t)
	b := newPrefixGenBuffer()
	b.segments = append(b.segments,
		contentSegment{kind: segmentPlain, text: "settled one", renderDirty: true},
		contentSegment{kind: segmentPlain, text: "settled two", renderDirty: true},
	)
	quietFrame(b)
	b.AppendEvent(thinkingDelta("first "))
	b.String(prefixGenWidth)

	gen, plen := b.gen, b.prefixCacheLen
	if plen != 2 || !b.prefixCacheValid(prefixGenWidth) {
		t.Fatalf("prefix not warm over settled segments: len=%d valid=%v", plen, b.prefixCacheValid(prefixGenWidth))
	}
	for _, text := range []string{"second ", "third ", "fourth"} {
		b.AppendEvent(thinkingDelta(text))
		if !b.prefixCacheValid(prefixGenWidth) {
			t.Fatalf("delta %q invalidated the prefix cache", text)
		}
		if b.gen != gen {
			t.Fatalf("gen = %d after tail delta %q, want %d", b.gen, text, gen)
		}
		if got := b.String(prefixGenWidth); !strings.Contains(got, strings.TrimSpace(text)) {
			t.Fatalf("output missing delta %q:\n%s", text, got)
		}
		if b.prefixCacheLen != plen {
			t.Fatalf("prefixCacheLen = %d, want %d (live segment must stay in the tail)", b.prefixCacheLen, plen)
		}
	}
}

func TestThinkingDeltaInFoldedPrefixInvalidates(t *testing.T) {
	useTrueColor(t)
	b := newPrefixGenBuffer()
	b.segments = append(b.segments, contentSegment{kind: segmentPlain, text: "settled", renderDirty: true})
	b.AppendEvent(thinkingDelta("early thought "))
	b.String(prefixGenWidth)
	quietFrame(b)
	if b.prefixCacheLen != 2 {
		t.Fatalf("thinking segment not folded into prefix: prefixCacheLen = %d, want 2", b.prefixCacheLen)
	}

	b.AppendEvent(thinkingDelta("late addition"))
	if b.prefixCacheValid(prefixGenWidth) {
		t.Fatal("delta into a folded thinking segment left the prefix cache valid")
	}
	got := b.String(prefixGenWidth)
	if !strings.Contains(got, "late addition") {
		t.Fatalf("output is stale, missing late delta:\n%s", got)
	}
}

func TestHiddenThinkingDeltaKeepsPrefixValid(t *testing.T) {
	useTrueColor(t)
	b := newPrefixGenBuffer()
	b.showThinking = false
	b.segments = append(b.segments, contentSegment{kind: segmentPlain, text: "settled", renderDirty: true})
	b.AppendEvent(thinkingDelta("hidden "))
	quietFrame(b)
	if b.prefixCacheLen != 2 {
		t.Fatalf("prefixCacheLen = %d, want 2", b.prefixCacheLen)
	}

	gen := b.gen
	b.AppendEvent(thinkingDelta("more"))
	if b.gen != gen || !b.prefixCacheValid(prefixGenWidth) {
		t.Fatalf("hidden thinking delta invalidated the prefix (gen %d -> %d)", gen, b.gen)
	}

	b.showThinking = true
	got := b.String(prefixGenWidth)
	if !strings.Contains(got, "hidden") || !strings.Contains(got, "more") {
		t.Fatalf("thinking text missing after showing it:\n%s", got)
	}
}

func TestInvalidatePrefixIfCached(t *testing.T) {
	tests := []struct {
		name         string
		set          bool
		length       int
		idx          int
		showThinking bool
		wantGen      int
	}{
		{"inside cached prefix", true, 3, 2, true, 1},
		{"hidden thinking inside prefix", true, 3, 2, false, 0},
		{"first past prefix", true, 3, 3, true, 0},
		{"beyond prefix", true, 3, 9, true, 0},
		{"no cache", false, 0, 0, true, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := &contentBuffer{prefixCacheSet: tc.set, prefixCacheLen: tc.length, showThinking: tc.showThinking}
			b.segments = make([]contentSegment, 10)
			if tc.name == "hidden thinking inside prefix" {
				b.segments[tc.idx].kind = segmentThinkingBlock
			}
			b.invalidatePrefixIfCached(tc.idx)
			if b.gen != tc.wantGen {
				t.Fatalf("gen = %d, want %d", b.gen, tc.wantGen)
			}
		})
	}
}

// TestPrefixGenSitesMatchColdRender replays event/mutation sequences on a
// buffer whose prefix cache is folded after every step, and compares the output
// against a cold buffer that only renders at the end. Each step exercises a
// single-segment mutation site that bumps gen through invalidatePrefixIfCached.
func TestPrefixGenSitesMatchColdRender(t *testing.T) {
	useTrueColor(t)
	saved := nanoNow
	nanoNow = func() int64 { return 1_000_000_000 }
	defer func() { nanoNow = saved }()

	ev := func(e output.Event) func(*contentBuffer) {
		return func(b *contentBuffer) { b.AppendEvent(e) }
	}
	toolStart := func(id string) func(*contentBuffer) {
		return ev(output.NewToolCallStartedEvent(1, "read", id, map[string]any{"file_path": "/src/" + id + ".go"}))
	}
	toolDone := func(id, out string) func(*contentBuffer) {
		return ev(output.NewToolCallFinishedEvent(1, "read", id, out, nil))
	}
	answer := func(text string) func(*contentBuffer) {
		return func(b *contentBuffer) {
			b.AppendEvent(output.NewAssistantChunkEventWithSource(1, text, output.ChunkSourceAssistant))
			b.finishStreaming()
		}
	}

	cases := []struct {
		name  string
		steps []func(*contentBuffer)
	}{
		{"thinking deltas and finalize", []func(*contentBuffer){
			answer("hello"), ev(thinkingDelta("a ")), ev(thinkingDelta("b ")),
			func(b *contentBuffer) { b.finalizeThinkingBlock() }, answer("after"),
		}},
		{"thinking then answer then thinking", []func(*contentBuffer){
			ev(thinkingDelta("one ")), answer("mid"), ev(thinkingDelta("two ")), ev(thinkingDelta("three")),
			func(b *contentBuffer) { b.finalizeThinkingBlock() },
		}},
		{"tool start and finish", []func(*contentBuffer){
			answer("go"), toolStart("c1"), toolDone("c1", "body one"), answer("next"),
		}},
		{"adjacent tool calls grouped", []func(*contentBuffer){
			toolStart("c1"), toolStart("c2"), toolStart("c3"),
			toolDone("c2", "two"), toolDone("c1", "one"), toolDone("c3", "three"),
		}},
		{"tool spinner ticks", []func(*contentBuffer){
			answer("go"), toolStart("c1"), func(b *contentBuffer) { b.AdvanceToolCallSpinners() },
			func(b *contentBuffer) { b.AdvanceToolCallSpinners() }, toolDone("c1", "done"),
		}},
		{"delegation lifecycle", []func(*contentBuffer){
			answer("go"),
			ev(output.NewDelegationStartedEvent(agentOcc("a1"), "investigate", "", "")),
			func(b *contentBuffer) { b.AdvanceDelegationSpinners() },
			func(b *contentBuffer) { b.ToggleLastDelegationOutput() },
			func(b *contentBuffer) { b.finalizeActiveDelegation("a1") },
			answer("after"),
		}},
		{"advisor start and complete", []func(*contentBuffer){
			answer("go"),
			ev(output.NewAdvisorStartedEvent("m", 1, 3, "why?", nil)),
			ev(output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{Model: "m", UseNumber: 1, MaxUses: 3, Note: "because"})),
			answer("after"),
		}},
		{"mixed", []func(*contentBuffer){
			answer("q"), ev(thinkingDelta("t1 ")), ev(thinkingDelta("t2 ")),
			func(b *contentBuffer) { b.finalizeThinkingBlock() },
			toolStart("c1"), ev(thinkingDelta("t3 ")), toolDone("c1", "out"), answer("end"),
		}},
	}

	for _, tc := range cases {
		for _, show := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/showThinking=%v", tc.name, show), func(t *testing.T) {
				newBuf := func() *contentBuffer {
					b := newPrefixGenBuffer()
					b.showThinking = show
					return b
				}
				warm := newBuf()
				for i, step := range tc.steps {
					step(warm)
					warm.String(prefixGenWidth)
					quietFrame(warm)

					cold := newBuf()
					for _, s := range tc.steps[:i+1] {
						s(cold)
					}
					if got, want := warm.String(prefixGenWidth), cold.String(prefixGenWidth); got != want {
						t.Fatalf("step %d: warm output differs from cold render\n--- got ---\n%q\n--- want ---\n%q", i, got, want)
					}
				}
			})
		}
	}
}
