package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// resizeFixtureModel builds a 220x60 model with an 892-line conversation, the
// populated-conversation geometry used to pin resize behaviour: viewport
// height 53, maxYOffset 839, autoScroll at the bottom.
func resizeFixtureModel(t *testing.T) *Model {
	t.Helper()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 220, Height: 60})
	for i := 0; i < 892; i++ {
		m.content.AppendLine("line")
	}
	m.layout()
	if m.viewport.TotalLineCount() != 892 {
		t.Fatalf("fixture lines = %d, want 892", m.viewport.TotalLineCount())
	}
	if m.viewport.Height() != 53 || m.viewport.maxYOffset() != 839 {
		t.Fatalf("fixture geometry: height %d, maxYOffset %d; want 53, 839",
			m.viewport.Height(), m.viewport.maxYOffset())
	}
	return m
}

// TestResizeKeepsScrollPosition pins the scroll position across terminal
// resizes. Growing the viewport shrinks maxYOffset, so only a position parked
// near the bottom of a growing terminal moves (it clamps to the new bottom);
// shrinking never invalidates an offset. The input-box expansion/collapse
// case exercises the same contract through relayoutInput. With autoScroll the
// resize handler pins to the new bottom instead of preserving the offset.
func TestResizeKeepsScrollPosition(t *testing.T) {
	tests := []struct {
		name     string
		start    int // yOffset before the resize; 839 means at the bottom
		apply    func(t *testing.T, m *Model)
		wantKeep int // expected yOffset with autoScroll off
		wantAuto int // expected yOffset with autoScroll on
	}{
		{
			"scrolled into history, terminal grows 60 to 80",
			339,
			func(t *testing.T, m *Model) {
				updateModel(t, m, tea.WindowSizeMsg{Width: 220, Height: 80})
			},
			339,
			819, // new bottom: 892 - (80-3-3-1)
		},
		{
			"parked 5 lines above bottom, terminal grows 60 to 90",
			834,
			func(t *testing.T, m *Model) {
				updateModel(t, m, tea.WindowSizeMsg{Width: 220, Height: 90})
			},
			809, // clamped to the new bottom: 892 - (90-3-3-1)
			809,
		},
		{
			"scrolled into history, terminal shrinks 60 to 30",
			339,
			func(t *testing.T, m *Model) {
				updateModel(t, m, tea.WindowSizeMsg{Width: 220, Height: 30})
			},
			339,
			869, // new bottom: 892 - (30-3-3-1)
		},
		{
			"input box expands then collapses",
			339,
			func(t *testing.T, m *Model) {
				// Seven lines make the composer 7+2 rows tall, shrinking the
				// viewport by 6 (53 to 47); clearing it restores the height.
				m.input.SetValue(strings.Repeat("x\n", 6) + "x")
				m.relayoutInput()
				if m.viewport.Height() != 47 {
					t.Fatalf("viewport height after input expansion = %d, want 47", m.viewport.Height())
				}
				m.input.SetValue("")
				m.relayoutInput()
				if m.viewport.Height() != 53 {
					t.Fatalf("viewport height after input collapse = %d, want 53", m.viewport.Height())
				}
			},
			339,
			839, // autoScroll re-pins to the bottom on each relayout
		},
	}

	for _, autoScroll := range []bool{false, true} {
		for _, tt := range tests {
			name := tt.name + " (autoScroll=" + map[bool]string{false: "false", true: "true"}[autoScroll] + ")"
			t.Run(name, func(t *testing.T) {
				m := resizeFixtureModel(t)
				if autoScroll {
					m.viewport.GotoBottom()
					m.autoScroll = true
					if m.viewport.YOffset() != 839 {
						t.Fatalf("start yOffset = %d, want 839", m.viewport.YOffset())
					}
				} else {
					m.scrollUp(m.viewport.YOffset() - tt.start)
					if m.viewport.YOffset() != tt.start {
						t.Fatalf("start yOffset = %d, want %d", m.viewport.YOffset(), tt.start)
					}
				}

				tt.apply(t, m)

				want := tt.wantKeep
				if autoScroll {
					want = tt.wantAuto
				}
				if got := m.viewport.YOffset(); got != want {
					t.Fatalf("yOffset after resize = %d, want %d", got, want)
				}
			})
		}
	}
}
