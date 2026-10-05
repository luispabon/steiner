package tui

import (
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// commonPrefixChunk is the initial block size compared with string equality
// (a vectorised memequal); below commonPrefixMinChunk a byte scan finishes.
const (
	commonPrefixChunk    = 4096
	commonPrefixMinChunk = 64
)

// emptyTranscript is the block list of the empty transcript.
var emptyTranscript = []string{""}

// bgFormatCache holds the viewport's formatted transcript lines: the
// transcript strings.Join(blocks, "\n") run through theme.FormatContent
// (background restoration plus padding), split on "\n" with "\r\n"
// normalised to "\n" as the scroll model's line rule requires (a '\r' ending
// a line that is followed by another loses one '\r'). Validity is keyed on
// the source bytes, so no content-buffer invalidation can leave it stale; an
// unchanged block is usually the same substring as last frame, which makes
// skipping it O(1). The zero value is the empty transcript at width 0.
type bgFormatCache struct {
	blocks []string
	// lineStart[i] is the first transcript line of blocks[i];
	// lineStart[len(blocks)] is the line count.
	lineStart []int
	// bodyLines counts the lines up to the last one holding a non-newline
	// byte; FormatContent pads the trailing empty lines after it without
	// background restoration.
	bodyLines int
	lines     []string
	width     int
	bg        string
}

// update formats blocks for width and bg, reformatting only from the first
// line that changed, and reports whether the joined source changed. lines
// keeps its backing array where possible, so slices handed out earlier are
// overwritten.
func (c *bgFormatCache) update(blocks []string, width int, bg string) (changed bool) {
	if c.lineStart == nil {
		c.blocks, c.lineStart, c.lines = emptyTranscript, []int{0, 1}, []string{""}
	}
	if len(blocks) == 0 {
		blocks = emptyTranscript
	}
	same := sameHeadBlocks(c.blocks, blocks)
	sameFormat := width == c.width && bg == c.bg
	if same == len(blocks) && same == len(c.blocks) && sameFormat {
		return false
	}
	pos, equal := joinedCommonPrefix(c.blocks, blocks, same)

	oldBody := c.bodyLines
	tail := sameTailBlocks(c.blocks, blocks, same)
	tailOld := c.lineStart[len(c.blocks)-tail]
	c.rebuildLineStart(blocks, same)
	if equal && sameFormat {
		return false
	}
	c.bodyLines = bodyLineCount(blocks, c.lineStart)

	k := 0
	if sameFormat {
		common := c.lineStart[pos.block] + strings.Count(blocks[pos.block][:pos.off], "\n")
		k = min(common, oldBody, c.bodyLines)
	}
	c.width, c.bg = width, bg
	if tail > 0 && sameFormat && c.reformatAroundTail(k, tail, tailOld, oldBody) {
		return !equal
	}
	c.reformatFrom(k)
	return !equal
}

// rebuildLineStart sets blocks and recomputes lineStart, keeping the entries
// of the first same blocks.
func (c *bgFormatCache) rebuildLineStart(blocks []string, same int) {
	c.lineStart = c.lineStart[:same+1]
	for _, b := range blocks[same:] {
		c.lineStart = append(c.lineStart, c.lineStart[len(c.lineStart)-1]+strings.Count(b, "\n")+1)
	}
	c.blocks = blocks
}

// sameHeadBlocks counts the equal blocks at the start of a and b.
func sameHeadBlocks(a, b []string) int {
	n := 0
	for n < min(len(a), len(b)) && a[n] == b[n] {
		n++
	}
	return n
}

// sameTailBlocks counts the blocks at the end of a and b that are equal,
// leaving the first from blocks to the prefix comparison.
func sameTailBlocks(a, b []string, from int) int {
	n := 0
	for n < min(len(a), len(b))-from && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// reformatAroundTail reformats only the lines between the common prefix (the
// first k lines) and the last tail blocks, which are unchanged but may have
// moved; their formatted lines are shifted rather than formatted again. tailOld
// is the old first line of those blocks and oldBody the old body line count. It
// reports false, leaving the cache untouched, when the tail's lines are not
// body lines at the same distance from the body end in both transcripts.
func (c *bgFormatCache) reformatAroundTail(k, tail, tailOld, oldBody int) bool {
	tailNew := c.lineStart[len(c.blocks)-tail]
	if tailNew >= c.bodyLines || tailOld >= oldBody || c.bodyLines-tailNew != oldBody-tailOld {
		return false
	}
	from := max(tailNew, k)
	if from >= c.bodyLines {
		return false
	}
	shift := tailNew - tailOld
	total := c.lineStart[len(c.blocks)]

	old := c.lines
	lines := old
	if cap(old) >= total {
		lines = old[:total]
	} else {
		lines = make([]string, total, total+total/4)
		copy(lines, old[:k])
	}
	copy(lines[from:c.bodyLines], old[from-shift:oldBody])

	if k < tailNew {
		bk := sort.SearchInts(c.lineStart[:len(c.blocks)], k+1) - 1
		off := 0
		for range k - c.lineStart[bk] {
			off += strings.IndexByte(c.blocks[bk][off:], '\n') + 1
		}
		middle := c.blocks[bk][off:]
		if end := len(c.blocks) - tail; bk+1 < end {
			middle += "\n" + strings.Join(c.blocks[bk+1:end], "\n")
		}
		formatted := theme.FormatBody(middle, c.width, c.bg)
		for i := k; i < tailNew; i++ {
			line, after, _ := strings.Cut(formatted, "\n")
			lines[i] = strings.TrimSuffix(line, "\r")
			formatted = after
		}
	}
	trailing := theme.FormatContent("", c.width, c.bg)
	for i := c.bodyLines; i < total; i++ {
		lines[i] = trailing
	}
	if len(lines) < len(old) {
		clear(old[len(lines):])
	}
	c.lines = lines
	return true
}

// reformatFrom replaces the formatted lines from line k to the end. Lines
// before k must be unchanged and body lines in both old and new transcripts.
func (c *bgFormatCache) reformatFrom(k int) {
	bk := sort.SearchInts(c.lineStart[:len(c.blocks)], k+1) - 1
	off := 0
	for range k - c.lineStart[bk] {
		off += strings.IndexByte(c.blocks[bk][off:], '\n') + 1
	}
	rest := c.blocks[bk][off:]
	if bk+1 < len(c.blocks) {
		rest += "\n" + strings.Join(c.blocks[bk+1:], "\n")
	}
	formatted := theme.FormatContent(rest, c.width, c.bg)

	old := c.lines
	lines := old[:k]
	for {
		line, after, found := strings.Cut(formatted, "\n")
		if !found {
			lines = append(lines, line)
			break
		}
		lines = append(lines, strings.TrimSuffix(line, "\r"))
		formatted = after
	}
	if len(lines) < len(old) {
		// No reallocation happened, so the dropped tail still pins strings.
		clear(old[len(lines):])
	}
	c.lines = lines
}

// rawLines returns the unformatted transcript lines [from, to).
func (c *bgFormatCache) rawLines(from, to int) []string {
	bk := sort.SearchInts(c.lineStart[:len(c.blocks)], from+1) - 1
	out := make([]string, 0, to-from)
	rest, ln := c.blocks[bk], c.lineStart[bk]
	for ln < to {
		line, after, found := strings.Cut(rest, "\n")
		if ln >= from {
			out = append(out, line)
		}
		ln++
		if found {
			rest = after
			continue
		}
		if bk++; bk == len(c.blocks) {
			break
		}
		rest = c.blocks[bk]
	}
	return out
}

// source returns the transcript the lines were formatted from.
func (c *bgFormatCache) source() string {
	return strings.Join(c.blocks, "\n")
}

// bodyLineCount returns the number of lines up to and including the last line
// with a non-newline byte.
func bodyLineCount(blocks []string, lineStart []int) int {
	for j := len(blocks) - 1; j >= 0; j-- {
		if body := strings.TrimRight(blocks[j], "\n"); body != "" {
			return lineStart[j+1] - (len(blocks[j]) - len(body))
		}
	}
	return 0
}

// joinPos is a byte position in strings.Join(blocks, "\n"): off bytes into
// blocks[block], where off == len(blocks[block]) is the separator after it.
type joinPos struct {
	block, off int
}

// chunk returns the unconsumed bytes of the current block, the separator
// "\n" at a block end, or "" at the end of the joined string.
func (p joinPos) chunk(blocks []string) string {
	if p.off < len(blocks[p.block]) {
		return blocks[p.block][p.off:]
	}
	if p.block+1 < len(blocks) {
		return "\n"
	}
	return ""
}

// advance consumes n > 0 bytes of the current chunk.
func (p *joinPos) advance(blocks []string, n int) {
	if p.off < len(blocks[p.block]) {
		p.off += n
		return
	}
	p.block, p.off = p.block+1, 0
}

// joinedCommonPrefix compares strings.Join(a, "\n") with strings.Join(b,
// "\n"), whose first from blocks are identical, and returns the end of their
// longest common prefix as a position in b and whether the joined strings are
// equal. Both block lists are non-empty.
func joinedCommonPrefix(a, b []string, from int) (joinPos, bool) {
	var pa joinPos
	if from > 0 {
		pa = joinPos{block: from - 1, off: len(a[from-1])}
	}
	pb := pa
	for {
		x, y := pa.chunk(a), pb.chunk(b)
		if x == "" || y == "" {
			return pb, x == y
		}
		n := commonPrefixLen(x, y)
		if n > 0 {
			pb.advance(b, n)
		}
		if n < len(x) && n < len(y) {
			return pb, false
		}
		pa.advance(a, n)
	}
}

// commonPrefixLen returns the length of the longest common prefix of a and b.
// Block size doubles while blocks match and halves when one differs, so an
// unchanged transcript (often the same backing array, which memequal
// short-circuits) costs a few dozen comparisons rather than one per block.
func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	i, chunk := 0, commonPrefixChunk
	for chunk >= commonPrefixMinChunk {
		if i+chunk <= n && a[i:i+chunk] == b[i:i+chunk] {
			i += chunk
			chunk *= 2
		} else {
			chunk /= 2
		}
	}
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}
