package theme

import (
	"math/rand/v2"
	"strings"
	"testing"
)

var (
	formatTestWidths = []int{-1, 0, 1, 4, 13, 40}
	formatTestBgs    = []string{"#1e1e2e", "", "#abc"}
	formatTestPieces = []string{
		"plain", "word ", " ", "", "\t", "x\ty", "世界", "🎉", "👩‍💻", "é",
		"\x1b[0m", "\x1b[m", "\x1b[49m", "\x1b[1;31m", "\x1b[38;2;1;2;3m",
		"\x1b[", "\x1b[4", "\x1b[0", "\x1b[49", "\x1b",
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\",
		"a long run of words that overflows the narrow widths",
	}
)

func oldFormat(s string, width int, bg string) string {
	return PadLines(WithBg(s, bg), width, bg)
}

func randomFormatLine(r *rand.Rand) string {
	var sb strings.Builder
	for range r.IntN(5) {
		sb.WriteString(formatTestPieces[r.IntN(len(formatTestPieces))])
	}
	return sb.String()
}

func randomFormatContent(r *rand.Rand) string {
	lines := make([]string, r.IntN(12))
	for i := range lines {
		lines[i] = randomFormatLine(r)
	}
	return strings.Join(lines, "\n") + strings.Repeat("\n", r.IntN(4))
}

func TestFormatContentMatchesPadLinesWithBg(t *testing.T) {
	fixed := []string{
		"", "\n", "\n\n\n", "a", "a\n", "a\n\n\n", "\na", "\n\na\n\nb\n",
		"\x1b[0m", "\x1b[0m\n", "\x1b[m\x1b[49m\n\x1b[0m", "x\x1b[0m\x1b[m\x1b[49m",
		"\x1b[", "a\x1b[\nb\x1b[4\nc\x1b[0", "\t\ttab", "世界🎉\n👩‍💻",
	}
	for _, s := range fixed {
		for _, width := range formatTestWidths {
			for _, bg := range formatTestBgs {
				if got, want := FormatContent(s, width, bg), oldFormat(s, width, bg); got != want {
					t.Errorf("FormatContent(%q, %d, %q)\n got %q\nwant %q", s, width, bg, got, want)
				}
			}
		}
	}
	for seed := range uint64(2000) {
		r := rand.New(rand.NewPCG(seed, 1))
		s := randomFormatContent(r)
		width := formatTestWidths[r.IntN(len(formatTestWidths))]
		bg := formatTestBgs[r.IntN(len(formatTestBgs))]
		if got, want := FormatContent(s, width, bg), oldFormat(s, width, bg); got != want {
			t.Fatalf("seed %d: FormatContent(%q, %d, %q)\n got %q\nwant %q", seed, s, width, bg, got, want)
		}
	}
}

// editFormatContent applies one random transcript edit: append, tail edit,
// mid insert/delete, head edit, or trailing-newline change.
func editFormatContent(r *rand.Rand, s string) string {
	lines := strings.Split(s, "\n")
	switch r.IntN(7) {
	case 0:
		lines = append(lines, randomFormatLine(r))
	case 1:
		lines[len(lines)-1] = randomFormatLine(r)
	case 2:
		i := r.IntN(len(lines))
		lines = append(lines[:i], append([]string{randomFormatLine(r)}, lines[i:]...)...)
	case 3:
		if len(lines) > 1 {
			i := r.IntN(len(lines))
			lines = append(lines[:i], lines[i+1:]...)
		}
	case 4:
		lines[0] = randomFormatLine(r)
	case 5:
		return strings.TrimRight(s, "\n") + strings.Repeat("\n", r.IntN(4))
	default:
		lines = lines[:r.IntN(len(lines))+1]
	}
	return strings.Join(lines, "\n")
}

func TestReformatContentMatchesPadLinesWithBg(t *testing.T) {
	for seed := range uint64(500) {
		r := rand.New(rand.NewPCG(seed, 2))
		width := formatTestWidths[r.IntN(len(formatTestWidths))]
		bg := formatTestBgs[r.IntN(len(formatTestBgs))]
		prev := randomFormatContent(r)
		prevOut := FormatContent(prev, width, bg)
		for step := range 20 {
			s := editFormatContent(r, prev)
			got := ReformatContent(prev, prevOut, s, width, bg)
			if want := oldFormat(s, width, bg); got != want {
				t.Fatalf("seed %d step %d: ReformatContent(%q -> %q, %d, %q)\n got %q\nwant %q", seed, step, prev, s, width, bg, got, want)
			}
			prev, prevOut = s, got
		}
	}
}

func TestReformatContentReusesSharedLines(t *testing.T) {
	const marker = "REUSED"
	tests := []struct {
		name       string
		prev, s    string
		wantMarked int
	}{
		{name: "unchanged", prev: "a\nb\n", s: "a\nb\n", wantMarked: 3},
		{name: "tail edited", prev: "a\nb\nc", s: "a\nb\nC", wantMarked: 2},
		{name: "line appended", prev: "a\nb", s: "a\nb\nc", wantMarked: 1},
		{name: "head edited", prev: "a\nb\nc", s: "A\nb\nc", wantMarked: 0},
		{name: "prev trailing empty lines are not body lines", prev: "a\n\n", s: "a\n\nb", wantMarked: 0},
		{name: "new trailing empty lines are not body lines", prev: "a\n\nb", s: "a\n\n", wantMarked: 0},
		{name: "empty body lines reused", prev: "a\n\nb", s: "a\n\nc", wantMarked: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Poison prevFormatted line by line; poisoned lines that survive
			// prove reuse, and the rest must match the oracle exactly.
			lines := strings.Split(FormatContent(tt.prev, 10, "#000000"), "\n")
			for i := range lines {
				lines[i] = marker
			}
			got := strings.Split(ReformatContent(tt.prev, strings.Join(lines, "\n"), tt.s, 10, "#000000"), "\n")
			want := strings.Split(oldFormat(tt.s, 10, "#000000"), "\n")
			if len(got) != len(want) {
				t.Fatalf("got %d lines, want %d", len(got), len(want))
			}
			for i := range got {
				wantLine := want[i]
				if i < tt.wantMarked {
					wantLine = marker
				}
				if got[i] != wantLine {
					t.Errorf("line %d = %q, want %q", i, got[i], wantLine)
				}
			}
		})
	}
}

func TestSharedBodyPrefixBoundsWork(t *testing.T) {
	lines := make([]string, 700)
	for i := range lines {
		lines[i] = strings.Repeat("settled transcript line ", 4)
	}
	prev := strings.Join(lines, "\n") + "\nrunning card ⠋ 1s"
	s := strings.Join(lines, "\n") + "\nrunning card ⠙ 2s"
	reused := sharedBodyPrefix(prev, s, commonPrefixLen(prev, s))
	if fresh := len(s) - reused; fresh*10 >= len(s) {
		t.Fatalf("formatter fed %d of %d bytes, want < 10%%", fresh, len(s))
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
}
