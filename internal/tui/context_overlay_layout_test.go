package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// contextOverlayLayoutModel returns a model of the given size with the context
// report overlay open over the roster entries.
func contextOverlayLayoutModel(t *testing.T, width, height int, entries map[string]*rosterEntry) *Model {
	t.Helper()
	useTrueColor(t)
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: height})
	m.roster.entries = entries
	m.contextOverlay = openContextReportOverlay(
		contextAgentsFromRoster(m.roster.snapshot()), m.width, m.height, m.styles, m.content.glamourStyleSheet)
	return m
}

// contextOverlayManyAgents returns n running sub-agent roster entries.
func contextOverlayManyAgents(n int) map[string]*rosterEntry {
	entries := make(map[string]*rosterEntry, n)
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("agent-%02d", i)
		entries[id] = rosterAgent(id, "code", rosterRunning, int64(i), 0, i)
	}
	return entries
}

// TestContextOverlayTwoPaneThresholds pins the layout switch: two panes need a
// 60-cell detail pane above a 7-row body, and either bound falling short by one
// selects the narrow cycler instead.
func TestContextOverlayTwoPaneThresholds(t *testing.T) {
	t.Parallel()
	entries := map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	}
	tests := []struct {
		name        string
		width       int
		height      int
		wantTwoPane bool
		wantDetail  int
	}{
		{name: "detail width at the minimum", width: 96, height: 24, wantTwoPane: true, wantDetail: 60},
		{name: "detail width one under the minimum", width: 95, height: 24, wantTwoPane: false},
		{name: "body height at the minimum", width: 130, height: 15, wantTwoPane: true, wantDetail: 88},
		{name: "body height one under the minimum", width: 130, height: 14, wantTwoPane: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := contextOverlayLayoutModel(t, tt.width, tt.height, entries)
			if got := m.contextOverlay.twoPane(); got != tt.wantTwoPane {
				t.Fatalf("twoPane() at %dx%d = %v, want %v", m.width, m.height, got, tt.wantTwoPane)
			}
			view := stripANSI(m.renderContextOverlay())
			if tt.wantTwoPane {
				if got := m.contextOverlay.detailWidth(); got != tt.wantDetail {
					t.Fatalf("detailWidth() at %dx%d = %d, want %d", m.width, m.height, got, tt.wantDetail)
				}
				for _, want := range []string{"Agents", "Sub-agents", "│"} {
					if !strings.Contains(view, want) {
						t.Fatalf("overlay at %dx%d = %q, want sidebar pane %q", m.width, m.height, view, want)
					}
				}
				if strings.Contains(view, "◂ orchestrator ▸") {
					t.Fatalf("overlay at %dx%d = %q, want no narrow cycler beside the sidebar", m.width, m.height, view)
				}
				return
			}
			for _, want := range []string{"◂ orchestrator ▸", "1 of 2 agents"} {
				if !strings.Contains(view, want) {
					t.Fatalf("overlay at %dx%d = %q, want narrow cycler %q", m.width, m.height, view, want)
				}
			}
			if strings.Contains(view, "Sub-agents") {
				t.Fatalf("overlay at %dx%d = %q, want no sidebar in the narrow layout", m.width, m.height, view)
			}
		})
	}
}

// TestContextOverlayResizeAcrossThreshold reflows between the two layouts and
// keeps an end-anchored report at its end across both resizes.
func TestContextOverlayResizeAcrossThreshold(t *testing.T) {
	t.Parallel()
	m := contextOverlayLayoutModel(t, 130, 24, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	})
	m.contextOverlay = m.contextOverlay.withReport("", longAgentReport())
	m.contextOverlay = m.contextOverlay.scrollToEnd()
	if !m.contextOverlay.atEnd() {
		t.Fatal("report did not start at its end, want an end-anchored starting point")
	}

	m = updateModel(t, m, tea.WindowSizeMsg{Width: 95, Height: 24})

	if m.contextOverlay.twoPane() || !m.contextOverlay.narrowMode() {
		t.Fatalf("layout after shrinking to %d columns = twoPane %v narrow %v, want narrow", m.width, m.contextOverlay.twoPane(), m.contextOverlay.narrowMode())
	}
	if got, want := m.contextOverlay.reportWidth(), m.contextOverlay.InnerWidth(); got != want {
		t.Fatalf("reportWidth() in narrow mode = %d, want the inner width %d", got, want)
	}
	if !m.contextOverlay.atEnd() {
		t.Fatal("end-anchored report left its end after shrinking to the narrow layout")
	}
	view := stripANSI(m.renderContextOverlay())
	if strings.Contains(view, "Sub-agents") || !strings.Contains(view, "◂ orchestrator ▸") {
		t.Fatalf("narrow overlay = %q, want the cycler and no sidebar", view)
	}

	m = updateModel(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})

	if !m.contextOverlay.twoPane() {
		t.Fatalf("twoPane() after growing back to %d columns = false, want the sidebar layout", m.width)
	}
	if got, want := m.contextOverlay.reportWidth(), m.contextOverlay.detailWidth(); got != want {
		t.Fatalf("reportWidth() in two-pane mode = %d, want the detail width %d", got, want)
	}
	if !m.contextOverlay.atEnd() {
		t.Fatal("end-anchored report left its end after growing back to two panes")
	}
	if view := stripANSI(m.renderContextOverlay()); !strings.Contains(view, "Sub-agents") {
		t.Fatalf("two-pane overlay = %q, want the sidebar back", view)
	}
}

