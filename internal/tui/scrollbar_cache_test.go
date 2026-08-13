package tui

import (
	"strings"
	"testing"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// newScrollbarTestModel builds a Model whose viewport overflows its height, so
// renderScrollbar produces a non-empty scrollbar. Content is fed through
// Update exactly like a real session, then syncViewport prepares the viewport.
func newScrollbarTestModel(t *testing.T, width, height int) *Model {
	t.Helper()
	m := newModel(Config{}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: width, Height: height})
	longContent := strings.Repeat("line of content\n", 100)
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, longContent, output.ChunkSourceAssistant)})
	m.syncViewport()
	return m
}

// renderScrollbarOriginalLoop is the pre-optimization per-cell loop, kept
// verbatim as the reference implementation for byte-identity tests. It renders
// both cells through the full Style.Render pipeline on every call, exactly as
// the old renderScrollbar did.
func renderScrollbarOriginalLoop(m *Model) string {
	totalContent := m.viewport.TotalLineCount()
	if totalContent <= m.viewport.Height() {
		return ""
	}

	vh := m.viewport.Height()
	if vh <= 0 {
		return ""
	}

	thumbH := max(1, vh*vh/totalContent)
	trackH := vh - thumbH

	scrollRange := totalContent - vh
	var thumbPos int
	if scrollRange > 0 && trackH > 0 {
		thumbPos = int(float64(m.viewport.YOffset()) / float64(scrollRange) * float64(trackH))
	}
	if thumbPos > trackH {
		thumbPos = trackH
	}

	style := m.styles.Scrollbar
	trackStyle := m.styles.ScrollbarTrack
	var sb strings.Builder
	sb.Grow(vh * 2)
	for i := 0; i < vh; i++ {
		if i >= thumbPos && i < thumbPos+thumbH {
			sb.WriteString(style.Render("▕"))
		} else {
			sb.WriteString(trackStyle.Render(" "))
		}
		if i < vh-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// TestRenderScrollbarMatchesReferenceLoop pins renderScrollbar's output to the
// original per-cell loop byte-for-byte across window sizes and scroll offsets.
// The group-repeat builder must never diverge from the loop, including at the
// scrollbar's top, middle, and bottom positions.
func TestRenderScrollbarMatchesReferenceLoop(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		width  int
		height int
		build  func(t *testing.T) *Model
	}{
		{name: "80x24", width: 80, height: 24},
		{name: "220x60", width: 220, height: 60},
		{
			// Degenerate geometry: vh=1 gives trackH=0 and thumbH=1, so the
			// thumb fills the track and thumbPos stays 0 for every offset.
			// Built directly because a Height:1 window reaches vh=1 only via
			// layout's max(1, ...) clamps; the pin below keeps this case from
			// silently drifting to a different height.
			name: "vh=1 degenerate",
			build: func(t *testing.T) *Model {
				m := &Model{
					viewport: newScrollModel(40, 1),
					styles:   testStyles(theme.AccentAmber),
				}
				m.setViewportContent(strings.Repeat("line of content\n", 100))
				if m.viewport.Height() != 1 {
					t.Fatalf("degenerate setup drifted: viewport height = %d, want 1", m.viewport.Height())
				}
				return m
			},
		},
		{
			// vh=2: thumbH=1 and trackH=1; the bottom offset puts thumbPos at
			// trackH, the opposite end of the thumb range from vh=1.
			name: "vh=2 thumb at bottom",
			build: func(_ *testing.T) *Model {
				m := &Model{
					viewport: newScrollModel(40, 2),
					styles:   testStyles(theme.AccentAmber),
				}
				m.setViewportContent(strings.Repeat("line of content\n", 100))
				return m
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var m *Model
			if tc.build != nil {
				m = tc.build(t)
			} else {
				m = newScrollbarTestModel(t, tc.width, tc.height)
			}
			maxOffset := m.viewport.TotalLineCount() - m.viewport.Height()
			if maxOffset <= 0 {
				t.Fatalf("model has no scroll range (total=%d height=%d)", m.viewport.TotalLineCount(), m.viewport.Height())
			}
			offsets := map[string]int{"top": 0, "middle": maxOffset / 2, "bottom": maxOffset}
			for name, off := range offsets {
				m.viewport.SetYOffset(off)
				got := m.renderScrollbar()
				want := renderScrollbarOriginalLoop(m)
				if got == "" {
					t.Fatalf("%s offset: renderScrollbar is empty; scrollbar expected", name)
				}
				if got != want {
					t.Fatalf("%s offset: renderScrollbar diverges from the reference loop\ngot:  %q\nwant: %q", name, got, want)
				}
			}
		})
	}
}

// TestRenderScrollbarCellsCachedAcrossScrolls proves the two rendered cells are
// reused (same backing array, not re-rendered) when the scrollbar moves, while
// the assembled scrollbar string still changes with the offset.
func TestRenderScrollbarCellsCachedAcrossScrolls(t *testing.T) {
	t.Parallel()
	m := newScrollbarTestModel(t, 80, 24)

	m.viewport.SetYOffset(0)
	scroll1 := m.renderScrollbar()
	thumbData := unsafe.StringData(m.scrollbarThumbCell)
	trackData := unsafe.StringData(m.scrollbarTrackCell)

	m.viewport.SetYOffset(10)
	scroll2 := m.renderScrollbar()

	if unsafe.StringData(m.scrollbarThumbCell) != thumbData {
		t.Fatal("thumb cell was re-rendered across a scroll instead of reused")
	}
	if unsafe.StringData(m.scrollbarTrackCell) != trackData {
		t.Fatal("track cell was re-rendered across a scroll instead of reused")
	}
	if scroll1 == scroll2 {
		t.Fatal("scrollbar output did not change after scrolling")
	}
	if got, want := m.scrollbarThumbCell, m.styles.Scrollbar.Render("▕"); got != want {
		t.Fatalf("cached thumb cell is not a correct render\ngot:  %q\nwant: %q", got, want)
	}
	if got, want := m.scrollbarTrackCell, m.styles.ScrollbarTrack.Render(" "); got != want {
		t.Fatalf("cached track cell is not a correct render\ngot:  %q\nwant: %q", got, want)
	}
}

// TestRenderScrollbarCellCacheInvalidatesOnAccent proves the cell cache keys on
// the *theme.Styles pointer: when handleSetAccentMsg re-points m.styles, the
// cells are re-derived under the new styles pointer. Today the scrollbar styles
// are accent-independent, so the strings may compare equal in content; the
// StringData change and the fresh-reference-render equality are the assertions
// that matter.
func TestRenderScrollbarCellCacheInvalidatesOnAccent(t *testing.T) {
	t.Parallel()
	m := newScrollbarTestModel(t, 80, 24)

	m.viewport.SetYOffset(0)
	m.renderScrollbar()
	oldThumb := unsafe.StringData(m.scrollbarThumbCell)

	m = updateModelDirect(m, setAccentMsg{preset: "violet"})

	// A different offset forces the whole-scrollbar cache to miss so the cell
	// cache re-derives under the new styles pointer.
	m.viewport.SetYOffset(10)
	m.renderScrollbar()
	if m.scrollbarCellStyles != m.styles {
		t.Fatal("cell cache was not re-derived under the new styles pointer after an accent change")
	}

	got := m.renderScrollbar()
	if unsafe.StringData(m.scrollbarThumbCell) == oldThumb {
		t.Fatal("thumb cell StringData did not change after an accent change re-pointed styles")
	}
	want := renderScrollbarOriginalLoop(m)
	if got != want {
		t.Fatalf("renderScrollbar after accent change does not match a fresh render from the current styles\ngot:  %q\nwant: %q", got, want)
	}
}

// TestRenderScrollbarCachesOnIdenticalInput proves renderScrollbar returns the
// exact cached string object (not a coincidentally-identical recomputation)
// when nothing about the scrollbar has changed between calls.
func TestRenderScrollbarCachesOnIdenticalInput(t *testing.T) {
	t.Parallel()
	m := newScrollbarTestModel(t, 80, 24)

	first := m.renderScrollbar()
	second := m.renderScrollbar()
	if unsafe.StringData(first) != unsafe.StringData(second) {
		t.Fatalf("renderScrollbar recomputed instead of returning the cached string\nfirst:  %q\nsecond: %q", first, second)
	}
}
