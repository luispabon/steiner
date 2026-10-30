package tui

import (
	"math/rand/v2"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestRenderGroupBoxMultiFrameMatchesStyledBox runs deterministic frame
// sequences through one persistent cache per run and compares every frame with
// renderStyledBox. Each frame either redraws the content or changes one line of
// the previous frame, so refused seeds, warm per-line draws, and cached lines
// all meet. The atoms cover tabs, CR, CRLF, open SGR, wrapping, and wide runes.
func TestRenderGroupBoxMultiFrameMatchesStyledBox(t *testing.T) {
	t.Parallel()
	border, bg := lipgloss.Color("#444444"), lipgloss.Color("#101010")
	atoms := []string{
		"plain words here",
		"col1\tcol2\tcol3",
		"prog 10%\rprog 100%",
		"trail\r",
		"\r",
		"\r\r x",
		"a\r\rb",
		"\x1b[0m",
		"\x1b[1mbold\x1b[0m done",
		"\x1b[31mred\x1b[m",
		"\x1b[31mred",
		"mid \x1b[32m green",
		strings.Repeat("longword", 8),
		"wide 日本語日本語日本語日本語日本語日本語",
		"  ",
		"",
		"tab\tat end\t",
		"\ttab start",
		"\t",
		"a very long line with many words that must wrap across several rows of the box",
		" " + strings.Repeat("x", 60),
		"emoji 👍🏽 text and more text to wrap around the edge",
		"x\r ",
		"\rlead",
		"crlf line\r\n",
		"\r\n",
		"x\r\r\ny",
	}
	rng := rand.New(rand.NewPCG(7, 11))
	// raceSample keeps the narrowest width and raceSeeds keeps one run under
	// -race; every width and all forty runs still execute in a normal build.
	for _, width := range raceSample([]int{6, 7, 10, 25, 40, 81, 120}) {
		for run := range int(raceSeeds(40)) {
			var cache groupBoxCache
			var cur []string
			for frame := range 8 {
				if frame == 0 || rng.IntN(3) == 0 {
					cur = make([]string, 1+rng.IntN(8))
					for j := range cur {
						cur[j] = atoms[rng.IntN(len(atoms))]
					}
				} else {
					cur[rng.IntN(len(cur))] = atoms[rng.IntN(len(atoms))]
				}
				content := strings.Join(cur, "\n")
				want := renderStyledBox(content, border, bg, width)
				if got := renderGroupBox(&cache, content, border, bg, width); got != want {
					t.Fatalf("width %d run %d frame %d content %q:\n got %q\nwant %q", width, run, frame, content, got, want)
				}
			}
		}
	}
}
