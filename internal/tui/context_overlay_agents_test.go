package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func rosterAgent(id, agentType, status string, startTime, finishTime int64, seq int) *rosterEntry {
	return &rosterEntry{agentID: id, agentType: agentType, status: status, startTime: startTime, finishTime: finishTime, seq: seq}
}

// contextAgentRosterModel returns a model with the context report overlay open
// over the given roster entries.
func contextAgentRosterModel(t *testing.T, width int, entries map[string]*rosterEntry) *Model {
	t.Helper()
	useTrueColor(t)
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: width, Height: 24})
	m.roster.entries = entries
	m.contextOverlay = openContextReportOverlay(
		contextAgentsFromRoster(m.roster.snapshot()), m.width, m.height, m.styles, m.content.glamourStyleSheet)
	return m
}

func TestContextAgentsFromRosterOrdersOrchestratorFirst(t *testing.T) {
	t.Parallel()
	r := subAgentRoster{entries: map[string]*rosterEntry{
		"done-old":  rosterAgent("done-old", "code", rosterDone, 10, 100, 1),
		"done-new":  rosterAgent("done-new", "code", rosterFailed, 20, 200, 2),
		"done-tie":  rosterAgent("aaa-tie", "code", rosterDone, 30, 200, 5),
		"running-b": rosterAgent("running-b", "review", rosterRunning, 60, 0, 4),
		"queued":    rosterAgent("queued", "plan", rosterQueued, 50, 0, 3),
	}}

	agents := contextAgentsFromRoster(r.snapshot())
	want := []string{"", "queued", "running-b", "aaa-tie", "done-new", "done-old"}
	if len(agents) != len(want) {
		t.Fatalf("agents = %d, want %d", len(agents), len(want))
	}
	for i, id := range want {
		if agents[i].id != id {
			t.Fatalf("agents[%d].id = %q, want %q (order %v)", i, agents[i].id, id, agentIDs(agents))
		}
	}
}

func agentIDs(agents []contextOverlayAgent) []string {
	ids := make([]string, len(agents))
	for i, a := range agents {
		ids[i] = a.id
	}
	return ids
}

func TestContextAgentsFollowUpSharesOneRow(t *testing.T) {
	t.Parallel()
	var r subAgentRoster
	occ := func(callID string) output.DelegationOccurrence {
		return output.DelegationOccurrence{CallID: callID, BatchID: "batch", AgentID: "child"}
	}
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: occ("call-1"), AgentType: "review"}), 1)
	r.observe(ev(output.DelegationCompleteEvent{DelegationOccurrence: occ("call-1"), AgentType: "review", Status: "completed", DurationMs: 10}), 2)
	r.observe(ev(output.DelegationStartedEvent{DelegationOccurrence: occ("call-2"), AgentType: "review"}), 3)

	agents := contextAgentsFromRoster(r.snapshot())
	if len(agents) != 2 {
		t.Fatalf("agents = %v, want the orchestrator and one follow-up row", agentIDs(agents))
	}
	if agents[1].id != "child" || agents[1].status != rosterRunning {
		t.Fatalf("follow-up row = %+v, want child running", agents[1])
	}
}

func TestOpenContextReportOverlaySelectsOrchestrator(t *testing.T) {
	t.Parallel()
	styles := newModel(Config{}, nil).styles
	s := openContextReportOverlay(contextAgentsFromRoster(nil), 120, 24, styles, nil)
	if !s.IsOpen() || !s.reportMode() {
		t.Fatal("overlay = closed or not in report mode")
	}
	if len(s.agents) != 1 || s.agents[0].id != "" {
		t.Fatalf("agents = %+v, want an orchestrator-only list", s.agents)
	}
	if s.selected != 0 || s.selectedAgentID() != "" {
		t.Fatalf("selected = %d, want the orchestrator", s.selected)
	}
	if s.content != contextOverlayPlaceholder {
		t.Fatalf("content = %q, want the loading placeholder", s.content)
	}
	if len(s.reports) != 0 {
		t.Fatalf("reports = %+v, want empty", s.reports)
	}
}

