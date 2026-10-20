package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/provider"
)

// sessionController safely asserts the controller to the concrete *interactive.Session
// type these oneshot actions require, reporting false instead of panicking when it
// isn't (e.g. a test harness or an alternate Controller implementation).
func sessionController(controller interactive.Controller) (*interactive.Session, bool) {
	sess, ok := controller.(*interactive.Session)
	return sess, ok
}

func (m *Model) executeInterruptAction() *Model {
	m.cancelWorkflowHandoffSettlement()
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.InterruptActiveRun{}); err != nil {
			m.appendError(err)
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
	m.syncInputChrome()
	m.syncSidebar()
	m.relayoutInput()
	m.syncViewport()
	return m
}

func (m *Model) executeClearAction() (tea.Model, tea.Cmd) {
	next, cleared, err := m.clearConversationStateWithError()
	if cleared && err == nil && m.clearConversationHooks != nil {
		m.clearConversationHooks()
	}
	return next, nil
}

func (m *Model) executeCompactAction(action inputAction) (tea.Model, tea.Cmd) {
	if m.compactBusy() {
		m.refuseWhileBusy("compact")
		return m, nil
	}
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.TriggerManualCompaction{Steering: action.compactionSteering}); err != nil {
			m.appendError(err)
		}
	}
	m.input.Reset()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

func (m *Model) executeInspectConfigAction() (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.RequestConfigReport{}); err != nil {
			m.appendError(err)
		}
	}
	m.input.Reset()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

func (m *Model) executeListSkillsAction() (tea.Model, tea.Cmd) {
	names := append([]string(nil), m.skillNames...)
	slices.Sort(names)
	if len(names) == 0 {
		m.content.AppendLine("status: no skills configured")
	} else {
		m.content.AppendLine("status: skills " + strings.Join(names, ", "))
	}
	m.input.Reset()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

func (m *Model) executeToggleSkillAction(skill string, enable bool) (tea.Model, tea.Cmd) {
	m = m.updateSkillState(skill, enable)
	m.input.Reset()
	m.syncSidebar()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

func (m *Model) executeOpenModelPickerAction() (tea.Model, tea.Cmd) {
	m.modelPicker = m.modelPicker.OpenEntries(m.modelPickerEntries(), m.primaryModel)
	m.modelPicker.width = m.width
	m.modelPicker.height = m.height
	m.input.SetValue("/model ")
	m.input.CursorEnd()
	return m, nil
}

func (m *Model) executeListFilesAction(path string) (tea.Model, tea.Cmd) {
	root := path
	if root == "" {
		root = m.sidebar.workingDir
	}
	m.fileList = m.fileList.Open(root)
	m.fileList.OverlayShell = m.fileList.WithDimensions(m.width, m.height)
	m.input.Reset()
	return m, nil
}

func (m *Model) executeShowMCPAction() (tea.Model, tea.Cmd) {
	m.mcpOverlay = m.mcpOverlay.Open(m.mcpServers, m.mcpEnabled)
	m.mcpOverlay.OverlayShell = m.mcpOverlay.WithDimensions(m.width, m.height)
	m.input.Reset()
	return m, nil
}

func (m *Model) executeShowUsageAction() (tea.Model, tea.Cmd) {
	m.input.Reset()
	if m.usageOverlay.quota == nil {
		m.content.AppendLine("status: subscription usage is not configured")
		return m, nil
	}
	return m, m.openUsageOverlay()
}

func (m *Model) executeShowLSPAction() (tea.Model, tea.Cmd) {
	m.lspOverlay = m.lspOverlay.Open(m.lspServers, m.lspEnabled)
	m.lspOverlay.OverlayShell = m.lspOverlay.WithDimensions(m.width, m.height)
	m.input.Reset()
	return m, nil
}

func (m *Model) executeToggleThinkingAction() (tea.Model, tea.Cmd) {
	m.input.Reset()
	return m, func() tea.Msg { return toggleThinkingMsg{} }
}

func (m *Model) executeOpenAccentPickerAction() (tea.Model, tea.Cmd) {
	m.accentPicker = m.accentPicker.Open(m.accentPreset)
	m.accentPicker.width = m.width
	m.accentPicker.height = m.height
	m.input.SetValue("/accent ")
	m.input.CursorEnd()
	return m, nil
}

func (m *Model) executeSetAccentAction(preset string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	return m, func() tea.Msg { return setAccentMsg{preset: preset} }
}

// executeModelAction switches the active model, optionally applying a
// session-time reasoning override. reasoning is nil when the switch carries
// no reasoning selection, leaving any previously stored override for
// modelName untouched.
func (m *Model) executeModelAction(modelName string, reasoning *provider.ReasoningOverride) (tea.Model, tea.Cmd) {
	providerBaseURL := m.sidebar.provider
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchModel{Name: modelName, Reasoning: reasoning}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: model %s is not configured", modelName))
			m.input.Reset()
			m.relayoutInput()
			m.syncViewport()
			return m, nil
		}
	}
	if baseURL, ok := m.modelBaseURLs[modelName]; ok {
		providerBaseURL = baseURL
	}
	if reasoning != nil {
		m.reasoningLabels[modelName] = reasoningOverrideLabel(*reasoning)
	}
	m.applyModelSelection(modelName, providerBaseURL)
	m.content.AppendLine(fmt.Sprintf("status: model switched to %s", modelName))
	m.input.Reset()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

func (m *Model) executeOpenProfilePickerAction() (tea.Model, tea.Cmd) {
	m.profilePicker = m.profilePicker.Open(m.profileNames, m.sidebar.profile)
	m.profilePicker.width = m.width
	m.profilePicker.height = m.height
	m.input.SetValue("/profile ")
	m.input.CursorEnd()
	return m, nil
}

func (m *Model) executeSwitchProfileAction(name string) (tea.Model, tea.Cmd) {
	name = strings.TrimSpace(name)
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchProfile{Name: name}); err != nil {
			m.appendError(err)
		} else {
			m.content.AppendLine(fmt.Sprintf("status: profile switched to %s", name))
			m.sidebar.profile = name
		}
	}
	m.input.Reset()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

