package tui

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var (
	bgCacheTestWidths = []int{0, 1, 6, 30}
	bgCacheTestBgs    = []string{"#1e1e2e", "", "#abc"}
	bgCacheTestPieces = []string{
		"plain", "word ", " ", "\t", "世界", "🎉", "\x1b[0m", "\x1b[m", "\x1b[49m", "\x1b[1;31m",
		"\r", "x\r", "\n", "\n\n", "a long run of words that overflows the narrow widths",
	}
)

func randomBgCacheBlock(r *rand.Rand) string {
	var sb strings.Builder
	for range r.IntN(5) {
		sb.WriteString(bgCacheTestPieces[r.IntN(len(bgCacheTestPieces))])
	}
	return sb.String()
}

// editBgCacheBlocks returns a fresh block list after one random edit, never
// writing into blocks: the cache keeps the previous list to diff against.
func editBgCacheBlocks(r *rand.Rand, blocks []string) []string {
	next := slices.Clone(blocks)
	switch r.IntN(9) {
	case 0:
		return append(next, randomBgCacheBlock(r))
	case 1:
		if len(next) > 0 {
			next[len(next)-1] = randomBgCacheBlock(r)
		}
	case 2:
		if len(next) > 0 {
			next[r.IntN(len(next))] = randomBgCacheBlock(r)
		}
	case 3:
		// Re-block identical bytes, as a settled-prefix fold does.
		if len(next) > 1 {
			i := r.IntN(len(next) - 1)
			merged := next[i] + "\n" + next[i+1]
			return slices.Concat(next[:i], []string{merged}, next[i+2:])
		}
	case 4:
		if len(next) > 0 {
			i := r.IntN(len(next))
			if before, after, ok := strings.Cut(next[i], "\n"); ok {
				return slices.Concat(next[:i], []string{before, after}, next[i+1:])
			}
		}
	case 5:
		return next[:r.IntN(len(next)+1)]
	case 6:
		return append(next, "", "")
	case 7:
		return []string{strings.Repeat("\n", r.IntN(3))}
	}
	return next
}

// TestBgFormatCacheMatchesOldPipeline feeds random block edits, width and
// background changes to one cache and checks every result against the old
// full join -> format -> split pipeline, and that changed reports exactly
// whether the joined source changed.
func TestBgFormatCacheMatchesOldPipeline(t *testing.T) {
	for seed := range uint64(400) {
		r := rand.New(rand.NewPCG(seed, 5))
		var c bgFormatCache
		var blocks []string
		prevSource := ""
		width, bg := 0, ""
		for step := range 30 {
			blocks = editBgCacheBlocks(r, blocks)
			if r.IntN(6) == 0 {
				width = bgCacheTestWidths[r.IntN(len(bgCacheTestWidths))]
			}
			if r.IntN(6) == 0 {
				bg = bgCacheTestBgs[r.IntN(len(bgCacheTestBgs))]
			}
			source := strings.Join(blocks, "\n")
			changed := c.update(blocks, width, bg)
			if changed != (source != prevSource) {
				t.Fatalf("seed %d step %d: changed = %v for %q -> %q", seed, step, changed, prevSource, source)
			}
			if c.source() != source {
				t.Fatalf("seed %d step %d: source() = %q, want %q", seed, step, c.source(), source)
			}
			// The old pipeline collapses a single zero-width line, as
			// scrollModel.SetLines still does after the cache.
			want, _ := oldPipelineLines(source, width, 0, bg, "")
			if want == nil && len(c.lines) == 1 && ansi.StringWidth(c.lines[0]) == 0 {
				want = c.lines
			}
			if !slices.Equal(c.lines, want) {
				t.Fatalf("seed %d step %d: blocks %q width %d bg %q\n got %q\nwant %q", seed, step, blocks, width, bg, c.lines, want)
			}
			if got := c.lineStart[len(c.blocks)]; got != len(c.lines) || len(c.lineStart) != len(c.blocks)+1 {
				t.Fatalf("seed %d step %d: lineStart %v for %d blocks and %d lines", seed, step, c.lineStart, len(c.blocks), len(c.lines))
			}
			prevSource = source
		}
	}
}

// TestBgFormatCacheReusesLines poisons the cached lines and checks which
// survive an update: reused lines keep the poison, the rest match the oracle.
func TestBgFormatCacheReusesLines(t *testing.T) {
	const marker = "REUSED"
	tests := []struct {
		name       string
		prev, next []string
		wantMarked int
	}{
		{name: "unchanged", prev: []string{"a", "b"}, next: []string{"a", "b"}, wantMarked: 2},
		{name: "re-blocked identical bytes", prev: []string{"a", "b", "c"}, next: []string{"a\nb", "c"}, wantMarked: 3},
		{name: "tail block edited", prev: []string{"a\nb", "c"}, next: []string{"a\nb", "C"}, wantMarked: 2},
		{name: "block appended", prev: []string{"a", "b"}, next: []string{"a", "b", "c"}, wantMarked: 1},
		{name: "head edited", prev: []string{"a", "b", "c"}, next: []string{"A", "b", "c"}, wantMarked: 0},
		{name: "edit inside a block", prev: []string{"a\nb\nc", "d"}, next: []string{"a\nb\nC", "d"}, wantMarked: 2},
		{name: "prev trailing empty lines are not body lines", prev: []string{"a\n", ""}, next: []string{"a\n", "b"}, wantMarked: 1},
		{name: "new trailing empty lines are not body lines", prev: []string{"a\n", "b"}, next: []string{"a\n", ""}, wantMarked: 1},
		{name: "empty block lines reused", prev: []string{"a", "", "b"}, next: []string{"a", "", "c"}, wantMarked: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c bgFormatCache
			c.update(tt.prev, 10, "#000000")
			for i := range c.lines {
				c.lines[i] = marker
			}
			c.update(tt.next, 10, "#000000")
			want, _ := oldPipelineLines(strings.Join(tt.next, "\n"), 10, 0, "#000000", "")
			if len(c.lines) != len(want) {
				t.Fatalf("got %d lines, want %d", len(c.lines), len(want))
			}
			for i := range c.lines {
				wantLine := want[i]
				if i < tt.wantMarked {
					wantLine = marker
				}
				if c.lines[i] != wantLine {
					t.Errorf("line %d = %q, want %q", i, c.lines[i], wantLine)
				}
			}
		})
	}
}

func TestCommonPrefixLen(t *testing.T) {
	long := strings.Repeat("x", 3*commonPrefixChunk+17)
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{name: "empty", a: "", b: "abc", want: 0},
		{name: "equal", a: long, b: long, want: len(long)},
		{name: "prefix", a: long, b: long + "y", want: len(long)},
		{name: "differs in second chunk", a: long, b: long[:commonPrefixChunk+5] + "y" + long[commonPrefixChunk+6:], want: commonPrefixChunk + 5},
		{name: "differs at end", a: long, b: long[:len(long)-1] + "y", want: len(long) - 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commonPrefixLen(tt.a, tt.b); got != tt.want {
				t.Errorf("commonPrefixLen = %d, want %d", got, tt.want)
			}
		})
	}
	for seed := range uint64(300) {
		r := rand.New(rand.NewPCG(seed, 4))
		a := strings.Repeat("y", r.IntN(40*commonPrefixChunk))
		diff := r.IntN(len(a) + 1)
		b := a[:diff] + "z" + a[min(diff+1, len(a)):]
		if got := commonPrefixLen(a, b); got != diff {
			t.Fatalf("seed %d: commonPrefixLen = %d, want %d (len %d)", seed, got, diff, len(a))
		}
	}
}
