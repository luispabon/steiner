package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
)

//nolint:gocyclo // command parsing branches intentionally stay explicit
func (m *Model) handleEnter() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	if m.approval.active {
		return m.executeApprovalDecision(m.selectedApprovalDecision())
	}

	if m.oneshotRunning && !oneshotAllowedAction(value) {
		return m.executeSteerAction(), nil
	}

	action := parseInputWithSkills(value, m.enabledSkills, m.skillNames)
	if action.quit {
		return m.beginExitFlow()
	}
	if action.clear {
		return m.executeClearAction()
	}
	if action.compaction {
		return m.executeCompactAction(action)
	}
	if action.inspectConfig {
		return m.executeInspectConfigAction()
	}
	if action.openCacheStats {
		return m.executeOpenCacheStatsAction()
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
	if action.openAccentPicker {
		return m.executeOpenAccentPickerAction()
	}
	if action.toggleThinking {
		return m.executeToggleThinkingAction()
	}
	if action.setAccent != "" {
		return m.executeSetAccentAction(action.setAccent)
	}
	if action.switchModel != "" {
		return m.executeModelAction(action.switchModel, nil)
	}
	if action.profileUsage {
		return m.executeProfileUsageAction()
	}
	if action.switchProfile != "" {
		return m.executeSwitchProfileAction(action.switchProfile)
	}
	if action.toggleMode {
		return m.executeSetModeAction(m.toggledMode())
	}
	if action.setMode != "" {
		return m.executeSetModeAction(action.setMode)
	}
	if action.invalidMode != "" {
		return m.executeInvalidModeAction(action.invalidMode)
	}
	if action.listFiles {
		return m.executeListFilesAction(action.listFilesPath)
	}
	if action.showMCP {
		return m.executeShowMCPAction()
	}
	if action.requestSessionPicker {
		return m.executeRequestSessionPickerAction()
	}
	if action.requestOneshotResumePicker {
		return m.executeOneshotResumePickerAction()
	}
	if action.forkSession {
		return m.executeForkSessionAction()
	}
	if action.invokeSkill != "" {
		return m.executeInvokeSkillAction(action.invokeSkill, action.invokeSkillArgs)
	}
	if action.launchOneshotTask != "" {
		return m.executeLaunchOneshotAction(action.launchOneshotTask)
	}
	if action.resumeOneshotID != "" {
		return m.executeResumeOneshotAction(action.resumeOneshotID)
	}
	if action.submit != "" {
		return m.executeSubmitAction(value, action.submit, value)
	}
	return m, nil
}

func (m *Model) updateSkillState(skill string, enable bool) *Model {
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

func (m *Model) sendSkillEnabledAction(skill string, enabled bool) {
	if m.controller == nil {
		return
	}
	if err := m.controller.Handle(context.Background(), interactive.SetSkillEnabled{Name: skill, Enabled: enabled}); err != nil {
		m.content.AppendLine(fmt.Sprintf("status: %v", err))
	}
}

// buildSlashOverlayItems builds a list of all available slash commands and skills for the overlay.
func (m *Model) buildSlashOverlayItems() []slashOverlayItem {
	return projectOverlayItems(m.oneshotRunning, m.skillNames, m.skillDescriptions)
}
