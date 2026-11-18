package tui

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	// contextOverlaySidebarWidth is the fixed width of the agent sidebar.
	contextOverlaySidebarWidth = 26
	// contextOverlaySidebarFixedRows counts the sidebar rows above the
	// sub-agent viewport: "Agents", the orchestrator row, the rule and
	// "Sub-agents".
	contextOverlaySidebarFixedRows = 4
	// contextOverlayPaneGap is the report divider plus the space that follows
	// it.
	contextOverlayPaneGap = 2
	// contextOverlayDetailMinWidth is the narrowest report pane that still
	// leaves room for prose beside the sidebar.
	contextOverlayDetailMinWidth = 60
	// contextOverlayTwoPaneMinBody is the narrowest body that still fits a
	// usable sidebar beside the report.
	contextOverlayTwoPaneMinBody = 7
	// contextOverlayNarrowChromeLines is the extra chrome the narrow layout
	// adds: the cycler line and its divider.
	contextOverlayNarrowChromeLines = 2
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

// detailWidth is the report pane width in two-pane mode.
func (s contextOverlayState) detailWidth() int {
	return max(1, s.InnerWidth()-contextOverlaySidebarWidth-contextOverlayPaneGap)
}

func (s contextOverlayState) twoPane() bool {
	return s.reportMode() &&
		s.detailWidth() >= contextOverlayDetailMinWidth &&
		s.bodyVisibleHeight(contextOverlayChromeLines) >= contextOverlayTwoPaneMinBody
}

func (s contextOverlayState) narrowMode() bool {
	return s.reportMode() && !s.twoPane()
}

// reportWidth is the width the report markdown wraps and pads to.
func (s contextOverlayState) reportWidth() int {
	if s.twoPane() {
		return s.detailWidth()
	}
	return s.InnerWidth()
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

// subAgentCapacity is how many sub-agent rows the sidebar can show after the
// overflow indicators take their two rows.
func (s contextOverlayState) subAgentCapacity() int {
	viewport := max(0, s.visibleHeight()-contextOverlaySidebarFixedRows)
	if s.subAgentCount() > viewport && viewport >= 3 {
		return viewport - 2
	}
	return viewport
}

// subAgentWindow returns the visible sub-agent range and how many rows are
// hidden above and below it.
func (s contextOverlayState) subAgentWindow() (start, end, above, below int) {
	total := s.subAgentCount()
	capacity := s.subAgentCapacity()
	if total <= capacity {
		return 0, total, 0, 0
	}
	start = min(max(0, s.agentsScroll), total-capacity)
	return start, start + capacity, start, total - (start + capacity)
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

// ensureAgentVisible scrolls the sub-agent window until the selected row is in
// view. The orchestrator keeps the window where it is.
func (s contextOverlayState) ensureAgentVisible() contextOverlayState {
	capacity := s.subAgentCapacity()
	if capacity <= 0 {
		s.agentsScroll = 0
		return s
	}
	index := s.selected - 1
	if index < 0 {
		return s
	}
	if index < s.agentsScroll {
		s.agentsScroll = index
	}
	if index >= s.agentsScroll+capacity {
		s.agentsScroll = index - capacity + 1
	}
	s.agentsScroll = min(max(0, s.agentsScroll), max(0, s.subAgentCount()-capacity))
	return s
}

// scrollAgents scrolls the sub-agent list by delta rows, clamped to the same
// window the sidebar renders.
func (s contextOverlayState) scrollAgents(delta int) contextOverlayState {
	maxScroll := max(0, s.subAgentCount()-s.subAgentCapacity())
	s.agentsScroll = min(max(0, s.agentsScroll+delta), maxScroll)
	return s
}

// sidebarSegment is one styled run of a sidebar row.
type sidebarSegment struct {
	style lipgloss.Style
	text  string
}

// sidebarCells renders one 26-cell sidebar row. Selected rows drop the
// per-segment colours so the accent bar paints every cell.
func (s contextOverlayState) sidebarCells(segments []sidebarSegment, selected bool) string {
	width := contextOverlaySidebarWidth
	var plain strings.Builder
	var styled strings.Builder
	for _, seg := range segments {
		plain.WriteString(seg.text)
		styled.WriteString(seg.style.Background(lipgloss.Color(theme.BgElev2)).Render(seg.text))
	}
	text := plain.String()
	if pad := width - lipgloss.Width(text); pad > 0 {
		text += strings.Repeat(" ", pad)
		styled.WriteString(lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev2)).Render(strings.Repeat(" ", pad)))
	}
	if selected {
		return s.styles.AccentBg.Render(text)
	}
	return styled.String()
}

func (s contextOverlayState) sidebarLabel(text string) string {
	return s.sidebarCells([]sidebarSegment{{style: s.styles.FgMute.Bold(true), text: text}}, false)
}

