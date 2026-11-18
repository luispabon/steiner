package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

// contextReportModel returns a 130x24 model with the context report overlay
// open over the given roster entries and a recording controller attached.
func contextReportModel(t *testing.T, entries map[string]*rosterEntry) (*Model, *testController) {
	t.Helper()
	useTrueColor(t)
	ctrl := &testController{}
	m := newModel(Config{Model: "gpt-test", Controller: ctrl}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m.roster.entries = entries
	m.contextOverlay = openContextReportOverlay(
		contextAgentsFromRoster(m.roster.snapshot()), m.width, m.height, m.styles, m.content.glamourStyleSheet)
	return m, ctrl
}

// contextSidebarCell returns the terminal cell of the sidebar row that selects
// agent, failing when the hit map disagrees with the layout the test assumes.
func contextSidebarCell(t *testing.T, m *Model, agent, bodyRow int) (x, y int) {
	t.Helper()
	_, w, h := m.contextOverlayRendered()
	x, y = (m.width-w)/2+3, (m.height-h)/2+4+bodyRow
	if hit, ok := m.contextOverlayHitAt(x, y); !ok || !hit.inSidebar || hit.agent != agent {
		t.Fatalf("cell (%d,%d) hit = %+v, %v; want sidebar agent %d", x, y, hit, ok, agent)
	}
	return x, y
}

func longAgentReport() string {
	lines := []string{"# Agent report", ""}
	for i := 0; i < 40; i++ {
		lines = append(lines, "- detail line "+strings.Repeat("z", 12))
	}
	return strings.Join(lines, "\n")
}

func lastContextReportRequest(t *testing.T, ctrl *testController) string {
	t.Helper()
	requests := ctrl.contextReportRequests()
	if len(requests) == 0 {
		t.Fatal("no context report request recorded")
	}
	return requests[len(requests)-1].AgentID
}

func TestContextOverlayTabCyclesAgentsThroughKeys(t *testing.T) {
	t.Parallel()
	m, ctrl := contextReportModel(t, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterRunning, 20, 0, 2),
	})

	for _, want := range []string{"agent-1", "agent-2", ""} {
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
		if got := m.contextOverlay.selectedAgentID(); got != want {
			t.Fatalf("selected agent = %q, want %q", got, want)
		}
		if got := lastContextReportRequest(t, ctrl); got != want {
			t.Fatalf("requested agent = %q, want %q", got, want)
		}
	}
}

func TestContextOverlayShiftTabCyclesBackwardWithoutTogglingMode(t *testing.T) {
	t.Parallel()
	m, ctrl := contextReportModel(t, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterRunning, 20, 0, 2),
	})

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})

	if got := m.contextOverlay.selectedAgentID(); got != "agent-2" {
		t.Fatalf("selected agent = %q, want agent-2 from wrapping backwards", got)
	}
	if got := lastContextReportRequest(t, ctrl); got != "agent-2" {
		t.Fatalf("requested agent = %q, want agent-2", got)
	}
	if n := len(ctrl.switchModeActions()); n != 0 {
		t.Fatalf("SwitchMode actions = %d, want none while the context overlay is open", n)
	}
	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay closed on shift+tab")
	}
}

func TestContextOverlayTabShowsCachedReportAndResetsScroll(t *testing.T) {
	t.Parallel()
	m, _ := contextReportModel(t, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	})
	m.contextOverlay = m.contextOverlay.withReport("", longAgentReport())
	m.contextOverlay = m.contextOverlay.withReport("agent-1", "agent one report")
	m.contextOverlay = m.contextOverlay.scrollToEnd()
	if m.contextOverlay.scrollOffset == 0 {
		t.Fatal("orchestrator report did not scroll, want a scrolled starting point")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	if got := m.contextOverlay.scrollOffset; got != 0 {
		t.Fatalf("scrollOffset = %d, want 0 after selecting agent-1", got)
	}
	if !strings.Contains(m.contextOverlay.content, "agent one report") {
		t.Fatalf("content = %q, want the cached agent-1 report", m.contextOverlay.content)
	}
}

