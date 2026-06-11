package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
)

//nolint:gocyclo // command parsing branches intentionally stay explicit
func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	if m.approval.active {
		return m.executeApprovalDecision(m.selectedApprovalDecision())
	}

	action := parseInputWithSkills(value, m.enabledSkills, m.skillNames)
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
	if action.openModelPicker {
		return m.executeOpenModelPickerAction()
	}
	if action.toggleThinking {
		return m.executeToggleThinkingAction()
	}
	if action.cavemanToggle {
		return m.executeToggleCavemanModeAction()
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
	if action.requestSessionPicker {
		return m.executeRequestSessionPickerAction()
	}
	if action.invokeSkill != "" {
		return m.executeInvokeSkillAction(action.invokeSkill, action.invokeSkillArgs)
	}
	if action.submit != "" {
		return m.executeSubmitAction(value, action.submit, value)
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
	// Sync to the embedded tool segment so it re-renders with the new selection.
	for i := len(m.content.segments) - 1; i >= 0; i-- {
		seg := &m.content.segments[i]
		switch seg.kind {
		case segmentToolCall:
			if seg.toolData != nil && seg.toolData.approvalPending && !seg.toolData.approvalResolved {
				seg.toolData.approvalSelectedAction = next
				seg.renderDirty = true
				return m
			}
		case segmentToolCallGroup:
			if seg.toolGroupData == nil {
				continue
			}
			for j := len(seg.toolGroupData.entries) - 1; j >= 0; j-- {
				entry := seg.toolGroupData.entries[j]
				if entry != nil && entry.approvalPending && !entry.approvalResolved {
					entry.approvalSelectedAction = next
					seg.renderDirty = true
					return m
				}
			}
		}
	}
	return m
}

func (m Model) executeApprovalDecision(decision ApprovalDecision) (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitApproval{
			Tool:     m.approval.tool,
			Mode:     m.approval.mode,
			Decision: string(decision),
		}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.approval = approvalState{}
	m.status.mode = "running"
	m.activity = m.activity.static("approval submitted", string(decision))
	m.input.Reset()
	m.input.Focus()
	m.historyIdx = 0
	m.syncInputChrome()
	m.syncViewport()
	return m, nil
}

func (m Model) executeInterruptAction() Model {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.InterruptActiveRun{}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.interruptPending = true
	m.content.AppendInterrupted()
	m.content.hadChunks = false
	m.approval = approvalState{}
	m.activity = m.activity.clear()
	m.status.mode = ""
	m.input.Reset()
	m.input.Focus()
	m.historyIdx = 0
	m.syncInputChrome()
	m.syncSidebar()
	m.syncViewport()
	return m
}

func (m Model) executeClearAction() (tea.Model, tea.Cmd) {
	return m.clearConversationState()
}

func (m Model) executeCompactAction() (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.TriggerManualCompaction{}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}

func (m Model) executeInspectContextAction() (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.RequestContextReport{}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}

func (m Model) executeInspectConfigAction() (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.RequestConfigReport{}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
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
	m = m.updateSkillState(skill, enable)
	m.input.Reset()
	m.historyIdx = 0
	m.syncSidebar()
	m.syncViewport()
	return m, nil
}

func (m Model) executeOpenModelPickerAction() (tea.Model, tea.Cmd) {
	m.modelPicker = m.modelPicker.Open(m.modelNames, m.primaryModel)
	m.modelPicker.width = m.width
	m.modelPicker.height = m.height
	m.input.SetValue("/model ")
	m.input.CursorEnd()
	m.historyIdx = 0
	return m, nil
}

func (m Model) executeListFilesAction(path string) (tea.Model, tea.Cmd) {
	root := path
	if root == "" {
		root = m.sidebar.workingDir
	}
	m.fileList = m.fileList.Open(root)
	m.fileList.OverlayShell = m.fileList.WithDimensions(m.width, m.height)
	m.input.Reset()
	m.historyIdx = 0
	return m, nil
}

func (m Model) executeToggleThinkingAction() (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.historyIdx = 0
	return m, func() tea.Msg { return paletteToggleThinkingMsg{} }
}

func (m Model) executeToggleCavemanModeAction() (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.historyIdx = 0
	if m.controller != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.controller.Handle(ctx, interactive.ToggleCavemanMode{}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	return m, nil
}

func (m Model) executeSetAccentAction(preset string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.historyIdx = 0
	return m, func() tea.Msg { return paletteSetAccentMsg{preset: preset} }
}

func (m Model) executeModelAction(modelName string) (tea.Model, tea.Cmd) {
	providerBaseURL := m.sidebar.provider
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchModel{Name: modelName}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: model %s is not configured", modelName))
			m.input.Reset()
			m.historyIdx = 0
			m.syncViewport()
			return m, nil
		}
	}
	if baseURL, ok := m.modelBaseURLs[modelName]; ok {
		providerBaseURL = baseURL
	}
	m.applyModelSelection(modelName, providerBaseURL)
	m.content.AppendLine(fmt.Sprintf("status: model switched to %s", modelName))
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}

