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
	if len(s.subAgents) == 0 {
		return nil
	}
	c := countRoster(s.subAgents)
	lines := []string{"", cardLabel(rosterLabel(c), s.styles)}

	visible, hidden := visibleRosterEntries(s.subAgents)

	typeW := 0
	for _, e := range visible {
		typeW = max(typeW, min(lipgloss.Width(e.agentType), 14))
	}
	emitted := map[string]bool{}
	for _, e := range visible {
		if e.group == "" {
			lines = append(lines, s.rosterRow(e, "", typeW, width))
			continue
		}
		if emitted[e.group] {
			continue
		}
		emitted[e.group] = true
		lines = append(lines, s.styledWithBg(s.styles.FgMute, fitText("┌ "+e.group, width)))
		for _, m := range visible {
			if m.group == e.group {
				lines = append(lines, s.rosterRow(m, s.styledWithBg(s.styles.FgMute, "│ "), typeW, width))
			}
		}
	}
	if hidden > 0 {
		lines = append(lines, s.styledWithBg(s.styles.FgMute, fmt.Sprintf("+%d finished", hidden)))
	}
	return lines
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
	if len(done) > subAgentsMaxRows && len(done) > slots {
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