func TestContextOverlayClickSelectsSidebarAgent(t *testing.T) {
	t.Parallel()
	m, ctrl := contextReportModel(t, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	})
	x, y := contextSidebarCell(t, m, 1, 4)

	m = updateModel(t, m, mouseClickMsg{x: x, y: y})

	if got := m.contextOverlay.selectedAgentID(); got != "agent-1" {
		t.Fatalf("selected agent = %q, want agent-1", got)
	}
	if got := lastContextReportRequest(t, ctrl); got != "agent-1" {
		t.Fatalf("requested agent = %q, want agent-1", got)
	}
	if m.activeRegion != regionNone || m.mousePressX >= 0 || m.sidebarPressX >= 0 {
		t.Fatalf("press state = region %v, mousePressX %d, sidebarPressX %d; want the overlay to own the press",
			m.activeRegion, m.mousePressX, m.sidebarPressX)
	}
	if !m.overlayPress {
		t.Fatal("overlayPress = false after a press inside the overlay")
	}

	m = updateModel(t, m, mouseReleaseMsg{x: x, y: y})

	if m.overlayPress || m.sidebarPressX >= 0 {
		t.Fatalf("release state = overlayPress %v, sidebarPressX %d; want the release swallowed", m.overlayPress, m.sidebarPressX)
	}
	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay closed on click")
	}
}

func TestContextOverlayClickInDetailPaneIsSwallowed(t *testing.T) {
	t.Parallel()
	m, ctrl := contextReportModel(t, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	})
	_, w, h := m.contextOverlayRendered()
	x, y := (m.width-w)/2+40, (m.height-h)/2+5

	m = updateModel(t, m, mouseClickMsg{x: x, y: y})

	if m.mousePressX >= 0 {
		t.Fatalf("mousePressX = %d, want no transcript press inside the overlay", m.mousePressX)
	}
	if !m.overlayPress {
		t.Fatal("overlayPress = false, want the overlay to own the press")
	}
	if got := m.contextOverlay.selectedAgentID(); got != "" {
		t.Fatalf("selected agent = %q, want the orchestrator unchanged", got)
	}
	if n := len(ctrl.contextReportRequests()); n != 0 {
		t.Fatalf("context report requests = %d, want none for a detail-pane click", n)
	}

	m = updateModel(t, m, mouseReleaseMsg{x: x, y: y})

	if m.overlayPress {
		t.Fatal("overlayPress = true after the matching release")
	}
	if m.selection.hasSelection() {
		t.Fatalf("selection = %+v, want none after a swallowed click", m.selection)
	}
}

func TestContextOverlayWheelOverSidebarScrollsAgentsOnly(t *testing.T) {
	t.Parallel()
	entries := map[string]*rosterEntry{}
	for i := 1; i <= 15; i++ {
		id := fmt.Sprintf("agent-%02d", i)
		entries[id] = rosterAgent(id, "code", rosterRunning, int64(i), 0, i)
	}
	m, _ := contextReportModel(t, entries)
	m.contextOverlay = m.contextOverlay.withReport("", longAgentReport())
	m.contextOverlay = m.contextOverlay.scrollDown(3)
	selected, detailScroll := m.contextOverlay.selected, m.contextOverlay.scrollOffset
	if detailScroll == 0 {
		t.Fatal("detail scrollOffset = 0, want a scrolled report")
	}
	x, y := contextSidebarCell(t, m, 0, 1)

	m = updateModel(t, m, mouseWheelMsg{direction: "down", x: x, y: y})

	if got := m.contextOverlay.agentsScroll; got != 1 {
		t.Fatalf("agentsScroll = %d, want 1 after one wheel notch", got)
	}
	if m.contextOverlay.selected != selected {
		t.Fatalf("selected = %d, want %d unchanged by the agent list wheel", m.contextOverlay.selected, selected)
	}
	if got := m.contextOverlay.scrollOffset; got != detailScroll {
		t.Fatalf("detail scrollOffset = %d, want %d unchanged", got, detailScroll)
	}

	m = updateModel(t, m, mouseWheelMsg{direction: "up", x: x, y: y})

	if got := m.contextOverlay.agentsScroll; got != 0 {
		t.Fatalf("agentsScroll = %d, want 0 after scrolling back up", got)
	}
}