// openSessionPicker lists resumable sessions and opens the session picker.
// Reports whether it opened, appending a status line and leaving the picker
// closed if the store isn't configured or listing fails.
func (m *Model) openSessionPicker() bool {
	if m.sessionStore == nil {
		m.content.AppendLine("status: no session store configured")
		return false
	}
	entries, err := m.sessionStore.List()
	if err != nil {
		m.content.AppendLine(fmt.Sprintf("status: failed to list sessions: %v", err))
		return false
	}
	m.sessionPicker = m.sessionPicker.Open(entries)
	return true
}

func (m *Model) executeRequestSessionPickerAction() (tea.Model, tea.Cmd) {
	if m.sessionBusy() {
		m.refuseWhileBusy("switch sessions")
		return m, nil
	}
	if !m.openSessionPicker() {
		m.input.Reset()
		m.relayoutInput()
		m.syncViewport()
		return m, nil
	}
	m.input.Reset()
	m.syncInputChrome()
	return m, nil
}

// openOneshotResumePicker lists resumable oneshot runs and opens the picker.
// Reports whether it opened, appending a status line and leaving the picker
// closed if the controller isn't wired, listing fails, or there are no runs.
func (m *Model) openOneshotResumePicker() bool {
	if !m.subAgentsEnabled {
		m.content.AppendLine("status: oneshot unavailable: sub-agents are disabled in config")
		return false
	}

	if m.controller == nil {
		m.content.AppendLine("status: controller not available")
		return false
	}

	sess, ok := sessionController(m.controller)
	if !ok {
		m.content.AppendLine("status: oneshot unavailable: controller does not support this action")
		return false
	}
	projectRoot := sess.ProjectRoot()

	runs, err := oneshot.ListRuns(projectRoot)
	if err != nil {
		m.content.AppendLine(fmt.Sprintf("status: failed to list oneshot runs: %v", err))
		return false
	}

	if len(runs) == 0 {
		m.content.AppendLine("status: no resumable oneshot runs found")
		return false
	}

	m.oneshotResumePicker = m.oneshotResumePicker.Open(runs)
	return true
}

