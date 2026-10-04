package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// eagerApplyScreenHighlight is the pre-memoisation implementation, kept as the
// byte-for-byte oracle for highlightCache.
func eagerApplyScreenHighlight(frame string, state selectionState, regionLeft, regionRight int) string {
	if !state.hasSelection() {
		return frame
	}
	start, end := state.canonical()
	lines := strings.Split(frame, "\n")
	for i, line := range lines {
		if i < start.line || i > end.line {
			continue
		}
		lineWidth := ansi.StringWidth(line)
		startCol, endCol := 0, lineWidth
		if i == start.line {
			startCol = start.col
		}
		if i == end.line {
			endCol = end.col
		}
		if regionRight > 0 {
			startCol = max(regionLeft, startCol)
			endCol = min(regionRight, endCol)
		}
		if startCol >= endCol {
			continue
		}
		before := ansi.Cut(line, 0, startCol)
		mid := testSelStyle.Render(ansi.Strip(ansi.Cut(line, startCol, endCol)))
		after := ansi.Cut(line, endCol, lineWidth)
		lines[i] = before + mid + after
	}
	return strings.Join(lines, "\n")
}

func TestHighlightCacheMatchesEagerHighlight(t *testing.T) {
	useTrueColor(t)
	frame := strings.Join([]string{
		"plain first row",
		"\x1b[31mred\x1b[0m and 日本語 wide 😀 row",
		"",
		"short",
		"\x1b[1mbold\x1b[0m tail with more text to cover",
		"last row here",
	}, "\n")
	sel := func(sl, sc, el, ec int) selectionState {
		return selectionState{start: selectionPoint{line: sl, col: sc}, end: selectionPoint{line: el, col: ec}}
	}
	tests := []struct {
		name        string
		state       selectionState
		left, right int
	}{
		{"single line", sel(0, 2, 0, 9), 0, 0},
		{"multi line", sel(0, 3, 4, 7), 0, 0},
		{"reversed endpoints", sel(4, 7, 0, 3), 0, 0},
		{"region clamped", sel(1, 0, 5, 40), 5, 20},
		{"wide graphemes", sel(1, 8, 1, 17), 0, 0},
		{"empty selection", sel(2, 4, 2, 4), 0, 0},
		{"beyond frame", sel(3, 0, 20, 5), 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := eagerApplyScreenHighlight(frame, tc.state, tc.left, tc.right)
			var c highlightCache
			for pass := range 2 {
				if got := c.apply(frame, tc.state, testSelStyle, tc.left, tc.right); got != want {
					t.Fatalf("pass %d: highlight differs from eager oracle\n got %q\nwant %q", pass, got, want)
				}
			}
			if got := applyScreenHighlight(frame, tc.state, testSelStyle, tc.left, tc.right); got != want {
				t.Fatalf("applyScreenHighlight differs from eager oracle")
			}
		})
	}
}

// A reused cache must follow a moving selection and a changed frame exactly.
func TestHighlightCacheTracksDragAndFrameChanges(t *testing.T) {
	useTrueColor(t)
	frameA := "one two three\nfour five six\nseven eight nine\nten"
	frameB := "one two three\nFOUR five six\nseven eight nine\nten"
	var c highlightCache
	for i, step := range []struct {
		frame string
		end   selectionPoint
	}{
		{frameA, selectionPoint{line: 1, col: 3}},
		{frameA, selectionPoint{line: 2, col: 6}},
		{frameA, selectionPoint{line: 2, col: 2}},
		{frameB, selectionPoint{line: 2, col: 2}},
		{frameB, selectionPoint{line: 0, col: 1}},
	} {
		state := selectionState{start: selectionPoint{line: 0, col: 4}, end: step.end}
		want := eagerApplyScreenHighlight(step.frame, state, 0, 0)
		if got := c.apply(step.frame, state, testSelStyle, 0, 0); got != want {
			t.Fatalf("step %d: got %q want %q", i, got, want)
		}
	}
}

func TestHighlightCacheResetDropsRows(t *testing.T) {
	useTrueColor(t)
	state := selectionState{start: selectionPoint{line: 0, col: 0}, end: selectionPoint{line: 0, col: 3}}
	var c highlightCache
	first := c.apply("abcdef", state, testSelStyle, 0, 0)
	c.reset()
	other := testSelStyle.Bold(true)
	if second := c.apply("abcdef", state, other, 0, 0); second == first {
		t.Fatalf("highlight unchanged after reset with a different style: %q", second)
	}
}

// inputRegionCell finds a screen cell classified as the input region.
func inputRegionCell(t *testing.T, m *Model) (x, y int) {
	t.Helper()
	for y = m.height - 1; y >= 0; y-- {
		for x = 0; x < m.width; x++ {
			if m.detectRegion(x, y) == regionInput {
				return x, y
			}
		}
	}
	t.Fatal("no input region cell")
	return 0, 0
}

func TestDragReleaseExtractsLazilyFromLastFrame(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m *Model)
		end   func(m *Model, x, y int) (int, int)
	}{
		{"single line", func(*Model) {}, func(_ *Model, x, y int) (int, int) { return x + 6, y }},
		{"multi line", func(*Model) {}, func(_ *Model, x, y int) (int, int) { return x + 3, y - 1 }},
		{"across sidebar boundary", func(*Model) {}, func(_ *Model, _, y int) (int, int) { return 1, y - 1 }},
		{"overlay open", func(m *Model) {
			m.accentPicker = m.accentPicker.Open(m.accentPreset)
		}, func(_ *Model, x, y int) (int, int) { return x + 8, y - 2 }},
		{"wide graphemes", func(m *Model) {
			m.input.SetValue("日本語 wide 😀 text\n😀😀 second 世界 line\nthird")
		}, func(_ *Model, x, y int) (int, int) { return x + 9, y }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := auditModelSized(t, 1, 1, true)
			m.input.SetValue("first line of input text\nsecond line of input text\nthird")
			tc.setup(m)
			m.View()
			x, y := inputRegionCell(t, m)
			updateModelDirect(m, mouseClickMsg{x: x, y: y})
			if !m.selection.active {
				t.Fatal("drag did not start")
			}
			ex, ey := tc.end(m, x, y)
			updateModelDirect(m, mouseMotionMsg{x: ex, y: ey})
			m.View()

			// Oracle: the old eager path stripped the last View's pre-highlight frame.
			contentWidth := m.contentWidth()
			frame := m.renderOverlayView(m.renderBaseView(contentWidth, m.sidebar.Visible(m.width)), contentWidth)
			wantLines := strings.Split(ansi.Strip(frame), "\n")

			if !m.screenFramePending {
				t.Fatal("drag frame not retained for lazy extraction")
			}
			left, right := m.selectionHighlightBounds()
			want := extractText(wantLines, m.selection, left, right)

			_, cmd := m.handleMouseReleaseMsg(mouseReleaseMsg{x: ex, y: ey})
			if !slices.Equal(m.screenLines, wantLines) {
				t.Fatalf("screenLines after release differ from eager extraction")
			}
			if m.screenFramePending {
				t.Fatal("pending frame not consumed on release")
			}
			if (cmd != nil) != (want != "") {
				t.Fatalf("copy cmd present = %v, want text %q", cmd != nil, want)
			}
			if want == "" {
				t.Fatalf("test selection copied nothing (%d,%d)->(%d,%d)", x, y, ex, ey)
			}
		})
	}
}