func TestContextReportSidebarRendersAgents(t *testing.T) {
	t.Parallel()
	m := contextAgentRosterModel(t, 130, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterDone, 5, 65_000_000_005, 0),
	})
	if !m.contextOverlay.twoPane() {
		t.Fatalf("twoPane() = false at %dx%d; want the sidebar layout", m.width, m.height)
	}

	view := stripANSI(m.renderContextOverlay())
	for _, want := range []string{"Agents", "orchestrator", "Sub-agents", "review", "agent-1", "code", "agent-2", "│", "1m05s"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overlay = %q, want %q", view, want)
		}
	}
	if strings.Index(view, "agent-1") > strings.Index(view, "agent-2") {
		t.Fatalf("overlay order = %q, want the running agent above the finished one", view)
	}
	orchestratorLine := ""
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "orchestrator") {
			orchestratorLine = line
			break
		}
	}
	if !strings.Contains(orchestratorLine, "│") || !strings.Contains(orchestratorLine, contextOverlayPlaceholder) {
		t.Fatalf("orchestrator row = %q, want the sidebar divider and the report pane on the same line", orchestratorLine)
	}
}

func TestContextReportRoutesToSelectedAgent(t *testing.T) {
	t.Parallel()
	m := contextAgentRosterModel(t, 130, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterRunning, 20, 0, 2),
	})

	// A report for a non-selected agent is cached without touching the pane.
	m = updateModel(t, m, runtimeEventMsg{Event: output.WithContextReportAgent(output.NewOverlayReportEvent("Context Report", "agent two report"), "agent-2")})
	if !strings.Contains(m.contextOverlay.content, contextOverlayPlaceholder) {
		t.Fatalf("content = %q, want the placeholder while the orchestrator report is missing", m.contextOverlay.content)
	}
	if got := m.contextOverlay.reports["agent-2"]; !strings.Contains(got, "agent two report") {
		t.Fatalf("cached agent-2 report = %q, want the routed report", got)
	}

	// The orchestrator's own report fills the selected pane.
	m = updateModel(t, m, runtimeEventMsg{Event: output.WithContextReportAgent(output.NewOverlayReportEvent("Context Report", "orchestrator report"), "")})
	if !strings.Contains(m.contextOverlay.content, "orchestrator report") {
		t.Fatalf("content = %q, want the orchestrator report", m.contextOverlay.content)
	}

	// Selecting the cached agent shows its report without a new event.
	m = m.clickContextAgent(2).(*Model)
	if got := m.contextOverlay.selectedAgentID(); got != "agent-2" {
		t.Fatalf("selected agent = %q, want agent-2", got)
	}
	if !strings.Contains(m.contextOverlay.content, "agent two report") {
		t.Fatalf("content = %q, want the cached agent-2 report", m.contextOverlay.content)
	}

	// A late report for another agent stays cached while agent-2 is selected.
	m = updateModel(t, m, runtimeEventMsg{Event: output.WithContextReportAgent(output.NewOverlayReportEvent("Context Report", "agent one report"), "agent-1")})
	if !strings.Contains(m.contextOverlay.content, "agent two report") {
		t.Fatalf("content = %q, want agent-2 still shown", m.contextOverlay.content)
	}
	if got := m.contextOverlay.reports["agent-1"]; !strings.Contains(got, "agent one report") {
		t.Fatalf("cached agent-1 report = %q, want the routed report", got)
	}
}

func TestContextReportNarrowCycler(t *testing.T) {
	t.Parallel()
	m := contextAgentRosterModel(t, 80, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	})
	if m.contextOverlay.twoPane() {
		t.Fatalf("twoPane() = true at %d columns; want the narrow layout", m.width)
	}
	view := stripANSI(m.renderContextOverlay())
	for _, want := range []string{"◂ orchestrator ▸", "1 of 2 agents"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overlay = %q, want %q", view, want)
		}
	}
	if strings.Contains(view, "Sub-agents") {
		t.Fatalf("overlay = %q, want no sidebar in narrow mode", view)
	}

	m.contextOverlay = m.contextOverlay.selectAgentAt(1)
	view = stripANSI(m.renderContextOverlay())
	if !strings.Contains(view, "◂ agent-1 ▸") || !strings.Contains(view, "2 of 2 agents") {
		t.Fatalf("overlay = %q, want the cycler on the selected agent", view)
	}
}