func TestContextOverlayWheelOverDetailScrollsReportOnly(t *testing.T) {
	t.Parallel()
	entries := map[string]*rosterEntry{}
	for i := 1; i <= 15; i++ {
		id := fmt.Sprintf("agent-%02d", i)
		entries[id] = rosterAgent(id, "code", rosterRunning, int64(i), 0, i)
	}
	m, _ := contextReportModel(t, entries)
	m.contextOverlay = m.contextOverlay.withReport("", longAgentReport())
	m.contextOverlay = m.contextOverlay.scrollAgents(2)
	_, w, h := m.contextOverlayRendered()
	x, y := (m.width-w)/2+40, (m.height-h)/2+5

	m = updateModel(t, m, mouseWheelMsg{direction: "down", x: x, y: y})

	if m.contextOverlay.scrollOffset == 0 {
		t.Fatal("detail scrollOffset = 0, want the report scrolled by the wheel")
	}
	if got := m.contextOverlay.agentsScroll; got != 2 {
		t.Fatalf("agentsScroll = %d, want the 2 set before the wheel", got)
	}
}

func TestContextOverlayRosterEventsRefreshSidebar(t *testing.T) {
	t.Parallel()
	m, _ := contextReportModel(t, nil)
	rev := m.contextOverlay.agentsRev
	occ := output.DelegationOccurrence{CallID: "call-1", BatchID: "batch", AgentID: "agent-1"}

	m = updateModel(t, m, runtimeEventMsg{Event: ev(output.DelegationStartedEvent{DelegationOccurrence: occ, AgentType: "review"})})

	if got := len(m.contextOverlay.agents); got != 2 {
		t.Fatalf("agents = %d after a start event, want the orchestrator and the new agent", got)
	}
	if m.contextOverlay.agentsRev != rev+1 {
		t.Fatalf("agentsRev = %d after a new row, want %d", m.contextOverlay.agentsRev, rev+1)
	}

	m = m.clickContextAgent(1).(*Model)
	m.contextOverlay = m.contextOverlay.withReport("agent-1", longAgentReport())
	m.contextOverlay = m.contextOverlay.scrollDown(4)
	content, scroll := m.contextOverlay.content, m.contextOverlay.scrollOffset
	if scroll == 0 {
		t.Fatal("detail scrollOffset = 0, want a scrolled agent report")
	}

	// An admission for a row that already exists changes nothing visible.
	m = updateModel(t, m, runtimeEventMsg{Event: ev(output.DelegationAcceptedEvent{DelegationOccurrence: occ})})
	if m.contextOverlay.agentsRev != rev+1 {
		t.Fatalf("agentsRev = %d after an unchanged roster event, want %d", m.contextOverlay.agentsRev, rev+1)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: ev(output.DelegationCompleteEvent{DelegationOccurrence: occ, AgentType: "review", Status: "completed", DurationMs: 1000})})

	if got := m.contextOverlay.selectedAgentID(); got != "agent-1" {
		t.Fatalf("selected agent = %q, want agent-1 to survive the refresh", got)
	}
	if got := m.contextOverlay.agents[1].status; got != rosterDone {
		t.Fatalf("agent-1 status = %q, want %q", got, rosterDone)
	}
	if m.contextOverlay.content != content {
		t.Fatalf("content changed on refresh, want the cached report kept")
	}
	if m.contextOverlay.scrollOffset != scroll {
		t.Fatalf("scrollOffset = %d, want %d preserved", m.contextOverlay.scrollOffset, scroll)
	}
	if m.contextOverlay.agentsRev != rev+2 {
		t.Fatalf("agentsRev = %d after the finished row, want %d", m.contextOverlay.agentsRev, rev+2)
	}
}

