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
	case isAlt(msg, 'a'):
		m.openSubAgentPicker()
		return true, nil
	case isAlt(msg, '/'):
		m.toggleJumpTargetCollapse()
		return true, nil
	}
	return false, nil
}

// openSubAgentPicker opens the sub-agent picker over the conversation's cards.
func (m *Model) openSubAgentPicker() {
	m.subAgentPicker = m.subAgentPicker.Open(func(q string) []subAgentPickerRow {
		return m.content.subAgentPickerRows(q)
	})
}

// refreshSubAgentPicker keeps an open picker current; the tick runs while
// delegations are active, so status changes reach it.
func (m *Model) refreshSubAgentPicker() {
	if m.subAgentPicker.IsOpen() {
		m.subAgentPicker = m.subAgentPicker.refresh()
	}
}

func (m *Model) openSubAgentPickerFromSlashCommand() *Model {
	m.openSubAgentPicker()
	return m
}

func (m *Model) executeRequestSubAgentPickerAction() (tea.Model, tea.Cmd) {
	m.openSubAgentPicker()
	m.input.Reset()
	m.syncInputChrome()
	return m, nil
}

// handleSubAgentPickerKey routes keys while the picker is open: esc and alt+a
// close it, enter jumps to the selected card and closes, the rest edit the picker.
func (m *Model) handleSubAgentPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case msg.Code == tea.KeyEsc, isAlt(msg, 'a'):
		m.subAgentPicker = m.subAgentPicker.Close()
		return nil
	case msg.Code == tea.KeyEnter:
		key, ok := m.subAgentPicker.SelectedKey()
		if !ok {
			return nil
		}
		m.subAgentPicker = m.subAgentPicker.Close()
		return m.jumpToDelegation(key)
	}
	var cmd tea.Cmd
	m.subAgentPicker, cmd = m.subAgentPicker.Update(msg)
	return cmd
}
