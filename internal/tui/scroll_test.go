package tui

import (
	"strings"
	"testing"
)

func manyScrollLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line"
	}
	return lines
}

// TestScrollModelMaxYOffset covers semantic 1: maxYOffset is
// max(0, len(lines) - height), with no frame size (no Style).
func TestScrollModelMaxYOffset(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		lines  []string
		height int
		want   int
	}{
		{"zero lines", nil, 5, 0},
		{"zero lines zero height", nil, 0, 0},
		{"single empty line", []string{""}, 1, 0},
		{"one line", []string{"a"}, 1, 0},
		{"one line zero height", []string{"a"}, 0, 1},
		{"content shorter than height", manyScrollLines(5), 10, 0},
		{"content exactly the height", manyScrollLines(5), 5, 0},
		{"content longer than height", manyScrollLines(10), 5, 5},
		{"height of 1", manyScrollLines(3), 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := scrollModel{lines: tc.lines, height: tc.height}
			if got := m.maxYOffset(); got != tc.want {
				t.Fatalf("maxYOffset() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestScrollModelSetYOffsetClamps covers semantic 2: SetYOffset clamps to
// [0, maxYOffset()], including negative values and values beyond the bottom.
func TestScrollModelSetYOffsetClamps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		lines  []string
		height int
		n      int
		want   int
	}{
		{"negative clamps to zero", manyScrollLines(10), 5, -3, 0},
		{"zero", manyScrollLines(10), 5, 0, 0},
		{"within range", manyScrollLines(10), 5, 4, 4},
		{"exact maximum", manyScrollLines(10), 5, 5, 5},
		{"beyond maximum", manyScrollLines(10), 5, 99, 5},
		{"beyond maximum with empty content", nil, 3, 5, 0},
		{"negative with empty content", nil, 3, -5, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := scrollModel{lines: tc.lines, height: tc.height}
			m.SetYOffset(tc.n)
			if got := m.YOffset(); got != tc.want {
				t.Fatalf("YOffset() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestScrollModelScrollDownGuards covers semantic 3: ScrollDown early-returns
// when AtBottom(), when n is zero, or when there are no lines. The PastBottom
// case is the observable part of the AtBottom guard: without it, the
// SetYOffset clamp would pull the view back to the bottom.
func TestScrollModelScrollDownGuards(t *testing.T) {
	t.Parallel()

	t.Run("at bottom stays", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{lines: manyScrollLines(10), height: 5}
		m.GotoBottom()
		m.ScrollDown(3)
		if got := m.YOffset(); got != 5 {
			t.Fatalf("YOffset() = %d, want 5 (at bottom)", got)
		}
	})

	t.Run("zero n is a no-op", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{lines: manyScrollLines(10), height: 5}
		m.ScrollDown(3)
		m.ScrollDown(0)
		if got := m.YOffset(); got != 3 {
			t.Fatalf("YOffset() = %d, want 3 (n=0 must not move)", got)
		}
	})

	t.Run("no lines is a no-op", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{height: 5}
		m.ScrollDown(3)
		if got := m.YOffset(); got != 0 {
			t.Fatalf("YOffset() = %d, want 0 (no content)", got)
		}
	})

	t.Run("clamped to new bottom after height change stays", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{lines: manyScrollLines(30), height: 20}
		m.ScrollDown(15) // offset 10, the old bottom
		m.SetHeight(25)  // maxYOffset shrinks to 5 and the offset clamps to it
		m.ScrollDown(3)  // already at bottom: no-op
		if got := m.YOffset(); got != 5 {
			t.Fatalf("YOffset() = %d, want 5 (clamped to new bottom)", got)
		}
	})

	t.Run("moves down when not at bottom", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{lines: manyScrollLines(10), height: 5}
		m.ScrollDown(2)
		if got := m.YOffset(); got != 2 {
			t.Fatalf("YOffset() = %d, want 2", got)
		}
	})
}

// TestScrollModelScrollUpGuards covers semantic 4: ScrollUp early-returns
// when AtTop() (YOffset() <= 0), when n is zero, or when there are no lines,
// and otherwise clamps to the current maximum.
func TestScrollModelScrollUpGuards(t *testing.T) {
	t.Parallel()

	t.Run("at top stays", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{lines: manyScrollLines(10), height: 5}
		m.ScrollUp(3)
		if got := m.YOffset(); got != 0 {
			t.Fatalf("YOffset() = %d, want 0 (at top)", got)
		}
	})

	t.Run("zero n is a no-op", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{lines: manyScrollLines(10), height: 5}
		m.GotoBottom()
		m.ScrollUp(0)
		if got := m.YOffset(); got != 5 {
			t.Fatalf("YOffset() = %d, want 5 (n=0 must not move)", got)
		}
	})

	t.Run("no lines is a no-op", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{height: 5}
		m.ScrollUp(3)
		if got := m.YOffset(); got != 0 {
			t.Fatalf("YOffset() = %d, want 0 (no content)", got)
		}
	})

	t.Run("after height change clamps to the new bottom then moves up", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{lines: manyScrollLines(30), height: 20}
		m.ScrollDown(15) // offset 10, the old bottom
		m.SetHeight(25)  // maxYOffset is now 5; offset clamps to it
		if got := m.YOffset(); got != 5 {
			t.Fatalf("YOffset() = %d, want 5 (clamped to new bottom)", got)
		}
		m.ScrollUp(3) // 5-3 = 2
		if got := m.YOffset(); got != 2 {
			t.Fatalf("YOffset() = %d, want 2", got)
		}
	})

	t.Run("moves up when not at top", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{lines: manyScrollLines(10), height: 5}
		m.GotoBottom()
		m.ScrollUp(2)
		if got := m.YOffset(); got != 3 {
			t.Fatalf("YOffset() = %d, want 3", got)
		}
	})
}

