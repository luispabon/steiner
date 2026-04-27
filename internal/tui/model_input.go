package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	if m.approval.active {
		allowed := isApprovalAccepted(value)
		if m.onApproval != nil {
			m.onApproval(allowed)
		}
		m.content.AppendLine(fmt.Sprintf("approval: %s", approvalDecisionText(allowed, m.approval.tool)))
		m.approval = approvalState{}
		m.status.mode = "running"
		m.input.Reset()
		m.input.Prompt = "› "
		m.input.Placeholder = "ask steiner — / for commands, @ for files"
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	action := parseInput(value, m.enabledSkills)
	if action.quit {
		return m, tea.Quit
	}
	if action.clear {
		m.content.Clear()
		m.sidebar.promptUsed = 0
		m.sidebar.budgetUsed = 0
		if m.sidebar.contextBudget > 0 {
			m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
		} else {
			m.status.context = ""
		}
		m.syncSidebar()
		if m.onClear != nil {
			m.onClear()
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.compaction {
		if m.onCompact != nil {
			m.onCompact()
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.inspectContext {
		if m.onContextInspect != nil {
			m.onContextInspect()
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.listSkills {
		names := append([]string(nil), m.skillNames...)
		slices.Sort(names)
		if len(names) == 0 {
			m.content.AppendLine("status: no skills configured")
		} else {
			m.content.AppendLine("status: skills " + strings.Join(names, ", "))
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.toggleSkill != "" {
		m.enabledSkills[action.toggleSkill] = action.toggleEnable
		if m.onSkillToggle != nil {
			m.onSkillToggle(action.toggleSkill, action.toggleEnable)
		}
		state := "disabled"
		if action.toggleEnable {
			state = "enabled"
		}
		m.content.AppendLine(fmt.Sprintf("status: skill %s %s", action.toggleSkill, state))
		m.input.Reset()
		m.historyIdx = 0
		m.syncSidebar()
		m.syncViewport()
		return m, nil
	}
	if action.listModels {
		if len(m.modelNames) == 0 {
			m.content.AppendLine("status: no named models configured")
		} else {
			m.content.AppendLine("status: models " + strings.Join(m.modelNames, ", "))
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.toggleThinking {
		m.input.Reset()
		m.historyIdx = 0
		return m, func() tea.Msg { return paletteToggleThinkingMsg{} }
	}
	if action.setAccent != "" {
		m.input.Reset()
		m.historyIdx = 0
		preset := action.setAccent
		return m, func() tea.Msg { return paletteSetAccentMsg{preset: preset} }
	}
	if action.switchModel != "" {
		if m.onModelSwitch != nil {
			m.onModelSwitch(action.switchModel)
		}
		m.status.model = action.switchModel
		m.sidebar.contextBudget = m.contextBudgetForModel(action.switchModel)
		m.sidebar.promptUsed = 0
		m.sidebar.budgetUsed = 0
		if m.sidebar.contextBudget > 0 {
			m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
		} else {
			m.status.context = ""
		}
		m.syncSidebar()
		m.content.AppendLine(fmt.Sprintf("status: model switched to %s", action.switchModel))
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.submit != "" {
		// prepend to history (non-empty submits only)
		if value != "" {
			m.inputHistory = append([]string{value}, m.inputHistory...)
			m.historyIdx = 0
		}
		if m.onSubmit != nil {
			m.onSubmit(action.submit)
		}
		m.content.AppendUser(action.submit)
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
	}
	return m, nil
}
