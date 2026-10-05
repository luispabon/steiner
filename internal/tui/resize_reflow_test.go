package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func reflowFixture(t *testing.T) *Model {
	t.Helper()
	stubBgFormatClock(t)
	return populateLongTranscript(newContentBenchModel(), 12)
}

// reflowOracle builds the transcript directly at window width w: the size
// arrives before any content, so nothing is deferred and no reflow is involved.
func reflowOracle(w int) *Model {
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 100000},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: w, Height: 40})
	m = updateModelDirect(m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "bench-model", "", 4, 256)})
	return populateLongTranscript(m, 12)
}

func resizeTo(m *Model, w, h int) tea.Cmd {
	_, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return cmd
}

func fireReflow(m *Model) {
	_, _ = m.Update(resizeReflowFiredMsg{seq: m.reflow.seq})
}

// renderedWidths returns every width any segment holds a render for.
func renderedWidths(m *Model) map[int]bool {
	widths := map[int]bool{}
	for _, seg := range m.content.segments {
		if seg.cachedRender != "" {
			widths[seg.cachedRenderWidth] = true
		}
		for _, alt := range seg.altRenders {
			if alt.rendered != "" {
				widths[alt.width] = true
			}
		}
	}
	return widths
}

func TestResizeBurstReflowsOnce(t *testing.T) {
	m := reflowFixture(t)
	startWidth := m.viewport.Width()
	burst := []int{118, 115, 112, 109, 106, 103}
	for _, w := range burst {
		if cmd := resizeTo(m, w, 40); cmd == nil {
			t.Fatalf("resize to %d armed no reflow timer", w)
		}
		if !m.reflow.pending {
			t.Fatalf("resize to %d not pending", w)
		}
	}
	if got := renderedWidths(m); len(got) != 1 || !got[startWidth] {
		t.Fatalf("rendered widths during burst = %v, want only %d", got, startWidth)
	}

	fireReflow(m)
	if m.reflow.pending {
		t.Fatal("still pending after the fired message")
	}
	final := m.viewport.Width()
	got := renderedWidths(m)
	if !got[final] {
		t.Fatalf("no render at final width %d: %v", final, got)
	}
	for _, w := range burst {
		vw := w
		if m.sidebar.Visible(w) {
			vw = w - sidebarWidth - 1
		}
		vw -= 6
		if vw != final && vw != startWidth && got[vw] {
			t.Errorf("intermediate width %d was rendered", vw)
		}
	}

	oracle := reflowOracle(burst[len(burst)-1])
	if oracle.reflow.pending {
		t.Fatal("oracle deferred")
	}
	if !slices.Equal(m.viewport.Lines(), oracle.viewport.Lines()) {
		t.Error("burst frame differs from a single resize at the final width")
	}
}

func TestResizeReflowStaleFrame(t *testing.T) {
	for _, tc := range []struct {
		name   string
		width  int
		scroll int
	}{
		{"narrower at bottom", 104, 0},
		{"wider at bottom", 150, 0},
		{"narrower scrolled", 104, 37},
		{"wider scrolled", 150, 37},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := reflowFixture(t)
			if tc.scroll > 0 {
				m.scrollUp(tc.scroll)
			}
			start, height := m.viewport.YOffset(), m.viewport.Height()
			oldLines := slices.Clone(m.viewport.Lines()[start : start+height])
			oldWidth := m.viewport.Width()
			resizeTo(m, tc.width, 40)
			if !m.reflow.pending {
				t.Fatal("resize not deferred")
			}
			vw := m.viewport.Width()
			if vw == oldWidth {
				t.Fatalf("viewport width unchanged at %d", vw)
			}
			if m.viewport.YOffset() != start || m.viewport.Height() != height {
				t.Fatalf("window moved: offset %d height %d, want %d %d", m.viewport.YOffset(), m.viewport.Height(), start, height)
			}
			lines := strings.Split(m.visibleViewportContent(), "\n")
			if len(lines) != height {
				t.Fatalf("visible lines = %d, want %d", len(lines), height)
			}
			bg := m.resolvedPalette().ContentBG
			var raw []string
			for _, l := range strings.Split(m.bgFormat.source(), "\n") {
				raw = append(raw, ansi.Truncate(l, vw, ""))
			}
			want := strings.Split(theme.FormatContent(strings.Join(raw, "\n"), vw, bg), "\n")[start-m.contentTopPad:]
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != vw {
					t.Fatalf("line %d is %d cells, want %d", i, w, vw)
				}
				if !strings.Contains(l, "\x1b[48;2;") {
					t.Fatalf("line %d has no background: %q", i, l)
				}
				if got, exp := strings.TrimSuffix(l, "\r"), strings.TrimSuffix(want[i], "\r"); got != exp {
					t.Fatalf("line %d differs from the FormatContent oracle:\n got %q\nwant %q", i, got, exp)
				}
				old := ansi.Strip(oldLines[i])
				if vw < oldWidth {
					old = ansi.Truncate(old, vw, "")
				}
				if got := strings.TrimRight(ansi.Strip(l), " "); !strings.HasPrefix(strings.TrimRight(old, " "), got) {
					t.Fatalf("line %d text %q is not a cut of %q", i, got, old)
				}
			}
		})
	}
}