// TestScrollModelAtBottomComparison covers semantic 5: AtBottom is
// YOffset() >= maxYOffset(), so it is also true in the PastBottom state.
func TestScrollModelAtBottomComparison(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		lines      []string
		height     int
		offset     int
		wantBottom bool
	}{
		{"above bottom", manyScrollLines(10), 5, 3, false},
		{"exactly at bottom", manyScrollLines(10), 5, 5, true},
		{"past bottom", manyScrollLines(10), 5, 7, true},
		{"empty content is bottom", nil, 5, 0, true},
		{"top of scrollable content", manyScrollLines(10), 5, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := scrollModel{lines: tc.lines, height: tc.height, yOffset: tc.offset}
			if got := m.AtBottom(); got != tc.wantBottom {
				t.Fatalf("AtBottom() = %v, want %v (offset %d, max %d)", got, tc.wantBottom, tc.offset, m.maxYOffset())
			}
		})
	}
}

// TestScrollModelSetContentClampsOffset covers semantic 6: setting content
// clamps a now-invalid offset by moving to the bottom, while an offset that
// is still in range is kept.
func TestScrollModelSetContentClampsOffset(t *testing.T) {
	t.Parallel()

	t.Run("shrinking content pulls to bottom", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{height: 5}
		m.SetLines(manyScrollLines(20))
		m.ScrollDown(10)     // offset 10, max 15
		m.SetContent("a\nb") // max shrinks to 0
		if got := m.YOffset(); got != 0 {
			t.Fatalf("YOffset() = %d, want 0 (clamped to new bottom)", got)
		}
	})

	t.Run("same length content keeps offset", func(t *testing.T) {
		t.Parallel()
		m := scrollModel{height: 5}
		m.SetLines([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"})
		m.ScrollDown(4)
		m.SetLines([]string{"k", "l", "m", "n", "o", "p", "q", "r", "s", "t"})
		if got := m.YOffset(); got != 4 {
			t.Fatalf("YOffset() = %d, want 4 (offset still valid)", got)
		}
	})
}

// TestScrollModelSetHeightClampsOffset covers semantic 7: SetHeight re-clamps
// yOffset to the new range. Growing the height shrinks maxYOffset, so an
// offset that was valid before the resize is pulled to the new bottom instead
// of remaining past it.
func TestScrollModelSetHeightClampsOffset(t *testing.T) {
	t.Parallel()
	m := scrollModel{lines: manyScrollLines(30), height: 20}
	m.ScrollDown(15) // offset 10, old maxYOffset 10
	m.SetHeight(25)  // new maxYOffset 5; offset must clamp to the bottom
	if got := m.YOffset(); got != 5 {
		t.Fatalf("YOffset() = %d, want 5 (clamped to new bottom)", got)
	}
	if !m.AtBottom() {
		t.Fatal("AtBottom() = false, want true at the clamped bottom")
	}
	m.SetHeight(30) // content fits exactly: maxYOffset 0
	if got := m.YOffset(); got != 0 {
		t.Fatalf("YOffset() = %d, want 0 (clamped when content fits)", got)
	}
}

// TestScrollModelMouseWheelDeltaDefault covers semantic 8: the mouse wheel
// delta defaults to 3, as set by viewport.New.
func TestScrollModelMouseWheelDeltaDefault(t *testing.T) {
	t.Parallel()
	m := newScrollModel(80, 22)
	if m.mouseWheelDelta != 3 {
		t.Fatalf("mouseWheelDelta = %d, want 3", m.mouseWheelDelta)
	}
}

// TestScrollModelSetContentSplitting covers the documented splitting rule:
// "\r\n" is normalised to "\n" before splitting on "\n", a lone '\r' does
// not split a line, a trailing newline yields a trailing empty line, and a
// single zero-width line collapses to no lines.
func TestScrollModelSetContentSplitting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"crlf normalised", "a\r\nb", []string{"a", "b"}},
		{"lone cr preserved", "a\rb", []string{"a\rb"}},
		{"trailing newline", "a\nb\n", []string{"a", "b", ""}},
		{"empty collapses to no lines", "", nil},
		{"ansi only line collapses to no lines", "\x1b[31m\x1b[0m", nil},
		{"plain lines", "a\nb\nc", []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := scrollModel{}
			m.SetContent(tc.content)
			got := m.Lines()
			if len(got) != len(tc.want) {
				t.Fatalf("len(Lines()) = %d, want %d (%q)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Lines()[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestScrollModelVisibleWindowIsConsistent covers the single-slice invariant
// at the Model level: visibleViewportContent slices the same lines the
// scrollbar counts, so the rendered window and the scroll position can never
// disagree. It also pins the past-bottom window behaviour the renderer
// relies on.
func TestScrollModelVisibleWindowIsConsistent(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.viewport = newScrollModel(80, 3)
	m.setViewportContent(strings.Repeat("line\n", 9) + "line")
	m.viewport.SetYOffset(7)

	if m.viewport.TotalLineCount() != 10 {
		t.Fatalf("TotalLineCount() = %d, want 10", m.viewport.TotalLineCount())
	}
	want := "line\nline\nline"
	if got := m.visibleViewportContent(); got != want {
		t.Fatalf("visibleViewportContent() = %q, want %q", got, want)
	}
}
