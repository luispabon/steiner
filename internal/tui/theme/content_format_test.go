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
