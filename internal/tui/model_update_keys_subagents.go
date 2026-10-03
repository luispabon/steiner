package tui

import (
	tea "charm.land/bubbletea/v2"
)

// activeJumpOrder returns active (running or queued) roster entries newest first:
// the exact reverse of subAgentRoster.snapshot() order (D5).
func (m *Model) activeJumpOrder() []rosterEntry {
	snap := m.roster.snapshot()
	out := make([]rosterEntry, 0, len(snap))
	for i := len(snap) - 1; i >= 0; i-- {
		e := snap[i]
		if e.status != rosterRunning && e.status != rosterQueued {
			continue
		}
		if e.occurrence == (occurrenceKey{}) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// cycleSubAgent jumps to the next (dir=+1, alt+.) or previous (dir=-1, alt+,) active sub-agent.
func (m *Model) cycleSubAgent(dir int) tea.Cmd {
	list := m.activeJumpOrder()
	n := len(list)
	if n == 0 {
		return nil
	}
	idx := -1
	for i, e := range list {
		if e.occurrence == m.jumpTarget {
			idx = i
			break
		}
	}
	if idx < 0 {
		if dir > 0 {
			idx = 0
		} else {
			idx = n - 1
		}
	} else {
		idx = ((idx+dir)%n + n) % n
	}
	for range n {
		if cmd := m.jumpToDelegation(list[idx].occurrence); cmd != nil {
			return cmd
		}
		idx = ((idx+dir)%n + n) % n
	}
	return nil
}

// handleSubAgentNavKey handles alt+. / alt+, / alt+/. Matched keys are always consumed.
func (m *Model) handleSubAgentNavKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if m.helpVisible {
		return false, nil
	}
	switch {
	case isAlt(msg, '.'):
		return true, m.cycleSubAgent(1)
	case isAlt(msg, ','):
		return true, m.cycleSubAgent(-1)
	case isAlt(msg, '/'):
		m.toggleJumpTargetCollapse()
		return true, nil
	}
	return false, nil
}
