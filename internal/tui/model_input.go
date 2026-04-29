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
		return m.executeApprovalAction(value)
	}

	action := parseInput(value, m.enabledSkills)
	if action.quit {
		return m, m.quitProgram()
	}
	if action.clear {
		return m.executeClearAction()
	}
	if action.compaction {
		return m.executeCompactAction()
	}
	if action.inspectContext {
		return m.executeInspectContextAction()
	}
	if action.listSkills {
		return m.executeListSkillsAction()
	}
	if action.toggleSkill != "" {
		return m.executeToggleSkillAction(action.toggleSkill, action.toggleEnable)
	}
	if action.listModels {
		return m.executeListModelsAction()
	}
	if action.toggleThinking {
		return m.executeToggleThinkingAction()
	}
	if action.setAccent != "" {
		return m.executeSetAccentAction(action.setAccent)
	}
	if action.switchModel != "" {
		return m.executeModelAction(action.switchModel)
	}
	if action.listFiles {
		return m.executeListFilesAction(action.listFilesPath)
	}
	if action.submit != "" {
		return m.executeSubmitAction(value, action.submit)
	}
	return m, nil
}

func (m Model) executeApprovalAction(value string) (tea.Model, tea.Cmd) {
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

func (m Model) executeInterruptAction() (tea.Model, tea.Cmd) {
	if m.onInterrupt != nil {
		m.onInterrupt()
	}
	m.content.AppendInterrupted()
	m.content.hadChunks = false
	m.approval = approvalState{}
	m.status.mode = ""
	m.status.streaming = false
	m.input.Reset()
	m.input.Prompt = "› "
	m.input.Placeholder = "ask steiner — / for commands, @ for files"
	m.historyIdx = 0
	m.syncSidebar()
	m.syncViewport()
	return m, nil
}

func (m Model) executeClearAction() (tea.Model, tea.Cmd) {
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

func (m Model) executeCompactAction() (tea.Model, tea.Cmd) {
	if m.onCompact != nil {
		m.onCompact()
	}
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}

func (m Model) executeInspectContextAction() (tea.Model, tea.Cmd) {
	if m.onContextInspect != nil {
		m.onContextInspect()
	}
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}

func (m Model) executeListSkillsAction() (tea.Model, tea.Cmd) {
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

func (m Model) executeToggleSkillAction(skill string, enable bool) (tea.Model, tea.Cmd) {
	m.enabledSkills[skill] = enable
	if m.onSkillToggle != nil {
		m.onSkillToggle(skill, enable)
	}
	state := "disabled"
	if enable {
		state = "enabled"
	}
	m.content.AppendLine(fmt.Sprintf("status: skill %s %s", skill, state))
	m.input.Reset()
	m.historyIdx = 0
	m.syncSidebar()
	m.syncViewport()
	return m, nil
}

func (m Model) executeListModelsAction() (tea.Model, tea.Cmd) {
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

func (m Model) executeListFilesAction(path string) (tea.Model, tea.Cmd) {
	root := path
	if root == "" {
		root = m.sidebar.workingDir
	}
	m.fileList = m.fileList.Open(root)
	m.fileList.width = m.width
	m.fileList.height = m.height
	m.input.Reset()
	m.historyIdx = 0
	return m, nil
}

func (m Model) executeToggleThinkingAction() (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.historyIdx = 0
	return m, func() tea.Msg { return paletteToggleThinkingMsg{} }
}

func (m Model) executeSetAccentAction(preset string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.historyIdx = 0
	return m, func() tea.Msg { return paletteSetAccentMsg{preset: preset} }
}

func (m Model) executeModelAction(modelName string) (tea.Model, tea.Cmd) {
	if m.onModelSwitch != nil {
		m.onModelSwitch(modelName)
	}
	m.status.model = modelName
	m.sidebar.contextBudget = m.contextBudgetForModel(modelName)
	m.sidebar.promptUsed = 0
	m.sidebar.budgetUsed = 0
	if m.sidebar.contextBudget > 0 {
		m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
	} else {
		m.status.context = ""
	}
	m.syncSidebar()
	m.content.AppendLine(fmt.Sprintf("status: model switched to %s", modelName))
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}

func (m Model) executeSubmitAction(value string, submitText string) (tea.Model, tea.Cmd) {
	// prepend to history (non-empty submits only)
	if value != "" {
		m.inputHistory = append([]string{value}, m.inputHistory...)
		m.historyIdx = 0
	}
	if m.onSubmit != nil {
		m.onSubmit(submitText)
	}
	m.content.AppendUser(submitText)
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}