func TestContextOverlayDropsAgentReportWithoutSidebar(t *testing.T) {
	t.Parallel()
	report := output.WithContextReportAgent(output.NewOverlayReportEvent("Context Report", "agent report"), "agent-1")

	t.Run("no overlay", func(t *testing.T) {
		t.Parallel()
		m := newModel(Config{Model: "gpt-test"}, nil)
		m = updateModel(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
		m = updateModel(t, m, runtimeEventMsg{Event: report})
		if m.contextOverlay.IsOpen() {
			t.Fatal("context overlay opened for an agent report with no sidebar")
		}
	})

	t.Run("single pane overlay", func(t *testing.T) {
		t.Parallel()
		m := newModel(Config{Model: "gpt-test"}, nil)
		m = updateModel(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
		m.executeOpenCacheStatsAction()
		before := m.contextOverlay.content
		m = updateModel(t, m, runtimeEventMsg{Event: report})
		if m.contextOverlay.reportMode() {
			t.Fatal("single-pane overlay entered report mode")
		}
		if got := m.contextOverlay.title; got != "Cache hit rate" {
			t.Fatalf("overlay title = %q, want the cache stats overlay kept", got)
		}
		if m.contextOverlay.content != before {
			t.Fatal("single-pane overlay was replaced by the agent report")
		}
	})
}

func TestContextOverlayAgentReportScrollsToEndWhenAsked(t *testing.T) {
	t.Parallel()
	m, _ := contextReportModel(t, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	})
	m = m.clickContextAgent(1).(*Model)
	if m.contextOverlay.scrollOffset != 0 {
		t.Fatalf("scrollOffset = %d, want 0 after selecting agent-1", m.contextOverlay.scrollOffset)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.WithContextReportAgent(
		output.NewOverlayReportEventAtEnd("Context Report", longAgentReport()), "agent-1")})

	maxOffset := m.contextOverlay.maxScrollOffset()
	if maxOffset == 0 {
		t.Fatal("maxScrollOffset = 0, want a report that overflows the detail pane")
	}
	if got := m.contextOverlay.scrollOffset; got != maxOffset {
		t.Fatalf("scrollOffset = %d, want the last page %d", got, maxOffset)
	}
}

func TestContextOverlayRequestErrorShowsInDetailPane(t *testing.T) {
	t.Parallel()
	ctrl := &testController{err: errors.New("report unavailable")}
	m := newModel(Config{Model: "gpt-test", Controller: ctrl}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})

	m.openContextOverlayImmediate()

	if !m.contextOverlay.reportMode() {
		t.Fatal("context overlay is not in report mode")
	}
	if got := m.contextOverlay.reports[""]; !strings.Contains(got, "report unavailable") {
		t.Fatalf("cached orchestrator report = %q, want the request error", got)
	}
	if got := stripANSI(m.renderContextOverlay()); !strings.Contains(got, "report unavailable") {
		t.Fatalf("overlay = %q, want the error in the selected detail pane", got)
	}
}

func TestContextOverlaySidebarRunningAgentUsesStaticDot(t *testing.T) {
	t.Parallel()
	m, _ := contextReportModel(t, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterDone, 5, 65_000_000_005, 0),
	})

	var running, done string
	for _, line := range strings.Split(stripANSI(m.renderContextOverlay()), "\n") {
		switch {
		case strings.Contains(line, "agent-1"):
			running = line
		case strings.Contains(line, "agent-2"):
			done = line
		}
	}
	if !strings.Contains(running, "●") {
		t.Fatalf("running row = %q, want the static status dot", running)
	}
	for _, frame := range spinnerFrames {
		if strings.Contains(running, frame) {
			t.Fatalf("running row = %q, want no frozen spinner frame %q", running, frame)
		}
	}
	if !strings.Contains(done, "✓") {
		t.Fatalf("finished row = %q, want the shared roster done glyph", done)
	}
}
