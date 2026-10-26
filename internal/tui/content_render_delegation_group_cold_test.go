package tui

import (
	"math/rand/v2"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// groupBoxLeadingSpaceLine starts with a space and then holds a word wider than
// the inner width at width 23. lipgloss widens the whole box by one column.
const groupBoxLeadingSpaceLine = " supercalifragilisticexpialidocious"

// TestRenderGroupBoxSeedPathSelection checks which path a render takes and
// whether it leaves a seeded cache. Every output must equal renderStyledBox.
func TestRenderGroupBoxSeedPathSelection(t *testing.T) {
	t.Parallel()
	border := lipgloss.Color("#ff5f00")
	bg := lipgloss.Color("#101010")
	const width = 20
	wrapping := "a line long enough that it wraps inside the box"
	tests := []struct {
		name      string
		prime     string
		content   string
		wantCache bool
	}{
		{"cold unwrapped seeds", "", "one\ntwo", true},
		{"cold wrapped seeds", "", wrapping + "\nshort", true},
		{"cold open SGR does not seed", "", "\x1b[31mopen\nnext", false},
		{"cold tab does not seed", "", "a\tb\nnext", false},
		{"partial miss keeps line path", "one\ntwo\nthree\nfour", "one\n" + wrapping + "\nthree\nfour", true},
		{"primed high miss ratio keeps line path", "one\ntwo", "\x1b[31mopen\nthree\nfour", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cache groupBoxCache
			if tc.prime != "" {
				renderGroupBox(&cache, tc.prime, border, bg, width)
			}
			got := renderGroupBox(&cache, tc.content, border, bg, width)
			if want := renderStyledBox(tc.content, border, bg, width); got != want {
				t.Errorf("output differs from renderStyledBox:\n got %q\nwant %q", got, want)
			}
			if (cache.lines != nil) != tc.wantCache {
				t.Errorf("cache populated = %v, want %v", cache.lines != nil, tc.wantCache)
			}
		})
	}
}

// TestRenderGroupBoxLeadingSpaceUnbreakableWord covers a line that lipgloss widens.
// The cold and partial-miss paths must both fall back to the full box.
func TestRenderGroupBoxLeadingSpaceUnbreakableWord(t *testing.T) {
	t.Parallel()
	border := lipgloss.Color("#ff5f00")
	bg := lipgloss.Color("#101010")
	const width = 23
	if w := ansi.StringWidth(strings.Split(renderStyledBox(groupBoxLeadingSpaceLine, border, bg, width), "\n")[0]); w == width {
		t.Fatalf("premise failed: renderStyledBox row is %d columns, want it widened past %d", w, width)
	}
	tests := []struct {
		name    string
		prime   string
		content string
	}{
		{"cold", "", "one\n" + groupBoxLeadingSpaceLine},
		{"partial miss", "one\ntwo\nthree\nfour", "one\ntwo\nthree\n" + groupBoxLeadingSpaceLine},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cache groupBoxCache
			if tc.prime != "" {
				renderGroupBox(&cache, tc.prime, border, bg, width)
			}
			want := renderStyledBox(tc.content, border, bg, width)
			if got := renderGroupBox(&cache, tc.content, border, bg, width); got != want {
				t.Errorf("output differs from renderStyledBox:\n got %q\nwant %q", got, want)
			}
		})
	}
}

// TestRenderGroupBoxRandomFramesMatchStyledBox runs a deterministic sequence of
// frames that share one cache. The width changes every 25 frames, so most frames
// take the warm and partial-miss paths.
func TestRenderGroupBoxRandomFramesMatchStyledBox(t *testing.T) {
	t.Parallel()
	border := lipgloss.Color("#ff5f00")
	bg := lipgloss.Color("#101010")
	pool := []string{
		"alpha",
		"",
		"探索作業 wide",
		"emoji 🙂 and 👨\u200d👩",
		"\x1b[31mred\x1b[0m",
		"\x1b[31mopen SGR",
		"tab\tcolumn",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
		" lead",
		groupBoxLeadingSpaceLine,
		strings.Repeat("w", 100),
		strings.Repeat("word ", 12),
	}
	rng := rand.New(rand.NewPCG(1, 2))
	var cache groupBoxCache
	width := 0
	for i := range 300 {
		if i%25 == 0 {
			width = rng.IntN(121)
		}
		lines := make([]string, 1+rng.IntN(5))
		for j := range lines {
			lines[j] = pool[rng.IntN(len(pool))]
		}
		content := strings.Join(lines, "\n")
		want := renderStyledBox(content, border, bg, width)
		if got := renderGroupBox(&cache, content, border, bg, width); got != want {
			t.Fatalf("frame %d width %d content %q:\n got %q\nwant %q", i, width, content, got, want)
		}
	}
}