// TestContextOverlayBodyGeometry pins the assembled body: every body row is
// one sidebar, one pane divider, one space and the detail pane, so the rows
// fill the inner width exactly and there are visibleHeight of them.
func TestContextOverlayBodyGeometry(t *testing.T) {
	t.Parallel()
	m := contextOverlayLayoutModel(t, 130, 24, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterRunning, 20, 0, 2),
	})
	s := m.contextOverlay
	if !s.twoPane() {
		t.Fatal("want the two-pane layout")
	}
	if got, want := contextOverlaySidebarWidth+1+1+s.detailWidth(), s.InnerWidth(); got != want {
		t.Fatalf("sidebar 26 + divider + space + detail = %d, want the inner width %d", got, want)
	}

	lines := strings.Split(stripANSI(m.renderContextOverlay()), "\n")
	bodyRows := s.visibleHeight()
	if got, want := len(lines), contextOverlayChromeLines+bodyRows; got != want {
		t.Fatalf("rendered rows = %d, want %d (chrome plus %d body rows)", got, want, bodyRows)
	}
	entry := m.overlayMemos.context.lookup(m.contextRenderKey())
	if entry == nil {
		t.Fatal("no cached context overlay render, want the rendered overlay memoized")
	}
	if got := len(entry.hit.targets); got != bodyRows {
		t.Fatalf("hit-map rows = %d, want one per body row (%d)", got, bodyRows)
	}

	// The box adds one padding column and one border column per side, so a
	// body row that fills the inner width renders InnerWidth+4 cells wide.
	for row := range bodyRows {
		line := lines[entry.hit.bodyTop+row]
		if got, want := lipgloss.Width(line), s.InnerWidth()+4; got != want {
			t.Fatalf("body row %d = %q width %d, want %d", row, line, got, want)
		}
		dividerCol := 2 + contextOverlaySidebarWidth
		if got := ansi.Cut(line, dividerCol, dividerCol+1); got != "│" {
			t.Fatalf("body row %d = %q, want the pane divider at column %d, got %q", row, line, dividerCol, got)
		}
	}
}

// selectedSubAgentVisible reports whether the selected sub-agent row falls in
// the sidebar window the overlay renders.
func selectedSubAgentVisible(s contextOverlayState) bool {
	start, end, _, _ := s.subAgentWindow()
	index := s.selected - 1
	return index >= 0 && index >= start && index < end
}

// TestContextOverlayResizeKeepsSelectedAgentVisible shrinks the terminal while
// the selected agent sits at the bottom of the sidebar, which removes rows and
// must re-scroll the window instead of hiding the selection.
func TestContextOverlayResizeKeepsSelectedAgentVisible(t *testing.T) {
	t.Parallel()
	m := contextOverlayLayoutModel(t, 130, 24, contextOverlayManyAgents(15))
	m.contextOverlay = m.contextOverlay.selectAgentAt(15)
	if got := m.contextOverlay.agentsScroll; got != 5 {
		t.Fatalf("agentsScroll before the resize = %d, want 5 so agent-15 sits at the bottom", got)
	}
	if !selectedSubAgentVisible(m.contextOverlay) {
		t.Fatal("selected agent is not visible before the resize")
	}

	m = updateModel(t, m, tea.WindowSizeMsg{Width: 130, Height: 18})

	if !m.contextOverlay.twoPane() {
		t.Fatalf("twoPane() at height %d = false, want the sidebar layout", m.height)
	}
	if got := m.contextOverlay.agentsScroll; got != 11 {
		t.Fatalf("agentsScroll after shrinking = %d, want 11 to keep agent-15 in the shorter window", got)
	}
	if !selectedSubAgentVisible(m.contextOverlay) {
		t.Fatalf("selected sub-agent %d left the sidebar window after the resize", m.contextOverlay.selected)
	}
	if view := stripANSI(m.renderContextOverlay()); !strings.Contains(view, "agent-15") {
		t.Fatalf("overlay = %q, want the selected agent-15 row rendered", view)
	}
}