func TestResizeReflowStaleSeqIgnored(t *testing.T) {
	m := reflowFixture(t)
	startWidth := m.viewport.Width()
	resizeTo(m, 110, 40)
	old := m.reflow.seq
	resizeTo(m, 105, 40)
	_, _ = m.Update(resizeReflowFiredMsg{seq: old})
	if !m.reflow.pending || m.contentRenderWidth() != startWidth {
		t.Fatal("stale fired message ended the pending reflow")
	}
	fireReflow(m)
	if m.reflow.pending || m.contentRenderWidth() != m.viewport.Width() {
		t.Fatal("current fired message did not reflow")
	}
}

func TestResizeReflowImmediatePaths(t *testing.T) {
	t.Run("height only", func(t *testing.T) {
		m := reflowFixture(t)
		if cmd := resizeTo(m, 120, 30); cmd != nil || m.reflow.pending {
			t.Fatalf("height-only resize deferred (cmd=%v pending=%v)", cmd != nil, m.reflow.pending)
		}
		if m.viewport.Height() >= 40 {
			t.Fatalf("viewport height %d not updated", m.viewport.Height())
		}
	})
	t.Run("cached width", func(t *testing.T) {
		m := reflowFixture(t)
		resizeTo(m, 110, 40)
		fireReflow(m)
		if cmd := resizeTo(m, 120, 40); cmd != nil || m.reflow.pending {
			t.Fatalf("cached-width resize deferred (cmd=%v pending=%v)", cmd != nil, m.reflow.pending)
		}
		if m.contentRenderWidth() != m.viewport.Width() {
			t.Fatal("cached-width resize did not reflow synchronously")
		}
	})
	t.Run("back to rendered width while pending", func(t *testing.T) {
		m := reflowFixture(t)
		resizeTo(m, 110, 40)
		resizeTo(m, 120, 40)
		if m.reflow.pending {
			t.Fatal("resize back to the rendered width stayed pending")
		}
	})
	t.Run("sidebar toggle", func(t *testing.T) {
		m := reflowFixture(t)
		resizeTo(m, 130, 40)
		if !m.reflow.pending {
			t.Fatal("resize not deferred")
		}
		seq := m.reflow.seq
		before := m.viewport.Width()
		_, _ = m.Update(sidebarToggleKey)
		if m.viewport.Width() == before {
			t.Fatal("sidebar toggle did not change the viewport width")
		}
		if m.reflow.pending || m.contentRenderWidth() != m.viewport.Width() {
			t.Fatal("sidebar toggle left a reflow pending")
		}
		_, _ = m.Update(resizeReflowFiredMsg{seq: seq})
		if m.reflow.pending {
			t.Fatal("stale timer re-armed the reflow")
		}
	})
}

func TestResizeReflowScrollAnchor(t *testing.T) {
	t.Run("pinned", func(t *testing.T) {
		m := reflowFixture(t)
		if !m.autoScroll {
			t.Fatal("fixture not pinned")
		}
		resizeTo(m, 108, 40)
		fireReflow(m)
		if !m.autoScroll || !m.viewport.AtBottom() {
			t.Fatal("pinned viewport left the bottom")
		}
	})
	t.Run("scrolled", func(t *testing.T) {
		m := reflowFixture(t)
		m.scrollUp(120)
		seg, _, ok := m.scrollAnchor()
		if !ok {
			t.Fatal("no anchor segment")
		}
		resizeTo(m, 108, 40)
		resizeTo(m, 102, 40)
		fireReflow(m)
		got, _, ok := m.scrollAnchor()
		if !ok || got != seg {
			t.Fatalf("top segment = %d, want %d", got, seg)
		}
		if m.autoScroll {
			t.Fatal("scrolled viewport re-pinned")
		}
	})
}

func TestResizeReflowClearsSelection(t *testing.T) {
	m := reflowFixture(t)
	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{line: 3, col: 2}, end: selectionPoint{line: 5, col: 9}}
	resizeTo(m, 108, 40)
	fireReflow(m)
	if m.selection.hasSelection() {
		t.Fatal("selection survived the width reflow")
	}
}

func TestResizeReflowStreamingWhilePending(t *testing.T) {
	chunk := runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(2, "streamed words that arrive mid resize, ", output.ChunkSourceAssistant)}
	m := reflowFixture(t)
	startWidth := m.viewport.Width()
	resizeTo(m, 108, 40)
	for range 3 {
		_, _ = m.Update(chunk)
	}
	if !m.reflow.pending || m.contentRenderWidth() != startWidth {
		t.Fatal("content event ended the pending reflow")
	}
	if got := renderedWidths(m); len(got) != 1 || !got[startWidth] {
		t.Fatalf("rendered widths while pending = %v, want only %d", got, startWidth)
	}
	fireReflow(m)

	oracle := reflowOracle(108)
	for range 3 {
		_, _ = oracle.Update(chunk)
	}
	oracle.syncViewport()
	if !slices.Equal(m.viewport.Lines(), oracle.viewport.Lines()) {
		t.Error("frame after streaming during a pending reflow differs from streaming after it")
	}
}
