package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// TestZipColumnsMatchesJoinHorizontal is the differential test that makes
// Step 2 safe: it builds a real frame (sidebar + divider + main column,
// populated model, 220x60) and asserts zipColumns produces byte-identical
// output to lipgloss.JoinHorizontal, for both sidebar positions.
func TestZipColumnsMatchesJoinHorizontal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		sidebarPosition string
	}{
		{name: "left", sidebarPosition: "left"},
		{name: "right", sidebarPosition: "right"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{
				Model:         "bench-model",
				ModelContexts: map[string]int{"bench-model": 4096},
			}, nil)
			m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
			populateBenchModelHeavy(m)
			m.sidebarPosition = tc.sidebarPosition
			m.sidebar.SetExpanded(true)
			m.syncViewport()

			contentWidth := m.contentWidth()
			mainColumn := m.renderMainColumn(contentWidth)
			vDivider := m.styles.VDivider.Height(m.height).Render("")
			sidebar := m.sidebar.View(m.width, m.height)

			var want, got string
			if tc.sidebarPosition == "right" {
				want = lipgloss.JoinHorizontal(lipgloss.Top, mainColumn, vDivider, sidebar)
				got = zipColumns(mainColumn, vDivider, sidebar)
			} else {
				want = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, vDivider, mainColumn)
				got = zipColumns(sidebar, vDivider, mainColumn)
			}

			if want != got {
				wantLines := strings.Split(want, "\n")
				gotLines := strings.Split(got, "\n")
				for i := 0; i < min(len(wantLines), len(gotLines)); i++ {
					if wantLines[i] != gotLines[i] {
						t.Fatalf("zipColumns differs from JoinHorizontal at line %d\nwant %q\ngot  %q", i, wantLines[i], gotLines[i])
					}
				}
				t.Fatalf("zipColumns differs from JoinHorizontal\nwant %d bytes, %d lines\ngot  %d bytes, %d lines", len(want), len(wantLines), len(got), len(gotLines))
			}
		})
	}
}

// TestRenderBaseViewSidebarHiddenSkipsJoin proves the sidebar-hidden branch
// of renderBaseView takes the early return (no divider, no zip/join) and is
// therefore unaffected by the zipColumns change: it emits exactly the main
// column.
func TestRenderBaseViewSidebarHiddenSkipsJoin(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		Model:         "bench-model",
		ModelContexts: map[string]int{"bench-model": 4096},
	}, nil)
	m = updateModelDirect(m, tea.WindowSizeMsg{Width: 220, Height: 60})
	populateBenchModelHeavy(m)
	m.sidebar.SetExpanded(false)
	m.syncViewport()

	contentWidth := m.contentWidth()
	want := m.renderMainColumn(contentWidth)
	got := m.renderBaseView(contentWidth, m.sidebar.Visible(m.width))
	if want != got {
		t.Fatalf("renderBaseView with sidebar hidden should equal renderMainColumn exactly\nwant %d bytes\ngot  %d bytes", len(want), len(got))
	}
}

// TestZipColumnsMatchesJoinHorizontalAcrossSizes stresses the precondition
// zipColumns documents but cannot enforce: that every block is rectangular and
// all three are the same height. JoinHorizontal pads a short block to match the
// tallest; zipColumns emits "" for missing lines, so if the heights ever
// diverge the two disagree and the layout silently breaks — no panic, no
// failing assertion elsewhere, just a corrupt frame.
//
// lipgloss Height() is a *minimum*, so a sidebar whose content wraps past the
// terminal height would grow taller than the main column (which
// TruncateAndPadVertical pins to exactly m.height). This sweeps tight heights,
// narrow widths, and a long modified-file list to cover that reachable space.
func TestZipColumnsMatchesJoinHorizontalAcrossSizes(t *testing.T) {
	t.Parallel()
	sizes := []struct{ w, h int }{
		{220, 60}, {200, 50}, {120, 40}, {100, 24},
		{90, 12}, {85, 8}, {80, 6}, {300, 80},
		{220, 10}, {220, 6}, {200, 4}, {180, 3}, {150, 20},
	}
	files := make([]gitModifiedFile, 0, 40)
	for i := range 40 {
		files = append(files, gitModifiedFile{
			Path:   strings.Repeat("deep/nested/path/", 3) + "file" + string(rune('a'+i%26)) + ".go",
			Status: "M",
		})
	}

	for _, pos := range []string{"left", "right"} {
		for _, sz := range sizes {
			t.Run(pos+"-"+strconv.Itoa(sz.w)+"x"+strconv.Itoa(sz.h), func(t *testing.T) {
				t.Parallel()
				m := newModel(Config{
					Model:         "bench-model",
					ModelContexts: map[string]int{"bench-model": 4096},
				}, nil)
				m = updateModelDirect(m, tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
				populateBenchModelHeavy(m)
				m.sidebarPosition = pos
				m.sidebar.SetExpanded(true)
				m.sidebar.modifiedFiles = files
				m.sidebar.branch = strings.Repeat("very-long-branch-name/", 4)
				m.syncViewport()

				if !m.sidebar.Visible(m.width) {
					t.Skip("sidebar hidden at this width; the join branch is not reached")
				}

				contentWidth := m.contentWidth()
				mainColumn := m.renderMainColumn(contentWidth)
				vDivider := m.styles.VDivider.Height(m.height).Render("")
				sidebar := m.sidebar.View(m.width, m.height)

				var want, got string
				if pos == "right" {
					want = lipgloss.JoinHorizontal(lipgloss.Top, mainColumn, vDivider, sidebar)
					got = zipColumns(mainColumn, vDivider, sidebar)
				} else {
					want = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, vDivider, mainColumn)
					got = zipColumns(sidebar, vDivider, mainColumn)
				}
				if want == got {
					return
				}
				wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
				t.Errorf("zipColumns differs from JoinHorizontal at %dx%d (%s sidebar): "+
					"want %d lines, got %d lines — the rectangularity precondition does not hold here",
					sz.w, sz.h, pos, len(wl), len(gl))
				for i := range min(len(wl), len(gl)) {
					if wl[i] != gl[i] {
						t.Errorf("first differing line %d:\nwant %q\ngot  %q", i, wl[i], gl[i])
						break
					}
				}
			})
		}
	}
}