// TestContextOverlayDropsReportForUnknownAgent pins the report cache guard: a
// report for an agent the sidebar has no row for can never render, so it is
// dropped instead of held in the cache.
func TestContextOverlayDropsReportForUnknownAgent(t *testing.T) {
	t.Parallel()
	m, _ := contextReportModel(t, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	})
	m.contextOverlay = m.contextOverlay.withReport("", longAgentReport())
	reports, content := len(m.contextOverlay.reports), m.contextOverlay.content

	m = updateModel(t, m, runtimeEventMsg{Event: output.WithContextReportAgent(
		output.NewOverlayReportEvent("Context Report", "ghost report"), "ghost-agent")})

	if _, ok := m.contextOverlay.reports["ghost-agent"]; ok {
		t.Fatalf("reports = %v, want no key for the agent that has no sidebar row", m.contextOverlay.reports)
	}
	if got := len(m.contextOverlay.reports); got != reports {
		t.Fatalf("cached reports = %d after the unknown agent report, want the %d unchanged", got, reports)
	}
	if m.contextOverlay.content != content {
		t.Fatal("detail pane changed on a report for an unknown agent, want the selected report kept")
	}
}

// TestContextOverlayRefreshesOnConversationReset pins the reset path: the
// reset drops the roster, so an open sidebar must rebuild to the orchestrator
// and fall back from a selection the cleared conversation no longer has.
func TestContextOverlayRefreshesOnConversationReset(t *testing.T) {
	t.Parallel()
	m, _ := contextReportModel(t, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterRunning, 20, 0, 2),
	})
	m.contextOverlay = m.contextOverlay.selectAgentAt(2)
	if got := m.contextOverlay.selectedAgentID(); got != "agent-2" {
		t.Fatalf("selected agent before the reset = %q, want agent-2", got)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewConversationResetEvent()})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("context overlay closed on conversation reset, want it kept open")
	}
	if agents := m.contextOverlay.agents; len(agents) != 1 || agents[0].id != "" {
		t.Fatalf("agents after the reset = %v, want the orchestrator only", agentIDs(agents))
	}
	if got := m.contextOverlay.selected; got != 0 {
		t.Fatalf("selected = %d after the reset, want the orchestrator fallback", got)
	}
	if got := m.contextOverlay.selectedAgentID(); got != "" {
		t.Fatalf("selected agent = %q after the reset, want the orchestrator", got)
	}
	view := stripANSI(m.renderContextOverlay())
	if strings.Contains(view, "agent-1") || strings.Contains(view, "agent-2") {
		t.Fatalf("overlay = %q, want no stale agent rows after the reset", view)
	}
	if !strings.Contains(view, "orchestrator") {
		t.Fatalf("overlay = %q, want the orchestrator row", view)
	}
}

func TestContextReportDetailWrapsToPaneWidth(t *testing.T) {
	t.Parallel()
	m := contextAgentRosterModel(t, 130, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	})
	if !m.contextOverlay.twoPane() {
		t.Fatal("want two-pane layout")
	}
	report := strings.TrimSpace(strings.Repeat("word ", 20))
	m.contextOverlay = m.contextOverlay.withReport("", report)

	if got, want := m.contextOverlay.reportWidth(), m.contextOverlay.detailWidth(); got != want {
		t.Fatalf("reportWidth() = %d, want the detail width %d", got, want)
	}
	if m.contextOverlay.reportWidth() >= m.contextOverlay.InnerWidth() {
		t.Fatalf("reportWidth() = %d, want less than the full inner width %d", m.contextOverlay.reportWidth(), m.contextOverlay.InnerWidth())
	}
	if m.contextOverlay.lineCount < 2 {
		t.Fatalf("lineCount = %d, want the paragraph wrapped to the pane", m.contextOverlay.lineCount)
	}
	for _, line := range m.contextOverlay.renderedLines {
		if w := lipgloss.Width(line); w > m.contextOverlay.detailWidth() {
			t.Fatalf("rendered line = %q width %d, want at most %d", line, w, m.contextOverlay.detailWidth())
		}
	}
}