func (m Model) executeRequestSessionPickerAction() (tea.Model, tea.Cmd) {
	if m.sessionStore == nil {
		m.content.AppendLine("status: no session store configured")
		m.input.Reset()
		m.syncViewport()
		return m, nil
	}

	entries, err := m.sessionStore.List()
	if err != nil {
		m.content.AppendLine(fmt.Sprintf("status: failed to list sessions: %v", err))
		m.input.Reset()
		m.syncViewport()
		return m, nil
	}

	m.sessionPicker = m.sessionPicker.Open(entries)
	m.input.Reset()
	m.syncInputChrome()
	return m, nil
}

func (m Model) executeSubmitAction(value string, submitText string, displayText string) (tea.Model, tea.Cmd) {
	// prepend to history (non-empty submits only)
	if value != "" {
		m.inputHistory = append([]string{value}, m.inputHistory...)
		m.historyIdx = 0
	}
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitPrompt{Text: submitText}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.pendingImages = nil
	m.content.AppendUser(displayText)
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}

func (m Model) executeInvokeSkillAction(skillName, args string) (tea.Model, tea.Cmd) {
	m = m.updateSkillState(skillName, true)
	m.syncSidebar()

	// If args are provided, submit them as a prompt; otherwise just enable the skill
	if args != "" {
		displayText := "/" + skillName
		if args != "" {
			displayText += " " + args
		}
		return m.executeSubmitAction(displayText, args, displayText)
	}

	m.input.Reset()
	m.historyIdx = 0
	m.syncSidebar()
	m.syncViewport()
	return m, nil
}

func (m Model) updateSkillState(skill string, enable bool) Model {
	if m.enabledSkills == nil {
		m.enabledSkills = make(map[string]bool, len(m.skillNames))
	}

	if enable {
		for _, name := range m.skillNames {
			if name == skill || !m.enabledSkills[name] {
				continue
			}
			m.enabledSkills[name] = false
			m.sendSkillEnabledAction(name, false)
			m.content.AppendLine(fmt.Sprintf("status: skill %s disabled", name))
		}
		if !m.enabledSkills[skill] {
			m.enabledSkills[skill] = true
			m.sendSkillEnabledAction(skill, true)
			m.content.AppendLine(fmt.Sprintf("status: skill %s enabled", skill))
		}
		return m
	}

	if m.enabledSkills[skill] {
		m.enabledSkills[skill] = false
		m.sendSkillEnabledAction(skill, false)
		m.content.AppendLine(fmt.Sprintf("status: skill %s disabled", skill))
	}
	return m
}

func (m Model) sendSkillEnabledAction(skill string, enabled bool) {
	if m.controller == nil {
		return
	}
	if err := m.controller.Handle(context.Background(), interactive.SetSkillEnabled{Name: skill, Enabled: enabled}); err != nil {
		m.content.AppendLine(fmt.Sprintf("status: %v", err))
	}
}

// buildSlashOverlayItems builds a list of all available slash commands and skills for the overlay.
func (m Model) buildSlashOverlayItems() []slashOverlayItem {
	var items []slashOverlayItem

	// Built-in commands
	builtins := []slashOverlayItem{
		{command: "/clear", name: "Clear conversation", desc: "reset the current session", source: ""},
		{command: "/compact", name: "Compact context", desc: "trigger compaction", source: ""},
		{command: "/config", name: "Inspect config", desc: "show configuration", source: ""},
		{command: "/context", name: "Inspect context", desc: "inspect last request", source: ""},
		{command: "/exit", name: "Exit", desc: "quit steiner", source: ""},
		{command: "/ls", name: "List files", desc: "show directory contents", source: ""},
		{command: "/model", name: "Switch model", desc: "pick a language model", source: ""},
		{command: "/implement", name: "Implement plan", desc: "start implementation from a plan", source: ""},
		{command: "/review", name: "Review plan", desc: "review a completed plan", source: ""},
		{command: "/resume", name: "Resume session", desc: "load a previous session", source: ""},
		{command: "/skill", name: "Toggle skill", desc: "enable or disable a skill", source: ""},
		{command: "/skills", name: "List skills", desc: "show available skills", source: ""},
		{command: "/caveman", name: "Toggle caveman mode", desc: "switch terse prompting on/off", source: ""},
		{command: "/thinking", name: "Toggle thinking", desc: "show or hide thinking blocks", source: ""},
		{command: "/accent", name: "Set accent", desc: "change accent color", source: ""},
	}
	items = append(items, builtins...)

	// Add available skills as direct invocation items
	for _, skillName := range m.skillNames {
		items = append(items, slashOverlayItem{
			command: "/" + skillName,
			desc:    strings.TrimSpace(m.skillDescriptions[skillName]),
			isSkill: true,
		})
	}

	return items
}
