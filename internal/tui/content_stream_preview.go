package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// tabSpaces is lipgloss's default tab expansion, which the wrap mapping must
// mirror to line up wrapped output with source offsets.
const tabSpaces = 4

// streamPreviewCache keeps the styled, padded lines of the last in-progress
// preview so a growing stream only re-renders its tail. It is content-keyed:
// a render is reused only while the new text still starts with source, so
// buffer resets need no invalidation.
type streamPreviewCache struct {
	probe  string
	width  int
	source string
	lines  []string
	starts []int // byte offset in source where each line begins
}

// render returns style.Width(width).Render(text), byte for byte.
//
// ansi.Wrap is a single forward pass that only appends to its output, so every
// line before the last is final once the text grows. A fresh wrap started at a
// line start reproduces the full run's state there when curWidth is 0 and no
// partial word is carried; the one carried-word divergence is a hyphen (a
// breakpoint behaves differently at curWidth 0), so line starts whose first
// word contains '-' are skipped unless they follow a hard newline. The last
// line is always re-rendered, plus the one before it because the newline that
// starts the last line can be triggered by a grapheme cluster that was still
// incomplete.
func (c *streamPreviewCache) render(style lipgloss.Style, text string, width int) string {
	probe := style.Render("x")
	valid := c.width == width && c.probe == probe && len(c.lines) > 0 && strings.HasPrefix(text, c.source)
	scanFrom := 0
	if valid {
		scanFrom = len(c.source)
	}
	if !wrapSafe(text[scanFrom:]) {
		c.reset()
		return style.Width(width).Render(text) + "\n"
	}

	keep, from := 0, 0
	if valid {
		keep = c.resumeLine(text)
		from = c.starts[keep]
	}
	lines, starts, ok := renderTail(style, text[from:], width)
	if !ok && from > 0 {
		keep, from = 0, 0
		lines, starts, ok = renderTail(style, text, width)
	}
	if !ok {
		c.reset()
		return style.Width(width).Render(text) + "\n"
	}

	for i := range starts {
		starts[i] += from
	}
	c.probe, c.width, c.source = probe, width, text
	c.lines = append(c.lines[:keep], lines...)
	c.starts = append(c.starts[:keep], starts...)
	return strings.Join(c.lines, "\n") + "\n"
}

func (c *streamPreviewCache) reset() {
	c.source, c.lines, c.starts = "", c.lines[:0], c.starts[:0]
}

// resumeLine picks the line to restart rendering from: the second-to-last, or
// the nearest earlier one that is safe to start a fresh wrap at.
func (c *streamPreviewCache) resumeLine(text string) int {
	r := max(0, len(c.lines)-2)
	for ; r > 0; r-- {
		end := len(text)
		if r+1 < len(c.starts) {
			end = c.starts[r+1]
		}
		if freshWrapStart(text, c.starts[r], end) {
			return r
		}
	}
	return 0
}

// freshWrapStart reports whether wrapping can restart at text[s:] and match a
// run that began earlier.
func freshWrapStart(text string, s, end int) bool {
	if s == 0 || text[s-1] == '\n' {
		return true
	}
	if r, _ := utf8.DecodeRuneInString(text[s:]); unicode.IsSpace(r) {
		return false
	}
	word := text[s:end]
	if i := strings.IndexAny(word, " \n"); i >= 0 {
		word = word[:i]
	}
	return !strings.Contains(word, "-")
}

// wrapSafe reports whether s is free of bytes whose rendering depends on
// state outside the line (escape sequences, carriage returns, other controls).
func wrapSafe(s string) bool {
	for i := 0; i < len(s); i++ {
		if ch := s[i]; (ch < 0x20 && ch != '\n' && ch != '\t') || ch == 0x7f {
			return false
		}
	}
	return true
}

// renderTail renders tail and maps each output line to its start offset in
// tail. ok is false when the output is not safe to splice: a line wider than
// width makes lipgloss pad every line to a different width.
func renderTail(style lipgloss.Style, tail string, width int) (lines []string, starts []int, ok bool) {
	lines = strings.Split(style.Width(width).Render(tail), "\n")
	if ansi.StringWidth(lines[0]) != width {
		return nil, nil, false
	}
	conv := tail
	hasTab := strings.IndexByte(tail, '\t') >= 0
	if hasTab {
		conv = strings.ReplaceAll(tail, "\t", strings.Repeat(" ", tabSpaces))
	}
	starts, ok = wrapStarts(conv, ansi.Wrap(conv, width, ""))
	if !ok || len(starts) != len(lines) {
		return nil, nil, false
	}
	if hasTab {
		starts, ok = rawOffsets(tail, starts)
	}
	return lines, starts, ok
}

// wrapStarts maps each line of wrapped (ansi.Wrap output of src) to the offset
// in src where it begins. Wrapping only inserts newlines and drops whitespace
// around them, so a lockstep walk recovers the offsets.
func wrapStarts(src, wrapped string) ([]int, bool) {
	starts := []int{0}
	i, j := 0, 0
	for i < len(wrapped) {
		switch {
		case j < len(src) && wrapped[i] == src[j]:
			i++
			j++
			if wrapped[i-1] == '\n' {
				starts = append(starts, j)
			}
		case j < len(src) && isSpaceAt(src, j):
			_, size := utf8.DecodeRuneInString(src[j:])
			j += size
		case wrapped[i] == '\n':
			starts = append(starts, j)
			i++
		default:
			return nil, false
		}
	}
	for j < len(src) {
		if !isSpaceAt(src, j) {
			return nil, false
		}
		_, size := utf8.DecodeRuneInString(src[j:])
		j += size
	}
	return starts, true
}

func isSpaceAt(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return unicode.IsSpace(r)
}

// rawOffsets converts ascending offsets in the tab-expanded form of raw back
// to offsets in raw.
func rawOffsets(raw string, conv []int) ([]int, bool) {
	out := make([]int, len(conv))
	j, c := 0, 0
	for k, target := range conv {
		for c < target && j < len(raw) {
			if raw[j] == '\t' {
				c += tabSpaces
			} else {
				c++
			}
			j++
		}
		if c != target {
			return nil, false
		}
		out[k] = j
	}
	return out, true
}