func (s contextOverlayState) sidebarAgentRow(index int) string {
	a := s.agents[index]
	glyph, glyphStyle := rosterStatusGlyph(a.status, s.styles, 0)
	dim := a.finished()
	typeStyle, ok := s.styles.DelegateTagStyles[strings.ToLower(a.agentType)]
	if !ok {
		typeStyle = s.styles.ToolTagDefault
	}
	meta := s.styles.FgDim
	if dim {
		typeStyle = s.styles.FgDim
		meta = s.styles.FgMute
	}
	elapsed := ""
	if dim && a.finishTime > 0 {
		elapsed = formatRosterElapsed(a.startTime, a.finishTime)
	}
	elapsedW := 0
	if elapsed != "" {
		elapsedW = lipgloss.Width(elapsed) + 1
	}
	budget := max(4, contextOverlaySidebarWidth-2-3-elapsedW)
	typ := singleLineDisplayText(a.agentType)
	typeW := min(lipgloss.Width(typ), budget/2)
	typ = padCells(fitText(typ, typeW), typeW)
	id := fitText(singleLineDisplayText(a.id), max(1, budget-typeW))
	prefix := glyph + " " + typ + " · " + id
	gap := max(0, contextOverlaySidebarWidth-lipgloss.Width(prefix)-lipgloss.Width(elapsed))
	segments := []sidebarSegment{
		{style: glyphStyle, text: glyph + " "},
		{style: typeStyle, text: typ},
		{style: s.styles.FgMute, text: " · "},
		{style: meta, text: id},
		{style: meta, text: strings.Repeat(" ", gap)},
	}
	if elapsed != "" {
		segments = append(segments, sidebarSegment{style: meta, text: elapsed})
	}
	return s.sidebarCells(segments, s.selected == index)
}

// sidebarLines renders exactly visibleHeight rows of the agent sidebar and the
// agent index each row selects (-1 for non-clickable rows).
func (s contextOverlayState) sidebarLines() ([]string, []int) {
	height := s.visibleHeight()
	if len(s.agents) == 0 {
		return nil, nil
	}
	lines := make([]string, 0, height)
	targets := make([]int, 0, height)
	add := func(line string, target int) {
		lines = append(lines, line)
		targets = append(targets, target)
	}
	add(s.sidebarLabel("Agents"), -1)
	add(s.sidebarCells([]sidebarSegment{{style: s.styles.FgMute, text: s.agents[0].label()}}, s.selected == 0), 0)
	rule := s.sidebarCells([]sidebarSegment{{style: lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)), text: strings.Repeat("─", contextOverlaySidebarWidth)}}, false)
	add(rule, -1)
	add(s.sidebarLabel("Sub-agents"), -1)

	start, end, above, below := s.subAgentWindow()
	if above > 0 {
		add(s.sidebarCells([]sidebarSegment{{style: s.styles.FgMute, text: fmt.Sprintf("↑ %d", above)}}, false), -1)
	}
	for i := start; i < end; i++ {
		add(s.sidebarAgentRow(i+1), i+1)
	}
	if below > 0 {
		add(s.sidebarCells([]sidebarSegment{{style: s.styles.FgMute, text: fmt.Sprintf("↓ %d", below)}}, false), -1)
	}
	for len(lines) < height {
		add(s.sidebarCells(nil, false), -1)
	}
	return lines[:height], targets[:height]
}

// narrowCyclerLine renders the single-line agent cycler the narrow layout uses
// in place of the sidebar.
func (s contextOverlayState) narrowCyclerLine(width int) string {
	if len(s.agents) == 0 {
		return ""
	}
	left := "◂ " + s.agents[s.selectedIndex()].label() + " ▸"
	right := fmt.Sprintf("%d of %d agents", s.selectedIndex()+1, len(s.agents))
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
	return s.styles.Accent.Render(left) + strings.Repeat(" ", gap) + s.styles.FgMute.Render(right)
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
// empty agentID reports the orchestrator.
func (m *Model) requestContextReport(agentID string) {
	if m.controller == nil {
		return
	}
	if err := m.controller.Handle(context.Background(), interactive.RequestContextReport{AgentID: agentID}); err != nil {
		m.appendError(err)
	}
}

// refreshContextAgents rebuilds the sidebar list from the roster. The selection
// follows its agent ID and falls back to the orchestrator when that agent is
// gone.
func (m *Model) refreshContextAgents() {
	agents := contextAgentsFromRoster(m.roster.snapshot())
	if !slices.Equal(agents, m.contextOverlay.agents) {
		m.contextOverlay.agentsRev++
	}
	selectedID := m.contextOverlay.selectedAgentID()
	m.contextOverlay.agents = agents
	index := 0
	for i, a := range agents {
		if a.id == selectedID {
			index = i
			break
		}
	}
	if index != m.contextOverlay.selected {
		m.contextOverlay = m.contextOverlay.selectAgentAt(index)
		return
	}
	m.contextOverlay.agentsScroll = min(m.contextOverlay.agentsScroll, max(0, m.contextOverlay.subAgentCount()-m.contextOverlay.subAgentCapacity()))
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