func TestContextOverlayHitAtMapsSidebarRows(t *testing.T) {
	t.Parallel()
	m := contextAgentRosterModel(t, 130, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterRunning, 20, 0, 2),
	})
	_, w, h := m.contextOverlayRendered()
	originX, originY := (m.width-w)/2, (m.height-h)/2
	row := func(bodyRow int) int { return originY + 4 + bodyRow }

	cases := []struct {
		name      string
		x, y      int
		agent     int
		inSidebar bool
	}{
		{name: "orchestrator row", x: originX + 3, y: row(1), agent: 0, inSidebar: true},
		{name: "first sub-agent", x: originX + 3, y: row(4), agent: 1, inSidebar: true},
		{name: "second sub-agent", x: originX + 3, y: row(5), agent: 2, inSidebar: true},
		{name: "header row", x: originX + 3, y: row(0), agent: -1, inSidebar: true},
		{name: "detail pane", x: originX + 40, y: row(4), agent: -1, inSidebar: false},
	}
	for _, tc := range cases {
		got, ok := m.contextOverlayHitAt(tc.x, tc.y)
		if !ok || !got.inside {
			t.Fatalf("%s: hit at (%d,%d) = %+v, %v; want inside", tc.name, tc.x, tc.y, got, ok)
		}
		if got.inSidebar != tc.inSidebar || got.agent != tc.agent {
			t.Fatalf("%s: hit = %+v, want inSidebar=%v agent=%d", tc.name, got, tc.inSidebar, tc.agent)
		}
	}
	if _, ok := m.contextOverlayHitAt(originX-1, originY); ok {
		t.Fatal("hit left of the overlay = inside, want outside")
	}
	if _, ok := m.contextOverlayHitAt(originX, originY+h); ok {
		t.Fatal("hit below the overlay = inside, want outside")
	}
}

func TestContextOverlayHitMapMatchesRenderedRows(t *testing.T) {
	t.Parallel()
	m := contextAgentRosterModel(t, 130, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterDone, 5, 65_000_000_005, 0),
	})
	_, w, h := m.contextOverlayRendered()
	originX, originY := (m.width-w)/2, (m.height-h)/2
	lines := strings.Split(m.renderContextOverlay(), "\n")
	if len(lines) != h {
		t.Fatalf("rendered lines = %d, want the measured height %d", len(lines), h)
	}

	matched := 0
	for row := range h {
		y := originY + row
		if y < 0 || y >= m.height {
			continue
		}
		if hit, ok := m.contextOverlayHitAt(originX+3, y); ok && hit.inside && hit.inSidebar && hit.agent >= 0 {
			want := m.contextOverlay.agents[hit.agent].label()
			if got := stripANSI(lines[row]); !strings.Contains(got, want) {
				t.Fatalf("sidebar cell in row %d selects agent %d (%q) but renders %q", row, hit.agent, want, got)
			}
			matched++
			continue
		}
		if hit, ok := m.contextOverlayHitAt(originX+40, y); !ok || !hit.inside || hit.inSidebar || hit.agent != -1 {
			t.Fatalf("detail cell in row %d = %+v, %v; want inside the pane without a sidebar agent", row, hit, ok)
		}
	}
	if matched != 3 {
		t.Fatalf("rows selecting an agent = %d, want the orchestrator and two sub-agents", matched)
	}
}

func TestContextSidebarOverflowIndicators(t *testing.T) {
	t.Parallel()
	entries := map[string]*rosterEntry{}
	for i := 1; i <= 15; i++ {
		id := fmt.Sprintf("agent-%02d", i)
		entries[id] = rosterAgent(id, "code", rosterRunning, int64(i), 0, i)
	}
	m := contextAgentRosterModel(t, 130, entries)
	if !m.contextOverlay.twoPane() {
		t.Fatal("want two-pane layout")
	}
	if got := m.contextOverlay.subAgentCapacity(); got != 10 {
		t.Fatalf("subAgentCapacity() = %d, want 10", got)
	}
	view := stripANSI(m.renderContextOverlay())
	if strings.Contains(view, "↑ 5") || !strings.Contains(view, "↓ 5") {
		t.Fatalf("overlay = %q, want only the down indicator at the top of the list", view)
	}

	m.contextOverlay = m.contextOverlay.scrollAgents(3)
	view = stripANSI(m.renderContextOverlay())
	if !strings.Contains(view, "↑ 3") || !strings.Contains(view, "↓ 2") {
		t.Fatalf("overlay = %q, want both indicators after scrolling", view)
	}

	m.contextOverlay = m.contextOverlay.selectAgentAt(15).ensureAgentVisible()
	if got := m.contextOverlay.agentsScroll; got != 5 {
		t.Fatalf("agentsScroll = %d, want 5 so the selected row is visible", got)
	}
	m.contextOverlay = m.contextOverlay.scrollAgents(100)
	if got := m.contextOverlay.agentsScroll; got != 5 {
		t.Fatalf("agentsScroll after a large scroll = %d, want the clamped 5", got)
	}
}

