package theme

import (
	"math/rand/v2"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

func TestSimpleWidthMatchesLipglossWidth(t *testing.T) {
	alphabet := []string{
		"a", "Z", " ", "~", "\t", "─", "│", "·", "\u00a0", "–", "✓", "▸", "→", "\r", "\x00", "\x07", "\x7f", "\x1f", "\x80", "\xff",
		"\x1b", "\x1b[", "\x1b[0m", "\x1b[1;31m", "\x1b[38;2;1;2;3m", "\x1b[?25h", "\x1b[4:3m",
		"\x1b[1;", "\x1b[\x01", "\x1b[ q", "\x1b]0;t\x07", "\x1b[K", "\x1bM",
		"世", "🎉", "👩‍💻", "é", "\u200b", "\u200d",
	}
	fixed := []string{"", "plain text", "\x1b[0m", "\x1b[0m\x1b[m", "\x1b[", "x\ty", "x\ry", "\x7f", "a\x1b[31", "\x1b[31mred\x1b[0m"}
	check := func(line string) {
		t.Helper()
		if w, ok := simpleWidth(line); ok && w != lipgloss.Width(line) {
			t.Fatalf("simpleWidth(%q) = %d, lipgloss.Width = %d", line, w, lipgloss.Width(line))
		}
	}
	for _, line := range fixed {
		check(line)
	}
	accepted := 0
	for seed := range uint64(20000) {
		r := rand.New(rand.NewPCG(seed, 7))
		var sb strings.Builder
		for range r.IntN(8) {
			sb.WriteString(alphabet[r.IntN(len(alphabet))])
		}
		line := sb.String()
		check(line)
		if _, ok := simpleWidth(line); ok {
			accepted++
		}
	}
	if accepted == 0 {
		t.Fatal("fast path never accepted a random line")
	}
	for _, line := range []string{"世", "🎉", "e\u0301", "\u200d", "\ufe0f", "\x1b", "\x1b[3", "\x1b]0;t\x07", "\xff", "\u00ad"} {
		if _, ok := simpleWidth(line); ok {
			t.Errorf("simpleWidth(%q) accepted, want fallback", line)
		}
	}
}

func TestSingleCellRunesAreOneCellAndNeverJoin(t *testing.T) {
	for r := rune(0x80); r <= 0x2fff; r++ {
		if !singleCell(r) {
			continue
		}
		for _, s := range []string{string(r), "a" + string(r) + "a", string(r) + string(r)} {
			if got, want := lipgloss.Width(s), len([]rune(s)); got != want {
				t.Fatalf("singleCell(%U): lipgloss.Width(%q) = %d, want %d", r, s, got, want)
			}
		}
	}
}

func TestFormatBodyTreatsEveryLineAsBody(t *testing.T) {
	for seed := range uint64(1000) {
		r := rand.New(rand.NewPCG(seed, 5))
		s := randomFormatContent(r)
		width := formatTestWidths[r.IntN(len(formatTestWidths))]
		bg := formatTestBgs[r.IntN(len(formatTestBgs))]
		full := FormatContent(s+"\nz", width, bg)
		want := full[:strings.LastIndexByte(full, '\n')]
		if got := FormatBody(s, width, bg); got != want {
			t.Fatalf("seed %d: FormatBody(%q, %d, %q)\n got %q\nwant %q", seed, s, width, bg, got, want)
		}
	}
}
