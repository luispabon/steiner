package tui

import (
	"context"
	"maps"
	"slices"
	"sort"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	// contextOverlayPlaceholder fills the report pane until the selected
	// agent's report arrives.
	contextOverlayPlaceholder = "Loading context report…"
)

// contextOverlayAgent is one row of the context report agent sidebar. The zero
// value is the orchestrator.
type contextOverlayAgent struct {
	id         string
	agentType  string
	status     string
	startTime  int64
	finishTime int64
}

func (a contextOverlayAgent) finished() bool {
	return a.status == rosterDone || a.status == rosterFailed || a.status == rosterLost
}

func (a contextOverlayAgent) label() string {
	if a.id == "" {
		return "orchestrator"
	}
	return a.id
}

// contextAgentsFromRoster builds the sidebar list: the orchestrator first, then
// live sub-agents in dispatch order, then finished ones newest first.
func contextAgentsFromRoster(entries []rosterEntry) []contextOverlayAgent {
	agents := make([]contextOverlayAgent, 0, len(entries)+1)
	agents = append(agents, contextOverlayAgent{})
	var finished []contextOverlayAgent
	for _, e := range entries {
		agent := contextOverlayAgent{
			id:         e.agentID,
			agentType:  e.agentType,
			status:     e.status,
			startTime:  e.startTime,
			finishTime: e.finishTime,
		}
		if agent.finished() {
			finished = append(finished, agent)
			continue
		}
		agents = append(agents, agent)
	}
	sort.SliceStable(finished, func(i, j int) bool {
		if finished[i].finishTime != finished[j].finishTime {
			return finished[i].finishTime > finished[j].finishTime
		}
		return finished[i].id < finished[j].id
	})
	return append(agents, finished...)
}

// reportMode reports whether the overlay shows the agent sidebar. Single-pane
// reports such as cache stats carry no agents.
func (s contextOverlayState) reportMode() bool {
	return len(s.agents) > 0
}

func (s contextOverlayState) selectedIndex() int {
	return min(max(0, s.selected), max(0, len(s.agents)-1))
}

func (s contextOverlayState) selectedAgentID() string {
	if len(s.agents) == 0 {
		return ""
	}
	return s.agents[s.selectedIndex()].id
}

// subAgentCount excludes the orchestrator.
func (s contextOverlayState) subAgentCount() int {
	return max(0, len(s.agents)-1)
}

// agentContent returns the cached report for agentID, or the loading
// placeholder when no report has arrived yet.
func (s contextOverlayState) agentContent(agentID string) string {
	if content, ok := s.reports[agentID]; ok {
		return content
	}
	return contextOverlayPlaceholder
}

// withReport caches a report for its agent and, when that agent is selected,
// displays it while holding the current scroll position.
func (s contextOverlayState) withReport(agentID, content string) contextOverlayState {
	reports := make(map[string]string, len(s.reports)+1)
	maps.Copy(reports, s.reports)
	reports[agentID] = content
	s.reports = reports
	if s.selectedAgentID() != agentID {
		return s
	}
	s.content = content
	before := s.scrollOffset
	s = s.reflow()
	s.scrollOffset = min(before, s.maxScrollOffset())
	return s
}

// selectAgentAt selects one agent, resets the report scroll and shows its
// cached report or the loading placeholder.
func (s contextOverlayState) selectAgentAt(index int) contextOverlayState {
	if len(s.agents) == 0 {
		return s
	}
	s.selected = min(max(0, index), len(s.agents)-1)
	s = s.ensureAgentVisible()
	s.scrollOffset = 0
	s.content = s.agentContent(s.selectedAgentID())
	return s.reflow()
}

// openContextReportOverlay returns the agent-scoped context report overlay with
// the orchestrator selected and its report still loading.
func openContextReportOverlay(agents []contextOverlayAgent, width, height int, styles *theme.Styles, styleSheet glamour.TermRendererOption) contextOverlayState {
	s := openContextOverlay("Context Report", contextOverlayPlaceholder, width, height, styles, styleSheet)
	s.agents = agents
	s.selected = 0
	s.agentsScroll = 0
	s.agentsRev = 1
	s.reports = map[string]string{}
	return s.reflow()
}

// requestContextReport asks the session for one agent's context report. An
// empty agentID reports the orchestrator. A failed request is cached in the
// selected detail pane, like a report, as well as appended to the transcript.
func (m *Model) requestContextReport(agentID string) {
	if m.controller == nil {
		return
	}
	if err := m.controller.Handle(context.Background(), interactive.RequestContextReport{AgentID: agentID}); err != nil {
		m.appendError(err)
		if m.contextOverlay.reportMode() {
			m.contextOverlay = m.contextOverlay.withReport(agentID, contextOverlayRequestError(err))
		}
	}
}

// contextOverlayRequestError renders a failed report request in the detail
// pane, matching the wording the session uses for its own report failures.
func contextOverlayRequestError(err error) string {
	return "Context report unavailable.\n\n" + err.Error()
}

// refreshContextAgents rebuilds the sidebar list from the roster. The selected
// agent keeps its cached report and the detail scroll offset; a selection whose
// agent is gone falls back to the orchestrator. Only a changed row set bumps
// the render revision.
func (m *Model) refreshContextAgents() {
	agents := contextAgentsFromRoster(m.roster.snapshot())
	if !slices.Equal(agents, m.contextOverlay.agents) {
		m.contextOverlay.agentsRev++
	}
	selectedID := m.contextOverlay.selectedAgentID()
	m.contextOverlay.agents = agents
	index := slices.IndexFunc(agents, func(a contextOverlayAgent) bool { return a.id == selectedID })
	if index < 0 {
		m.contextOverlay = m.contextOverlay.selectAgentAt(0)
		return
	}
	m.contextOverlay.selected = index
	m.contextOverlay = m.contextOverlay.ensureAgentVisible().scrollAgents(0)
}

// refreshContextAgentsIfOpen refreshes the sidebar rows while the report
// overlay is open. Roster changes and prompt pruning call it.
func (m *Model) refreshContextAgentsIfOpen() {
	if m.contextOverlay.IsOpen() && m.contextOverlay.reportMode() {
		m.refreshContextAgents()
	}
}

// cycleContextAgent selects the next agent (wrapping) and requests its report.
func (m *Model) cycleContextAgent(delta int) tea.Model {
	if !m.contextOverlay.reportMode() {
		return m
	}
	m.refreshContextAgents()
	n := len(m.contextOverlay.agents)
	if n == 0 {
		return m
	}
	next := ((m.contextOverlay.selected+delta)%n + n) % n
	m.contextOverlay = m.contextOverlay.selectAgentAt(next)
	m.requestContextReport(m.contextOverlay.selectedAgentID())
	return m
}

// clickContextAgent selects the agent at index and requests its report.
func (m *Model) clickContextAgent(index int) tea.Model {
	if !m.contextOverlay.reportMode() || index < 0 || index >= len(m.contextOverlay.agents) {
		return m
	}
	m.contextOverlay = m.contextOverlay.selectAgentAt(index)
	m.requestContextReport(m.contextOverlay.selectedAgentID())
	return m
}
