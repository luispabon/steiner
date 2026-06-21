package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
)

//nolint:gocyclo // command parsing branches intentionally stay explicit
func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	if m.approval.active {
		return m.executeApprovalDecision(m.selectedApprovalDecision())
	}

	if m.oneshotRunning && !oneshotAllowedAction(value) {
		return m.executeSteerAction(), nil
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
	if m.oneshotRunning {
		return []slashOverlayItem{
			{command: "/exit", name: "Exit", desc: "quit steiner", source: ""},
			{command: "/thinking", name: "Toggle thinking", desc: "show or hide thinking blocks", source: ""},
			{command: "/accent", name: "Set accent", desc: "change accent color", source: ""},
		}
	}

	var items []slashOverlayItem

	// Built-in commands
	builtins := []slashOverlayItem{
		{command: "/cache-stats", name: "Cache stats", desc: "show cache hit rate stats", source: ""},
		{command: "/clear", name: "Clear conversation", desc: "reset the current session", source: ""},
		{command: "/compact", name: "Compact context", desc: "trigger compaction", source: ""},
		{command: "/config", name: "Inspect config", desc: "show configuration", source: ""},
		{command: "/exit", name: "Exit", desc: "quit steiner", source: ""},
		{command: "/fork", name: "Fork conversation", desc: "fork current conversation into a new session", source: ""},
		{command: "/ls", name: "List files", desc: "show directory contents", source: ""},
		{command: "/model", name: "Switch model", desc: "pick a language model", source: ""},
		{command: "/implement", name: "Implement plan", desc: "start implementation from a plan", source: ""},
		{command: "/review", name: "Review plan", desc: "review a completed plan", source: ""},
		{command: "/resume", name: "Resume session", desc: "load a previous session", source: ""},
		{command: "/skill", name: "Toggle skill", desc: "enable or disable a skill", source: ""},
		{command: "/skills", name: "List skills", desc: "show available skills", source: ""},
		{command: "/thinking", name: "Toggle thinking", desc: "show or hide thinking blocks", source: ""},
		{command: "/accent", name: "Set accent", desc: "change accent color", source: ""},
		{command: "/oneshot", name: "Oneshot mode", desc: "run a headless task", source: ""},
		{command: "/oneshot-resume", name: "Resume oneshot", desc: "resume a oneshot run", source: ""},
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
