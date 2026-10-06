package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// composerWheelModel returns a model whose composer holds more lines than its
// MaxHeight and whose transcript is scrollable both ways.
func composerWheelModel(t *testing.T) *Model {
	t.Helper()
	m := populateLongTranscript(newContentBenchModel(), 5)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 120, Height: 60})
	for i := range 60 {
		m.input.InsertString(fmt.Sprintf("composer line %d\n", i))
	}
	m.syncInputChrome()
	m.layout()
	m.viewport.SetYOffset(m.viewport.maxYOffset() / 2)
	return m
}

// screenRow returns the screen row of the first rendered line containing sub.
func screenRow(t *testing.T, m *Model, sub string) int {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if strings.Contains(line, sub) {
			return y
		}
	}
	t.Fatalf("no rendered row contains %q", sub)
	return -1
}

// TestComposerWheelScrolling verifies the wheel never scrolls the composer: the
// composer window follows the cursor, so the textarea's own viewport is hidden
// state. Wherever the pointer is, only the transcript scrolls.
func TestComposerWheelScrolling(t *testing.T) {
	t.Parallel()
	probe := composerWheelModel(t)
	composerY := screenRow(t, probe, "composer line 59")
	if chrome := probe.inputChromeHeight(probe.contentWidth()); chrome < 30 {
		t.Fatalf("composer chrome height = %d, want an overflowing composer (>= 30)", chrome)
	}
	if !probe.sidebar.Visible(probe.width) {
		t.Fatalf("sidebar not visible at width %d; the sidebar case needs it", probe.width)
	}
	sidebarX := 1
	if probe.sidebarPosition == "right" {
		sidebarX = probe.width - 2
	}
	if got := probe.detectRegion(40, composerY); got != regionInput {
		t.Fatalf("detectRegion(40, %d) = %v, want regionInput", composerY, got)
	}

	tests := []struct {
		name string
		x, y int
	}{
		{"over composer", 40, composerY},
		{"over transcript", 40, 3},
		{"over sidebar beside composer", sidebarX, composerY},
	}
	for _, tc := range tests {
		for _, button := range []tea.MouseButton{tea.MouseWheelUp, tea.MouseWheelDown} {
			t.Run(fmt.Sprintf("%s/%v", tc.name, button), func(t *testing.T) {
				t.Parallel()
				m := composerWheelModel(t)
				composerState, composerRows := m.input.View(), composerFrameRows(t, m, composerY)
				vpBefore := m.viewport.YOffset()

				newAuditDriver(m).send(tea.MouseWheelMsg{X: tc.x, Y: tc.y, Button: button})

				if m.input.View() != composerState {
					t.Error("composer textarea scrolled")
				}
				if got := composerFrameRows(t, m, composerY); got != composerRows {
					t.Error("rendered composer rows changed")
				}
				if m.viewport.YOffset() == vpBefore {
					t.Error("transcript did not scroll")
				}
			})
		}
	}
}

// composerFrameRows returns the rendered rows from just above the composer's
// last text row to the bottom of the screen, as one string.
func composerFrameRows(t *testing.T, m *Model, fromY int) string {
	t.Helper()
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	return strings.Join(lines[fromY-5:], "\n")
}
