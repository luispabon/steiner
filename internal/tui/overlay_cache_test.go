package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func oracleComposeCenteredOverlay(base, overlay string, width, height int) string {
	if width < 1 || height < 1 {
		return base
	}

	baseLines := oracleNormalizeOverlayLines(base, width, height)
	overlayLines := strings.Split(overlay, "\n")
	overlayHeight := len(overlayLines)
	if overlayHeight == 0 {
		return strings.Join(baseLines, "\n")
	}

	maxOverlayWidth := 0
	for _, line := range overlayLines {
		maxOverlayWidth = max(maxOverlayWidth, lipgloss.Width(line))
	}
	if maxOverlayWidth == 0 {
		return strings.Join(baseLines, "\n")
	}

	startX := (width - maxOverlayWidth) / 2
	startY := (height - overlayHeight) / 2
	endY := min(height, startY+overlayHeight)
	for y := max(0, startY); y < endY; y++ {
		baseLines[y] = composeOverlayLine(baseLines[y], overlayLines[y-startY], width, startX, maxOverlayWidth)
	}

	return strings.Join(baseLines, "\n")
}

func oracleNormalizeOverlayLines(rendered string, width, height int) []string {
	lines := strings.Split(rendered, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	normalized := make([]string, height)
	for i := 0; i < height; i++ {
		if i < len(lines) {
			normalized[i] = padOverlayLine(ansi.Cut(lines[i], 0, width), width)
			continue
		}
		normalized[i] = strings.Repeat(" ", width)
	}
	return normalized
}

func testGridLines(n int, mk func(i int) string) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = mk(i)
	}
	return strings.Join(lines, "\n")
}

func TestComposeCenteredOverlayMatchesOracle(t *testing.T) {
	t.Parallel()

	styled := func(i int) string {
		return "\x1b[38;5;" + strconv.Itoa(i%200) + "m" + strings.Repeat("abc世界", 12) + "\x1b[0m"
	}
	trailing := func(_ int) string { return "\x1b[48;5;17m" + strings.Repeat("x", 30) + "\x1b[0m\x1b[0m" }
	wide := func(_ int) string { return strings.Repeat("世", 20) }
	short := func(_ int) string { return "ab" }
	long := func(_ int) string { return strings.Repeat("0123456789", 12) }
	plain := func(i int) string { return strings.Repeat(string(rune('a'+i%26)), 40) }

	type input struct {
		name          string
		base, overlay string
		w, h          int
	}
	box := func(w, h int) string {
		return testGridLines(h, func(i int) string {
			return "\x1b[44m" + strings.Repeat(string(rune('A'+i%26)), w) + "\x1b[0m"
		})
	}
	inputs := []input{
		{"even", testGridLines(20, styled), box(10, 4), 40, 20},
		{"odd", testGridLines(21, styled), box(9, 5), 41, 21},
		{"odd-even-mixed", testGridLines(20, styled), box(9, 5), 40, 20},
		{"wide-graphemes", testGridLines(10, wide), box(7, 3), 40, 10},
		{"wide-overlay", testGridLines(10, plain), testGridLines(3, wide), 40, 10},
		{"wide-cut-odd", testGridLines(10, wide), testGridLines(3, wide), 41, 10},
		{"trailing-resets", testGridLines(10, trailing), box(8, 4), 40, 10},
		{"short-rows", testGridLines(10, short), box(8, 4), 40, 10},
		{"long-rows", testGridLines(10, long), box(8, 4), 40, 10},
		{"base-fewer-rows", testGridLines(4, plain), box(8, 6), 40, 10},
		{"base-more-rows", testGridLines(30, plain), box(8, 6), 40, 10},
		{"overlay-wider", testGridLines(10, plain), box(60, 4), 40, 10},
		{"overlay-taller", testGridLines(10, plain), box(10, 25), 40, 10},
		{"overlay-bigger", testGridLines(10, plain), box(70, 25), 40, 10},
		{"overlay-exact", testGridLines(10, plain), box(40, 10), 40, 10},
		{"overlay-empty", testGridLines(10, plain), "", 40, 10},
		{"overlay-blank-width", testGridLines(10, plain), "\n\n", 40, 10},
		{"empty-base", "", box(8, 3), 40, 10},
		{"trailing-newline-base", testGridLines(5, plain) + "\n", box(8, 3), 40, 10},
		{"overlay-ragged", testGridLines(10, plain), "ab\nabcdefgh\n\x1b[1mabc\x1b[0m", 40, 10},
		{"tiny", testGridLines(3, plain), box(2, 1), 3, 3},
		{"zero-width", testGridLines(3, plain), box(2, 1), 0, 3},
		{"zero-height", testGridLines(3, plain), box(2, 1), 10, 0},
	}

	for _, in := range inputs {
		t.Run(in.name, func(t *testing.T) {
			t.Parallel()
			want := oracleComposeCenteredOverlay(in.base, in.overlay, in.w, in.h)
			var c overlayComposeCache
			for pass := range 2 {
				if got := c.compose(in.base, in.overlay, in.w, in.h, false); got != want {
					t.Fatalf("pass %d: composed output differs from oracle\nwant %q\ngot  %q", pass, want, got)
				}
			}
			if got := composeCenteredOverlay(in.base, in.overlay, in.w, in.h); got != want {
				t.Fatalf("composeCenteredOverlay differs from oracle")
			}
		})
	}
}