func TestRefreshContextAgentsBumpsRevisionOnlyOnRowChange(t *testing.T) {
	t.Parallel()
	m := contextAgentRosterModel(t, 130, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
	})
	rev := m.contextOverlay.agentsRev
	m.refreshContextAgents()
	if m.contextOverlay.agentsRev != rev {
		t.Fatalf("agentsRev = %d after an unchanged refresh, want %d", m.contextOverlay.agentsRev, rev)
	}

	m.roster.entries["agent-1"].status = rosterDone
	m.roster.entries["agent-1"].finishTime = 60_000_000_000
	m.refreshContextAgents()
	if m.contextOverlay.agentsRev != rev+1 {
		t.Fatalf("agentsRev = %d after a row change, want %d", m.contextOverlay.agentsRev, rev+1)
	}
}

func TestRefreshContextAgentsKeepsSelectionAndFallsBack(t *testing.T) {
	t.Parallel()
	m := contextAgentRosterModel(t, 130, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterRunning, 20, 0, 2),
	})
	m.contextOverlay = m.contextOverlay.selectAgentAt(2)
	m.roster.entries["agent-1"].status = rosterDone
	m.refreshContextAgents()
	if got := m.contextOverlay.selectedAgentID(); got != "agent-2" {
		t.Fatalf("selected agent = %q, want agent-2 to survive the refresh", got)
	}

	delete(m.roster.entries, "agent-2")
	m.refreshContextAgents()
	if got := m.contextOverlay.selectedAgentID(); got != "" {
		t.Fatalf("selected agent = %q, want the orchestrator fallback", got)
	}
}

func TestCycleContextAgentWrapsAndRequests(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m.roster.entries = map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "review", rosterRunning, 10, 0, 1),
		"agent-2": rosterAgent("agent-2", "code", rosterRunning, 20, 0, 2),
	}
	m.contextOverlay = openContextReportOverlay(contextAgentsFromRoster(m.roster.snapshot()), m.width, m.height, m.styles, m.content.glamourStyleSheet)

	for _, want := range []string{"agent-1", "agent-2", ""} {
		m = m.cycleContextAgent(1).(*Model)
		requests := ctrl.contextReportRequests()
		if got := requests[len(requests)-1].AgentID; got != want {
			t.Fatalf("cycle request agent = %q, want %q", got, want)
		}
		if got := m.contextOverlay.selectedAgentID(); got != want {
			t.Fatalf("selected agent = %q, want %q", got, want)
		}
	}
}

func TestTokenStatsOverlayStaysSinglePane(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 130, Height: 24})
	m.executeOpenTokenStatsAction()

	if m.contextOverlay.reportMode() || m.contextOverlay.twoPane() || m.contextOverlay.narrowMode() {
		t.Fatalf("token stats overlay entered report mode: %+v", m.contextOverlay.agents)
	}
	if got, want := m.contextOverlay.reportWidth(), m.contextOverlay.InnerWidth(); got != want {
		t.Fatalf("reportWidth() = %d, want the single-pane inner width %d", got, want)
	}
	view := stripANSI(m.renderContextOverlay())
	if !strings.Contains(view, "Token usage") {
		t.Fatalf("overlay = %q, want the token stats title", view)
	}
	for _, unwanted := range []string{contextOverlayPlaceholder, "Sub-agents", "tab"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("overlay = %q, want no %q", view, unwanted)
		}
	}
}

func TestContextOverlaySidebarUsesPaletteSidebarBackground(t *testing.T) {
	t.Parallel()
	palette := theme.Palette{SidebarBG: "#112233", ContentBG: "#000000"}
	styles := theme.BuildStyles(theme.AccentAmber, palette)
	m := contextAgentRosterModel(t, 130, map[string]*rosterEntry{
		"agent-1": rosterAgent("agent-1", "code", rosterRunning, 10, 0, 1),
	})
	m.styles = &styles
	m.contextOverlay.styles = &styles

	// 48;2;17;34;51 is #112233 in truecolor, the configured sidebar background.
	if rendered := m.renderContextOverlay(); !strings.Contains(rendered, "48;2;17;34;51") {
		t.Fatalf("context overlay = %q, want the sidebar to paint the palette background", stripANSI(rendered))
	}
}
