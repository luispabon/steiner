package tui

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/tui/theme"
)

var streamPreviewTokens = []string{
	"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog", "a", "I",
	"internationalization", "supercalifragilisticexpialidocious-and-more",
	"well-known", "re-run", "--flag", "-", "--", "a-b-c-d", "end-",
	" ", "  ", "     ", " ", " ", " ", " ", " ",
	"\n", "\n\n", "\n", "  \n", "\n   indented",
	"\t", "\tcode\t", "x\ty",
	"日本語のテキスト", "漢字", "한국어", "ｗｉｄｅ",
	"👨‍👩‍👧‍👦", "👍🏽", "🇺🇸", "é", "ạ̈", "🏳️‍🌈", " nbsp ", "　",
	"`code`", "**bold**", "1.", "- item",
}

var streamPreviewControlTokens = []string{"\x1b[31m", "\x1b[0m", "\r\n", "\r", "\x1b]8;;http://x\x07", "\x07"}

func randomStream(rng *rand.Rand, tokens []string, n int) string {
	var sb strings.Builder
	for range n {
		sb.WriteString(tokens[rng.IntN(len(tokens))])
	}
	return sb.String()
}

func randomChunks(rng *rand.Rand, s string, maxRunes int) []string {
	var chunks []string
	for len(s) > 0 {
		n := 0
		for k := rng.IntN(maxRunes) + 1; k > 0 && n < len(s); k-- {
			_, size := utf8.DecodeRuneInString(s[n:])
			n += size
		}
		chunks = append(chunks, s[:n])
		s = s[n:]
	}
	return chunks
}

func fullPreview(b *contentBuffer, width int) string {
	preview := strings.TrimRight(b.streamBuffer, "\n")
	if strings.TrimSpace(preview) == "" {
		return ""
	}
	return b.styles.AssistantProse.Width(max(1, width)).Render(preview) + "\n"
}

func newPreviewBuffer(palette theme.Palette) *contentBuffer {
	s := theme.BuildStyles("#ff8800", palette)
	return &contentBuffer{styles: &s}
}

func TestInProgressPreviewMatchesFullRender(t *testing.T) {
	widths := []int{0, 1, 2, 3, 5, 8, 17, 40, 200}
	tokenSets := map[string][]string{
		"text":     streamPreviewTokens,
		"controls": append(append([]string{}, streamPreviewTokens...), streamPreviewControlTokens...),
	}
	for name, tokens := range tokenSets {
		for _, width := range widths {
			for seed := uint64(1); seed <= 6; seed++ {
				t.Run(fmt.Sprintf("%s/w%d/seed%d", name, width, seed), func(t *testing.T) {
					t.Parallel()
					rng := rand.New(rand.NewPCG(seed, uint64(width)+7))
					b := newPreviewBuffer(theme.DefaultPalette())
					stream := randomStream(rng, tokens, 120)
					for i, chunk := range randomChunks(rng, stream, 6) {
						b.streamBuffer += chunk
						if got, want := b.inProgressPreview(width), fullPreview(b, width); got != want {
							t.Fatalf("chunk %d (%q): preview diverged\n got: %q\nwant: %q", i, chunk, got, want)
						}
					}
				})
			}
		}
	}
}

func TestInProgressPreviewSurvivesStateChanges(t *testing.T) {
	darker, err := theme.ResolvePalette("#101010", "#202020")
	if err != nil {
		t.Fatalf("resolve palette: %v", err)
	}
	for seed := uint64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, 99))
			b := newPreviewBuffer(theme.DefaultPalette())
			widths := []int{1, 2, 9, 30, 120}
			width := 30
			for i, chunk := range randomChunks(rng, randomStream(rng, streamPreviewTokens, 400), 8) {
				switch i % 25 {
				case 7:
					width = widths[rng.IntN(len(widths))]
				case 13:
					s := theme.BuildStyles("#00aaff", darker)
					b.styles = &s
				case 19:
					b.streamBuffer = ""
				}
				b.streamBuffer += chunk
				if got, want := b.inProgressPreview(width), fullPreview(b, width); got != want {
					t.Fatalf("chunk %d (width %d): preview diverged\n got: %q\nwant: %q", i, width, got, want)
				}
			}
		})
	}
}

func TestInProgressPreviewBlankBuffer(t *testing.T) {
	tests := []struct{ name, buffer string }{
		{"empty", ""},
		{"newlines", "\n\n"},
		{"spaces", "  \n "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newPreviewBuffer(theme.DefaultPalette())
			b.streamBuffer = tt.buffer
			if got := b.inProgressPreview(20); got != "" {
				t.Fatalf("blank buffer preview = %q, want empty", got)
			}
		})
	}
}

// TestFreshWrapStartsMatchFullWrap pins the resume rule itself: re-rendering
// from every line start that freshWrapStart accepts must reproduce the
// remaining lines of a full render.
func TestFreshWrapStartsMatchFullWrap(t *testing.T) {
	for _, width := range []int{1, 2, 3, 4, 6, 10, 25} {
		for seed := uint64(1); seed <= 40; seed++ {
			t.Run(fmt.Sprintf("w%d/seed%d", width, seed), func(t *testing.T) {
				rng := rand.New(rand.NewPCG(seed, uint64(width)))
				text := strings.TrimRight(randomStream(rng, streamPreviewTokens, 60), "\n")
				if strings.TrimSpace(text) == "" {
					return
				}
				b := newPreviewBuffer(theme.DefaultPalette())
				style := b.styles.AssistantProse
				full, starts, ok := renderTail(style, text, width)
				if !ok {
					return
				}
				for r := range full {
					end := len(text)
					if r+1 < len(starts) {
						end = starts[r+1]
					}
					if !freshWrapStart(text, starts[r], end) {
						continue
					}
					tail, _, ok := renderTail(style, text[starts[r]:], width)
					if !ok {
						continue
					}
					if got, want := strings.Join(tail, "\n"), strings.Join(full[r:], "\n"); got != want {
						t.Fatalf("resume at line %d (offset %d) of %q diverged\n got: %q\nwant: %q", r, starts[r], text, got, want)
					}
				}
			})
		}
	}
}

func TestStreamPreviewRendersBoundedTail(t *testing.T) {
	const width = 80
	var c streamPreviewCache
	b := newPreviewBuffer(theme.DefaultPalette())
	style := b.styles.AssistantProse
	text := strings.Repeat("streamed words here ", 400)
	c.render(style, text, width)
	for range 20 {
		text += "and some more streamed words, "
		if got, want := c.render(style, text, width), style.Width(width).Render(text)+"\n"; got != want {
			t.Fatalf("preview diverged from full render")
		}
		if rerendered := len(text) - c.starts[max(0, len(c.lines)-3)]; rerendered > 4*width {
			t.Fatalf("re-rendered %d bytes of a %d byte paragraph, want at most %d", rerendered, len(text), 4*width)
		}
	}
	if resume := len(text) - c.starts[c.resumeLine(text)]; resume > 4*width {
		t.Fatalf("resume point is %d bytes from the end, want at most %d", resume, 4*width)
	}
}
