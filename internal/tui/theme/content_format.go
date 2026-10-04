package theme

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// commonPrefixChunk is the block size compared with string equality (a
// vectorised memequal) before falling back to a byte scan.
const commonPrefixChunk = 4096

// FormatContent returns PadLines(WithBg(s, bg), width, bg) in a single
// line-by-line pass. Every line except the trailing empty ones is a body line
// (background restored, then padded); trailing empty lines are padded raw.
func FormatContent(s string, width int, bg string) string {
	f := newContentFormatter(width, bg)
	var sb strings.Builder
	sb.Grow(f.sizeHint(s))
	f.writeContent(&sb, s)
	return sb.String()
}

// ReformatContent returns FormatContent(s, width, bg), reusing the formatted
// lines s shares with prev. prevFormatted must be FormatContent(prev, width,
// bg); only the lines from the first differing one onwards are formatted.
func ReformatContent(prev, prevFormatted, s string, width int, bg string) string {
	common := commonPrefixLen(prev, s)
	if common == len(prev) && common == len(s) {
		return prevFormatted
	}
	reuse := sharedBodyPrefix(prev, s, common)
	if reuse == 0 {
		return FormatContent(s, width, bg)
	}
	// Formatting never adds or removes '\n', so the k-th newline of s maps to
	// the k-th newline of prevFormatted.
	pos := -1
	for range strings.Count(s[:reuse], "\n") {
		next := strings.IndexByte(prevFormatted[pos+1:], '\n')
		if next < 0 {
			return FormatContent(s, width, bg)
		}
		pos += next + 1
	}
	f := newContentFormatter(width, bg)
	tail := s[reuse:]
	var sb strings.Builder
	sb.Grow(pos + 1 + f.sizeHint(tail))
	sb.WriteString(prevFormatted[:pos+1])
	f.writeContent(&sb, tail)
	return sb.String()
}

// sharedBodyPrefix returns the length of the longest run of whole lines
// (including their '\n') within the first common bytes that a and b share and
// that are body lines in both, i.e. each string has a non-newline byte after
// the run. Because those lines are body lines on both sides, their formatting
// is identical in a and b.
func sharedBodyPrefix(a, b string, common int) int {
	limit := min(common, len(strings.TrimRight(a, "\n"))-1, len(strings.TrimRight(b, "\n"))-1)
	if limit <= 0 {
		return 0
	}
	return strings.LastIndexByte(b[:limit], '\n') + 1
}

// commonPrefixLen returns the length of the longest common prefix of a and b.
func commonPrefixLen(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i+commonPrefixChunk <= n && a[i:i+commonPrefixChunk] == b[i:i+commonPrefixChunk] {
		i += commonPrefixChunk
	}
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// contentFormatter formats content lines for one (width, bg) pair, memoising
// the rendered padding per pad length.
type contentFormatter struct {
	bgSeq    string
	width    int
	padStyle lipgloss.Style
	pads     []string
}

func newContentFormatter(width int, bg string) *contentFormatter {
	f := &contentFormatter{bgSeq: bgEscape(bg), width: width}
	if width >= 1 {
		f.padStyle = lipgloss.NewStyle().Background(lipgloss.Color(bg))
		f.pads = make([]string, width+1)
	}
	return f
}

func (f *contentFormatter) sizeHint(s string) int {
	return len(s) + (strings.Count(s, "\n")+1)*(len(f.bgSeq)+16)
}

// pad returns the background-styled padding of n spaces, identical to what
// PadLines appends.
func (f *contentFormatter) pad(n int) string {
	if f.pads[n] == "" {
		f.pads[n] = f.padStyle.Render(strings.Repeat(" ", n))
	}
	return f.pads[n]
}

// writeContent writes the formatted form of s: body lines up to the last
// non-newline byte, then each trailing empty line padded without background
// restoration, matching WithBg's raw re-append of trailing newlines.
func (f *contentFormatter) writeContent(sb *strings.Builder, s string) {
	body := strings.TrimRight(s, "\n")
	if body == "" {
		f.writePadding(sb, sb.Len())
	} else {
		for rest := body; ; {
			line, after, found := strings.Cut(rest, "\n")
			f.writeLine(sb, line)
			if !found {
				break
			}
			sb.WriteByte('\n')
			rest = after
		}
	}
	for range len(s) - len(body) {
		sb.WriteByte('\n')
		f.writePadding(sb, sb.Len())
	}
}

// writeLine writes one body line: background re-applied at the line start and
// after every reset (an empty line becomes a single background-filled space),
// then padded to width.
func (f *contentFormatter) writeLine(sb *strings.Builder, line string) {
	start := sb.Len()
	switch {
	case f.bgSeq == "":
		sb.WriteString(line)
	case line == "":
		sb.WriteString(f.bgSeq)
		sb.WriteByte(' ')
		sb.WriteString(ansiResetLong)
	default:
		writeLineWithBg(sb, line, f.bgSeq)
	}
	f.writePadding(sb, start)
}

// writePadding pads the line written since start out to width, measuring the
// formatted bytes exactly as PadLines does.
func (f *contentFormatter) writePadding(sb *strings.Builder, start int) {
	if f.width < 1 {
		return
	}
	if w := lipgloss.Width(sb.String()[start:]); w < f.width {
		sb.WriteString(f.pad(f.width - w))
	}
}

// writeLineWithBg is WithBg's scan for a single non-empty line.
func writeLineWithBg(sb *strings.Builder, line, bgSeq string) {
	sb.WriteString(bgSeq)
	for {
		i := strings.IndexByte(line, '\x1b')
		if i < 0 {
			sb.WriteString(line)
			return
		}
		sb.WriteString(line[:i])
		line = line[i:]
		n := 0
		switch {
		case strings.HasPrefix(line, ansiBgReset):
			n = len(ansiBgReset)
		case strings.HasPrefix(line, ansiResetLong):
			n = len(ansiResetLong)
		case strings.HasPrefix(line, ansiResetShort):
			n = len(ansiResetShort)
		}
		if n == 0 {
			sb.WriteByte('\x1b')
			line = line[1:]
			continue
		}
		sb.WriteString(line[:n])
		sb.WriteString(bgSeq)
		line = line[n:]
	}
}
