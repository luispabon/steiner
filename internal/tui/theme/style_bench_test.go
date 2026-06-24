package theme

import (
	"strings"
	"testing"
)

var benchSink string

func ansiStyledContent(n int) string {
	line := "\x1b[1;32mHello\x1b[0m world \x1b[34mfoo\x1b[0m bar \x1b[0m" + strings.Repeat("x", 60)
	return strings.Repeat(line+"\n", n)
}

// BenchmarkWithBg measures WithBg on 80 lines of ANSI-styled content (~5000 chars).
func BenchmarkWithBg(b *testing.B) {
	input := ansiStyledContent(80)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = WithBg(input, "#1e1e1e")
	}
}

// BenchmarkPadLines measures PadLines on 80 lines of ANSI-styled content.
func BenchmarkPadLines(b *testing.B) {
	input := ansiStyledContent(80)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = PadLines(input, 80, "#1e1e1e")
	}
}

// BenchmarkWithBgThenPadLines measures WithBg then PadLines combined.
func BenchmarkWithBgThenPadLines(b *testing.B) {
	input := ansiStyledContent(80)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		withBg := WithBg(input, "#1e1e1e")
		benchSink = PadLines(withBg, 80, "#1e1e1e")
	}
}
