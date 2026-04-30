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
		return m.executeApprovalDecision(m.selectedApprovalDecision())
	}

	action := parseInput(value, m.enabledSkills)
	if action.quit {
		return m, tea.Quit
	}
	if action.clear {
		return m.executeClearAction()
	}
	if action.compaction {
		return m.executeCompactAction()
	}
	if action.inspectConfig {
		return m.executeInspectConfigAction()
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

func (m Model) selectedApprovalDecision() ApprovalDecision {
	if m.approval.selectedAction < 0 || m.approval.selectedAction >= len(approvalDecisions) {
		return ApprovalDecisionAllowOnce
	}
	return approvalDecisions[m.approval.selectedAction]
}

func (m Model) moveApprovalSelection(delta int) Model {
	if !m.approval.active || len(approvalDecisions) == 0 {
		return m
	}
	count := len(approvalDecisions)
	next := (m.approval.selectedAction + delta) % count
	if next < 0 {
		next += count
	}
	m.approval.selectedAction = next
	return m
}

func (m Model) executeApprovalDecision(decision ApprovalDecision) (tea.Model, tea.Cmd) {
	if m.onApproval != nil {
		m.onApproval(ApprovalSubmission{
			Tool:     m.approval.tool,
			Mode:     m.approval.mode,
			Decision: decision,
		})
	}
	m.approval = approvalState{}
	m.status.mode = "running"
	m.input.Reset()
	m.input.Focus()
	m.historyIdx = 0
	m.syncInputChrome()
	m.syncViewport()
	return m, nil
}

func (m Model) executeInterruptAction() (tea.Model, tea.Cmd) {
	if m.onInterrupt != nil {
		m.onInterrupt()
	}
	m.interruptPending = true
	m.content.AppendInterrupted()
	m.content.hadChunks = false
	m.approval = approvalState{}
	m.status.mode = ""
	m.input.Reset()
	m.input.Focus()
	m.historyIdx = 0
	m.syncInputChrome()
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

func (m Model) executeInspectConfigAction() (tea.Model, tea.Cmd) {
	if m.onConfigInspect != nil {
		m.onConfigInspect()
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
	providerBaseURL := m.sidebar.provider
	if m.onModelSwitch != nil {
		var ok bool
		providerBaseURL, ok = m.onModelSwitch(modelName)
		if !ok {
			m.content.AppendLine(fmt.Sprintf("status: model %s is not configured", modelName))
			m.input.Reset()
			m.historyIdx = 0
			m.syncViewport()
			return m, nil
		}
	}
	m.applyModelSelection(modelName, providerBaseURL)
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