func (m *Model) executeOneshotResumePickerAction() (tea.Model, tea.Cmd) {
	if !m.openOneshotResumePicker() {
		m.input.Reset()
		m.relayoutInput()
		m.syncViewport()
		return m, nil
	}
	m.input.Reset()
	m.syncInputChrome()
	return m, nil
}

func (m *Model) executeForkSessionAction() (tea.Model, tea.Cmd) {
	if m.sessionBusy() {
		m.refuseWhileBusy("fork")
		return m, nil
	}
	// Check if there are any segments/messages in the conversation
	if len(m.content.segments) == 0 {
		m.content.AppendLine("status: no conversation to fork")
		m.input.Reset()
		m.relayoutInput()
		m.syncViewport()
		return m, nil
	}

	m.input.Reset()
	// Delete pending images now, while the store is still bound to the session
	// being left; the load rebinds it asynchronously.
	m.removePendingImages()
	m.relayoutInput()
	m.syncViewport()
	if m.sessionResetCleanup != nil {
		m.sessionResetCleanup()
	}
	m.sessionStartedAt = nil
	m.syncSidebar()
	if m.controller != nil {
		ctrl := m.controller
		return m, func() tea.Msg {
			if err := ctrl.Handle(context.Background(), interactive.ForkSession{}); err != nil {
				return controllerHandleFailedMsg{err: err}
			}
			return nil
		}
	}
	return m, nil
}
func (m *Model) executeSubmitAction(submitText string, displayText string) (tea.Model, tea.Cmd) {
	var sessionCmd tea.Cmd
	newSession := m.sessionStartedAt == nil
	if newSession {
		now := time.Now()
		m.sessionStartedAt = &now
		m.syncSidebar()
		sessionCmd = sessionTickCmd()
	}
	// Capture images before clearing
	images := m.pendingImageBlocks()
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitPrompt{Text: submitText, Images: images}); err != nil {
			if newSession {
				m.sessionStartedAt = nil
				m.syncSidebar()
			}
			m.appendError(err)
			m.input.Reset()
			m.syncInputChrome()
			return m, nil
		}
	}
	m.imageMarkers = nil
	m.clearStrandedResults()
	m.content.AppendUser(displayText)
	m.pruneRosterOnPrompt()
	if len(images) > 0 {
		m.content.AppendImagesAttached(images, m.sidebar.workingDir, m.sidebar.homeDir)
	}
	m.input.Reset()
	m.relayoutInput()
	m.syncViewport()
	return m, sessionCmd
}

// skillExecutionMode returns the execution mode a direct `/skillname`
// invocation should switch the session into, and whether a switch is needed at
// all. The plan skill is the only plan-only workflow; every other skill
// (implement, review, simplify, pull-request, and any project/user-defined
// skill) implies normal workspace editing, so it maps to build mode. The
// security-audit skill reports on the current state rather than editing, so it
// preserves the mode the session is already in and requests no switch.
func skillExecutionMode(skillName string) (config.ExecutionMode, bool) {
	switch skillName {
	case "plan":
		return config.ExecutionModePlan, true
	case "security-audit":
		return "", false
	}
	return config.ExecutionModeBuild, true
}

func (m *Model) executeInvokeSkillAction(skillName, args string) (tea.Model, tea.Cmd) {
	m = m.updateSkillState(skillName, true)

	if m.controller != nil {
		if mode, switchMode := skillExecutionMode(skillName); switchMode {
			if err := m.controller.Handle(context.Background(), interactive.SwitchMode{Mode: mode}); err != nil {
				m.appendError(err)
			}
		}
	}
	m.syncSidebar()

	// Submit the literal "/skillname [args]" text so the model sees an
	// unambiguous invocation, matching each skill's "invoked by name" trigger
	// instead of relying solely on the passive "Active Skills" framing.
	displayText := "/" + skillName
	if args != "" {
		displayText += " " + args
	}
	return m.executeSubmitAction(displayText, displayText)
}
