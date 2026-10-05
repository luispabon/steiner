package tui

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

var (
	bgUpdateWidths = []int{-1, 0, 1, 6, 20, 41}
	bgUpdateBgs    = []string{"#1e1e2e", "", "#abc"}
	bgUpdatePieces = []string{
		"alpha", "beta gamma ", "", "\t", "x\ty", "世界", "🎉", "─│┌", "\r", "end\r",
		"\x1b[0m", "\x1b[1;31mred\x1b[0m", "\x1b[", "\x1b[49m", "long words that overflow the narrow widths",
	}
)

func randomBgUpdateBlock(r *rand.Rand) string {
	lines := make([]string, 1+r.IntN(4))
	for i := range lines {
		var sb strings.Builder
		for range r.IntN(4) {
			sb.WriteString(bgUpdatePieces[r.IntN(len(bgUpdatePieces))])
		}
		lines[i] = sb.String()
	}
	block := strings.Join(lines, "\n")
	switch r.IntN(6) {
	case 0:
		return ""
	case 1:
		return block + "\n"
	case 2:
		return block + "\n\n"
	case 3:
		return strings.ReplaceAll(block, "\n", "\r\n")
	}
	return block
}

func randomBgUpdateBlocks(r *rand.Rand) []string {
	blocks := make([]string, r.IntN(10))
	for i := range blocks {
		blocks[i] = randomBgUpdateBlock(r)
	}
	return blocks
}

// mutateBgUpdateBlocks returns a copy of blocks with one prefix, suffix or
// middle edit (insert, delete, replace, duplicate) applied.
func mutateBgUpdateBlocks(r *rand.Rand, blocks []string) []string {
	out := slices.Clone(blocks)
	pos := func() int { return r.IntN(len(out) + 1) }
	switch op := r.IntN(9); {
	case op == 0 || len(out) == 0:
		i := pos()
		out = slices.Insert(out, i, randomBgUpdateBlock(r))
	case op == 1:
		i := r.IntN(len(out))
		out = slices.Delete(out, i, i+1)
	case op == 2:
		out[r.IntN(len(out))] = randomBgUpdateBlock(r)
	case op == 3:
		out[0] = randomBgUpdateBlock(r)
	case op == 4:
		out[len(out)-1] = randomBgUpdateBlock(r)
	case op == 5:
		out = append(out, randomBgUpdateBlock(r))
	case op == 6:
		i := r.IntN(len(out))
		out = slices.Insert(out, i, out[i])
	case op == 7:
		i := r.IntN(len(out))
		j := i + r.IntN(len(out)-i+1)
		repl := make([]string, r.IntN(3))
		for n := range repl {
			repl[n] = randomBgUpdateBlock(r)
		}
		out = slices.Replace(out, i, j, repl...)
	default:
		out = out[:r.IntN(len(out))]
	}
	return out
}

// formatSplit is the cache's contract: the joined transcript formatted in
// full, split on "\n" with the "\r\n" rule (every line but the last loses one
// trailing '\r').
func formatSplit(blocks []string, width int, bg string) []string {
	if len(blocks) == 0 {
		blocks = emptyTranscript
	}
	lines := strings.Split(theme.FormatContent(strings.Join(blocks, "\n"), width, bg), "\n")
	for i := range len(lines) - 1 {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func TestBgFormatCacheUpdateMatchesFullFormat(t *testing.T) {
	for seed := range uint64(300) {
		r := rand.New(rand.NewPCG(seed, 11))
		var c bgFormatCache
		blocks := randomBgUpdateBlocks(r)
		width, bg := bgUpdateWidths[r.IntN(len(bgUpdateWidths))], bgUpdateBgs[0]
		for step := range 40 {
			switch r.IntN(8) {
			case 0:
				width = bgUpdateWidths[r.IntN(len(bgUpdateWidths))]
			case 1:
				bg = bgUpdateBgs[r.IntN(len(bgUpdateBgs))]
			case 2:
				blocks = randomBgUpdateBlocks(r)
			default:
				blocks = mutateBgUpdateBlocks(r, blocks)
			}
			c.update(slices.Clone(blocks), width, bg)
			if want := formatSplit(blocks, width, bg); !slices.Equal(c.lines, want) {
				t.Fatalf("seed %d step %d (width %d bg %q) blocks %q\n got %q\nwant %q", seed, step, width, bg, blocks, c.lines, want)
			}
			if c.lineStart[len(c.blocks)] != len(c.lines) {
				t.Fatalf("seed %d step %d: lineStart ends at %d, have %d lines", seed, step, c.lineStart[len(c.blocks)], len(c.lines))
			}
		}
	}
}

// TestBgFormatCacheReusesUnchangedTail poisons the cached lines, changes one
// early block and checks that the trailing blocks keep their (shifted) lines
// while the formatter is fed under 10% of the transcript.
func TestBgFormatCacheReusesUnchangedTail(t *testing.T) {
	const marker = "POISONED"
	blocks := make([]string, 120)
	for i := range blocks {
		blocks[i] = strings.Repeat("line of tool output\n", 5) + "last line of block"
	}
	var c bgFormatCache
	c.update(slices.Clone(blocks), 60, "#1e1e2e")
	if len(c.lines) < 700 {
		t.Fatalf("setup: %d lines, want >= 700", len(c.lines))
	}

	for _, edit := range []struct {
		name string
		to   string
	}{
		{"grow", blocks[3] + "\nextra line one\nextra line two"},
		{"shrink", "short"},
		{"same height", strings.Repeat("other output\n", 5) + "last line of block"},
	} {
		t.Run(edit.name, func(t *testing.T) {
			c.update(slices.Clone(blocks), 60, "#1e1e2e")
			for i := range c.lines {
				c.lines[i] = marker
			}
			edited := slices.Clone(blocks)
			edited[3] = edit.to
			c.update(edited, 60, "#1e1e2e")

			want := formatSplit(edited, 60, "#1e1e2e")
			if len(c.lines) != len(want) {
				t.Fatalf("got %d lines, want %d", len(c.lines), len(want))
			}
			fresh := 0
			for i, line := range c.lines {
				if line == marker {
					continue
				}
				fresh++
				if line != want[i] {
					t.Fatalf("line %d = %q, want %q", i, line, want[i])
				}
			}
			if fresh*10 >= len(c.lines) {
				t.Fatalf("formatted %d of %d lines, want < 10%%", fresh, len(c.lines))
			}
		})
	}
}