func TestOverlayComposeCacheSequenceMatchesOracle(t *testing.T) {
	t.Parallel()

	row := func(tag string) func(int) string {
		return func(i int) string {
			return "\x1b[3" + strconv.Itoa(i%7) + "m" + tag + strings.Repeat("世x", 15) + "\x1b[0m"
		}
	}
	box := func(tag string, w, h int) string {
		return testGridLines(h, func(_ int) string { return tag + strings.Repeat("#", w-len(tag)) })
	}
	baseA, baseB := testGridLines(20, row("A")), testGridLines(20, row("B"))
	// scrolled: rows shifted by one so most row strings repeat at other indices
	scrolled := strings.SplitN(baseA, "\n", 2)[1] + "\n" + row("Z")(0)
	ovA, ovB := box("a", 10, 5), box("b", 11, 4)

	steps := []struct {
		name          string
		base, overlay string
		w, h          int
	}{
		{"initial", baseA, ovA, 40, 20},
		{"same", baseA, ovA, 40, 20},
		{"base-only", baseB, ovA, 40, 20},
		{"scrolled", scrolled, ovA, 40, 20},
		{"overlay-only", scrolled, ovB, 40, 20},
		{"both", baseA, ovA, 40, 20},
		{"resize-narrower", baseA, ovA, 30, 20},
		{"resize-shorter", baseA, ovA, 30, 12},
		{"resize-back", baseA, ovA, 40, 20},
		{"overlay-wider-than-screen", baseA, box("w", 50, 5), 40, 20},
		{"overlay-taller-than-screen", baseA, box("t", 10, 30), 40, 20},
		{"after-oversized", baseB, ovA, 40, 20},
		{"parity-flip", baseB, ovA, 41, 21},
	}
	var c overlayComposeCache
	for _, s := range steps {
		want := oracleComposeCenteredOverlay(s.base, s.overlay, s.w, s.h)
		if got := c.compose(s.base, s.overlay, s.w, s.h, false); got != want {
			t.Fatalf("%s: differs from oracle\nwant %q\ngot  %q", s.name, want, got)
		}
	}
}

func TestOverlayComposeFullWidthMatchesOracle(t *testing.T) {
	t.Parallel()

	row := func(tag string) func(int) string {
		return func(i int) string {
			return "\x1b[3" + strconv.Itoa(i%7) + "m" + tag + strings.Repeat("世x", 18) + "\x1b[0m" + strings.Repeat(" ", 3)
		}
	}
	const w, h = 41 + 0, 20
	fix := func(s string) string { return oracleComposeCenteredOverlay(s, "", w, h) }
	baseA, baseB := fix(testGridLines(h, row("A"))), fix(testGridLines(h, row("B")))
	box := func(width, height int) string {
		return testGridLines(height, func(_ int) string { return "\x1b[44m" + strings.Repeat("o", width) + "\x1b[0m" })
	}
	var c overlayComposeCache
	for _, s := range []struct {
		name          string
		base, overlay string
		w, h          int
	}{
		{"initial", baseA, box(10, 5), w, h},
		{"base-only", baseB, box(10, 5), w, h},
		{"overlay-only", baseB, box(11, 4), w, h},
		{"overlay-wider", baseB, box(60, 4), w, h},
		{"overlay-taller", baseB, box(10, 30), w, h},
		{"overlay-empty", baseB, "", w, h},
		{"base-short-row-falls-back", "abc\n" + strings.TrimPrefix(baseA, strings.SplitN(baseA, "\n", 2)[0]+"\n"), box(10, 5), w, h},
		{"wrong-row-count-falls-back", baseA + "\nextra", box(10, 5), w, h},
	} {
		want := oracleComposeCenteredOverlay(s.base, s.overlay, s.w, s.h)
		if got := c.compose(s.base, s.overlay, s.w, s.h, true); got != want {
			t.Fatalf("%s: differs from oracle\nwant %q\ngot  %q", s.name, want, got)
		}
	}
}

