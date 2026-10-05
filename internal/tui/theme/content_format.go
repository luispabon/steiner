package theme

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

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

// FormatBody formats s like FormatContent but treats every line as a body
// line, so trailing empty lines get background restoration too. It formats a
// slice taken from the middle of a larger transcript.
func FormatBody(s string, width int, bg string) string {
	f := newContentFormatter(width, bg)
	var sb strings.Builder
	sb.Grow(f.sizeHint(s))
	f.writeBody(&sb, s)
	return sb.String()
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
		f.writeBody(sb, body)
	}
	for range len(s) - len(body) {
		sb.WriteByte('\n')
		f.writePadding(sb, sb.Len())
	}
}

// writeBody writes every line of s as a body line.
func (f *contentFormatter) writeBody(sb *strings.Builder, s string) {
	for rest := s; ; {
		line, after, found := strings.Cut(rest, "\n")
		f.writeLine(sb, line)
		if !found {
			return
		}
		sb.WriteByte('\n')
		rest = after
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
	if f.width < 1 {
		return
	}
	w, ok := simpleWidth(line)
	switch {
	case line == "" && f.bgSeq != "":
		w = 1
	case !ok:
		w = lipgloss.Width(sb.String()[start:])
	}
	f.padTo(sb, w)
}

// writePadding pads the line written since start out to width, measuring the
// formatted bytes exactly as PadLines does.
func (f *contentFormatter) writePadding(sb *strings.Builder, start int) {
	if f.width < 1 {
		return
	}
	f.padTo(sb, lipgloss.Width(sb.String()[start:]))
}

func (f *contentFormatter) padTo(sb *strings.Builder, w int) {
	if w < f.width {
		sb.WriteString(f.pad(f.width - w))
	}
}

// simpleWidth returns the display width of line when it holds only printable
// ASCII, C0/DEL controls (zero cells), a vetted set of single-cell non-ASCII
// runes, and complete CSI sequences (zero cells). Background re-insertion only
// adds zero-width SGR sequences after such resets, so the source width equals
// the formatted width. Anything else reports false.
func simpleWidth(line string) (int, bool) {
	w := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c >= 0x20 && c <= 0x7e:
			w++
		case c == 0x1b:
			if i+1 >= len(line) || line[i+1] != '[' {
				return 0, false
			}
			i += 2
			for i < len(line) && line[i] >= 0x30 && line[i] <= 0x3f {
				i++
			}
			if i == len(line) || line[i] < 0x40 || line[i] > 0x7e {
				return 0, false
			}
		case c < 0x20 || c == 0x7f:
		default:
			r, size := utf8.DecodeRuneInString(line[i:])
			if !singleCell(r) {
				return 0, false
			}
			w++
			i += size - 1
		}
	}
	return w, true
}

// singleCell reports whether r is a non-ASCII rune that always occupies one
// cell and never joins a grapheme cluster: Latin supplements, general
// punctuation, arrows, box drawing and block elements, and common symbols.
func singleCell(r rune) bool {
	switch {
	case r >= 0xa1 && r <= 0x2ff:
		return r != 0xad
	case r >= 0x2010 && r <= 0x2027:
		return true
	case r >= 0x2190 && r <= 0x21ff:
		return true
	case r >= 0x2500 && r <= 0x259f:
		return true
	case r == 0x25b8 || r == 0x2713 || r == 0xa0:
		return true
	}
	return false
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
