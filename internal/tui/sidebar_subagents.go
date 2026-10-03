package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// subAgentsMaxRows is the row budget before finished entries collapse.
const subAgentsMaxRows = 8

func formatRosterElapsed(startNano, endNano int64) string {
	s := max(0, endNano-startNano) / 1_000_000_000
	return fmt.Sprintf("%dm%02ds", s/60, s%60)
}

// subAgentsSection renders the sidebar sub-agent roster. It is empty when no
// sub-agent was dispatched since the last prompt.
func (s sidebarState) subAgentsSection(width int) []string {
	lines, _ := s.subAgentsRows(width)
	return lines
}

// subAgentsRows renders the roster section and, in parallel, the agent ID each
// line targets ("" for non-clickable lines: blank, label, group header,
// "+N finished").
func (s sidebarState) subAgentsRows(width int) (lines []string, targets []string) {
	if len(s.subAgents) == 0 {
		return nil, nil
	}
	add := func(line, target string) {
		lines = append(lines, line)
		targets = append(targets, target)
	}
	c := countRoster(s.subAgents)
	add("", "")
	add(cardLabel(rosterLabel(c), s.styles), "")

	visible, hidden := visibleRosterEntries(s.subAgents)

	typeW := 0
	for _, e := range visible {
		typeW = max(typeW, min(lipgloss.Width(e.agentType), 14))
	}
	type groupKey struct{ batchID, group string }
	emitted := map[groupKey]bool{}
	for _, e := range visible {
		if !e.admitted || e.occurrence.BatchID == "" || e.group == "" {
			add(s.rosterRow(e, "", typeW, width), e.agentID)
			continue
		}
		key := groupKey{batchID: e.occurrence.BatchID, group: e.group}
		if emitted[key] {
			continue
		}
		emitted[key] = true
		add(s.styledWithBg(s.styles.FgMute, fitText("┌ "+e.group, width)), "")
		for _, m := range visible {
			if m.admitted && m.occurrence.BatchID == e.occurrence.BatchID && m.group == e.group {
				add(s.rosterRow(m, s.styledWithBg(s.styles.FgMute, "│ "), typeW, width), m.agentID)
			}
		}
	}
	if hidden > 0 {
		add(s.styledWithBg(s.styles.FgMute, fmt.Sprintf("+%d finished", hidden)), "")
	}
	return lines, targets
}

// rosterLabel builds the section heading from the roster counts.
func rosterLabel(c rosterCounts) string {
	switch {
	case c.running == 0 && c.queued > 0:
		return fmt.Sprintf("sub-agents · %d queued", c.queued)
	case c.running == 0:
		return fmt.Sprintf("sub-agents · %d finished", c.finished)
	case c.queued > 0:
		return fmt.Sprintf("sub-agents · %d running · %d queued", c.running, c.queued)
	}
	return fmt.Sprintf("sub-agents · %d running", c.running)
}

// visibleRosterEntries orders active entries before finished ones and
// collapses finished entries beyond the row budget, returning the hidden count.
func visibleRosterEntries(entries []rosterEntry) ([]rosterEntry, int) {
	active := make([]rosterEntry, 0, len(entries))
	var done []rosterEntry
	for _, e := range entries {
		if e.finished() {
			done = append(done, e)
		} else {
			active = append(active, e)
		}
	}
	slots := max(0, subAgentsMaxRows-len(active))
	hidden := 0
	if len(active)+len(done) > subAgentsMaxRows && len(done) > slots {
		hidden = len(done) - slots
		done = done[:slots]
	}
	return slices.Concat(active, done), hidden
}

func (s sidebarState) rosterRow(e rosterEntry, prefix string, typeW, width int) string {
	dim := e.finished()
	var icon string
	switch e.status {
	case rosterRunning:
		icon = s.styledWithBg(lipgloss.NewStyle().Foreground(s.styles.AccentColor), spinnerFrames[s.tickCount%len(spinnerFrames)])
	case rosterQueued:
		icon = s.styledWithBg(s.styles.FgMute, "◌")
	case rosterDone:
		icon = s.styledWithBg(s.styles.SuccessStyle, "✓")
	case rosterFailed:
		icon = s.styledWithBg(s.styles.ErrorStyle, "✗")
	default:
		icon = s.styledWithBg(s.styles.Warn, "?")
	}
	elapsed := ""
	switch {
	case e.status == rosterQueued:
	case e.finishTime > 0:
		elapsed = formatRosterElapsed(e.startTime, e.finishTime)
	default:
		elapsed = formatRosterElapsed(e.startTime, s.subAgentsNow)
	}
	id := fitText(e.agentID, 10)
	avail := width - lipgloss.Width(prefix) - 2 - lipgloss.Width(id) - 1 - len(elapsed) - 1
	tw := min(typeW, max(4, avail))
	typ := fitText(e.agentType, tw)
	typ += strings.Repeat(" ", max(0, tw-lipgloss.Width(typ)))
	typeStyle := s.styles.DelegateTagStyles[strings.ToLower(e.agentType)]
	if _, ok := s.styles.DelegateTagStyles[strings.ToLower(e.agentType)]; !ok {
		typeStyle = s.styles.ToolTagDefault
	}
	meta := s.styles.FgMute
	if dim {
		typeStyle = s.styles.FgDim
		meta = s.styles.FgFaint
	}
	return prefix + icon + s.styledWithBg(s.styles.FgMute, " ") + s.styledWithBg(typeStyle, typ) +
		s.styledWithBg(meta, " "+id+" "+elapsed)
}