func TestOverlayComposeCacheHits(t *testing.T) {
	t.Parallel()

	base := testGridLines(10, func(_ int) string { return strings.Repeat("b", 40) })
	overlay := testGridLines(3, func(_ int) string { return strings.Repeat("o", 8) })
	var c overlayComposeCache

	first := c.compose(base, overlay, 40, 10, false)
	// Equal contents in fresh allocations must still hit.
	second := c.compose(strings.Clone(base), strings.Clone(overlay), 40, 10, false)
	if first != second || c.composes != 1 {
		t.Fatalf("identical inputs recomputed: composes = %d, want 1", c.composes)
	}

	c.compose(base+"x", overlay, 40, 10, false)
	if c.composes != 2 {
		t.Fatalf("changed base did not recompute: composes = %d", c.composes)
	}
	if got, want := c.compose(base, overlay+"o", 40, 10, false), oracleComposeCenteredOverlay(base, overlay+"o", 40, 10); got != want || c.composes != 3 {
		t.Fatalf("changed overlay: composes = %d, match = %v", c.composes, got == want)
	}
	c.compose(base, overlay+"o", 41, 10, false)
	c.compose(base, overlay+"o", 41, 11, false)
	if c.composes != 5 {
		t.Fatalf("resize did not recompute: composes = %d", c.composes)
	}
}

func TestOverlayComposeCacheReusesOverlayPlanOnBaseChange(t *testing.T) {
	t.Parallel()

	overlay := testGridLines(3, func(_ int) string { return strings.Repeat("o", 8) })
	var c overlayComposeCache
	c.compose(testGridLines(10, func(int) string { return "a" }), overlay, 40, 10, false)
	plan := &c.plan.mids[0]
	c.compose(testGridLines(10, func(int) string { return "b" }), overlay, 40, 10, false)
	if plan != &c.plan.mids[0] {
		t.Fatal("overlay plan was rebuilt on a base-only change")
	}
}

type testBottomOverlay struct{ OverlayShell }

func (testBottomOverlay) View() string { return "" }

func TestPlaceBottomMemoMatchesDirect(t *testing.T) {
	t.Parallel()

	base := testGridLines(20, func(_ int) string { return strings.Repeat("b", 40) })
	overlay := testGridLines(4, func(_ int) string { return "\x1b[7m" + strings.Repeat("世o", 5) + "\x1b[0m" })
	shell := OverlayShell{open: true}.WithDimensions(40, 20)
	var c overlayComposeCache
	for _, xOff := range []int{0, 7, 0, 7} {
		want := shell.PlaceBottomAnchoredAt(base, overlay, 3, xOff)
		if got := c.placeBottom(0, testBottomOverlay{shell}, base, overlay, 3, xOff); got != want {
			t.Fatalf("xOffset %d: memoised result differs", xOff)
		}
	}
	if got, want := c.placeBottom(0, testBottomOverlay{shell.WithDimensions(40, 30)}, base, overlay, 3, 7), shell.WithDimensions(40, 30).PlaceBottomAnchoredAt(base, overlay, 3, 7); got != want {
		t.Fatal("shell height change served a stale result")
	}
}

// TestBaseViewRowsAreFullWidth pins the invariant that makes overlay
// normalisation a no-op for every row the base view produces.
func TestBaseViewRowsAreFullWidth(t *testing.T) {
	t.Parallel()

	sizes := []struct{ w, h int }{{220, 60}, {160, 40}, {120, 30}, {80, 24}, {61, 17}}
	for _, pos := range []string{"left", "right", "hidden"} {
		for _, sz := range sizes {
			t.Run(pos+"-"+strconv.Itoa(sz.w)+"x"+strconv.Itoa(sz.h), func(t *testing.T) {
				t.Parallel()
				m := newModel(Config{
					Model:         "bench-model",
					ModelContexts: map[string]int{"bench-model": 4096},
				}, nil)
				m = updateModelDirect(m, tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
				populateBenchModelHeavy(m)
				if pos == "hidden" {
					m.sidebar.SetExpanded(false)
				} else {
					m.sidebarPosition = pos
					m.sidebar.SetExpanded(true)
				}
				m.layout()
				m.syncViewport()

				base := m.renderBaseView(m.contentWidth(), m.sidebar.Visible(m.width))
				rows := strings.Split(base, "\n")
				if len(rows) != m.height {
					t.Fatalf("base has %d rows, want %d", len(rows), m.height)
				}
				for i, r := range rows {
					if w := lipgloss.Width(r); w != m.width {
						t.Fatalf("row %d is %d cells wide, want %d: %q", i, w, m.width, r)
					}
					if cut := padOverlayLine(ansi.Cut(r, 0, m.width), m.width); cut != r {
						t.Fatalf("row %d changes under normalisation\nrow %q\ncut %q", i, r, cut)
					}
				}
			})
		}
	}
}
