package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
)

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
	return m, func() tea.Msg { return toggleThinkingMsg{} }
}

func (m Model) executeOpenAccentPickerAction() (tea.Model, tea.Cmd) {
	m.accentPicker = m.accentPicker.Open(m.accentPreset)
	m.accentPicker.width = m.width
	m.accentPicker.height = m.height
	m.input.SetValue("/accent ")
	m.input.CursorEnd()
	m.historyIdx = 0
	return m, nil
}

func (m Model) executeSetAccentAction(preset string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.historyIdx = 0
	return m, func() tea.Msg { return setAccentMsg{preset: preset} }
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

func (m Model) executeOneshotResumePickerAction() (tea.Model, tea.Cmd) {
	if m.controller == nil {
		m.content.AppendLine("status: controller not available")
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	// Type assertion is safe here; we know from wiring in cmd/steiner that
	// the controller is always a *Session
	sess := m.controller.(*interactive.Session)
	projectRoot := sess.ProjectRoot()

	runs, err := oneshot.ListRuns(projectRoot)
	if err != nil {
		m.content.AppendLine(fmt.Sprintf("status: failed to list oneshot runs: %v", err))
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	if len(runs) == 0 {
		m.content.AppendLine("status: no resumable oneshot runs found")
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	m.oneshotResumePicker = m.oneshotResumePicker.Open(runs)
	m.input.Reset()
	m.syncInputChrome()
	return m, nil
}

func (m Model) executeForkSessionAction() (tea.Model, tea.Cmd) {
	// Check if there are any segments/messages in the conversation
	if len(m.content.segments) == 0 {
		m.content.AppendLine("status: no conversation to fork")
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	if m.controller != nil {
		ctrl := m.controller
		return m, func() tea.Msg {
			_ = ctrl.Handle(context.Background(), interactive.ForkSession{})
			return nil
		}
	}
	return m, nil
}

func (m Model) executeSubmitAction(value string, submitText string, displayText string) (tea.Model, tea.Cmd) {
	if value != "" {
		m.inputHistory = append([]string{value}, m.inputHistory...)
		m.historyIdx = 0
	}
	// Capture images before clearing
	images := m.pendingImageBlocks()
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitPrompt{Text: submitText, Images: images}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.imageMarkers = nil
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

func (m Model) executeLaunchOneshotAction(task string) (tea.Model, tea.Cmd) {
	if m.oneshotRunnerFactory == nil {
		m.content.AppendLine("status: oneshot runner factory not configured")
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	if m.controller == nil {
		m.content.AppendLine("status: controller not available")
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	// Create run identity
	runIdentity, err := oneshot.NewRunIdentity(strings.TrimSpace(task))
	if err != nil {
		m.content.AppendLine(fmt.Sprintf("status: launch oneshot failed: %v", err))
		m.syncViewport()
		return m, nil
	}

	// Create steer channel
	m.oneshotSteerCh = make(chan string, 4)
	m.oneshotRunning = true
	m.oneshotPhase = ""

	sessionStore := m.sessionStore

	// Spawn orchestrator goroutine
	go func() {
		// Type assertion is safe here; we know from wiring in cmd/steiner that
		// the controller is always a *Session
		sess := m.controller.(*interactive.Session)

		// Cast sessionStore to oneshot.SessionStore interface
		oneshotSessionStore, ok := sessionStore.(oneshot.SessionStore)
		if !ok {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", "session store does not implement required oneshot interface"))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(runIdentity.ID, nil))
			return
		}

		deps := oneshot.Dependencies{
			ProjectRoot:      sess.ProjectRoot(),
			Identity:         runIdentity,
			Task:             strings.TrimSpace(task),
			Config:           sess.Config(),
			SessionStore:     oneshotSessionStore,
			RunnerFactory:    m.oneshotRunnerFactory(runIdentity),
			Events:           sess.EventSink(),
			SteerCh:          m.oneshotSteerCh,
			InterruptFactory: context.WithCancel,
		}

		orchestrator, err := oneshot.NewOrchestrator(deps)
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot failed: %v", err)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(runIdentity.ID, err))
			return
		}

		manifest, err := orchestrator.Run(context.Background())
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot run failed: %v", err)))
		}
		if manifest.ReportPath != "" {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot report: %s", manifest.ReportPath)))
		}
		sess.EventSink().Emit(output.NewOneshotFinishedEvent(runIdentity.ID, err))

		// Do not close the steer channel — sending to a closed channel panics.
		// The buffered channel becomes inert once the orchestrator goroutine exits;
		// sends hit the select/default branch and the channel is GC'd when replaced.
	}()

	m.content.AppendLine(fmt.Sprintf("status: launching oneshot run for: %s", task))
	m.input.Reset()
	m.historyIdx = 0
	m.syncInputChrome()
	m.syncViewport()
	return m, nil
}

func (m Model) executeResumeOneshotAction(runID string) (tea.Model, tea.Cmd) {
	if m.oneshotRunnerFactory == nil {
		m.content.AppendLine("status: oneshot runner factory not configured")
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	if m.controller == nil {
		m.content.AppendLine("status: controller not available")
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	sessionStore := m.sessionStore

	// Create steer channel
	m.oneshotSteerCh = make(chan string, 4)
	m.oneshotRunning = true
	m.oneshotPhase = ""

	// Spawn orchestrator goroutine
	go func() {
		// Type assertion is safe here; we know from wiring in cmd/steiner that
		// the controller is always a *Session
		sess := m.controller.(*interactive.Session)
		projectRoot := sess.ProjectRoot()

		manifest, err := oneshot.ListRuns(projectRoot)
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("resume oneshot failed: %v", err)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(runID, err))
			return
		}

		var targetRun *oneshot.ResumableRun
		for i := range manifest {
			if manifest[i].RunID == strings.TrimSpace(runID) {
				targetRun = &manifest[i]
				break
			}
		}

		if targetRun == nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot run %q not found", runID)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(runID, nil))
			return
		}

		// Reconstruct RunIdentity from ResumableRun
		identity := oneshot.RunIdentity{
			ID:   targetRun.RunID,
			Slug: targetRun.Slug,
		}

		// Cast sessionStore to oneshot.SessionStore interface
		oneshotSessionStore, ok := sessionStore.(oneshot.SessionStore)
		if !ok {
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(identity.ID, nil))
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", "session store does not implement required oneshot interface"))
			return
		}

		deps := oneshot.Dependencies{
			ProjectRoot:      projectRoot,
			Identity:         identity,
			Task:             targetRun.Task,
			Config:           sess.Config(),
			SessionStore:     oneshotSessionStore,
			RunnerFactory:    m.oneshotRunnerFactory(identity),
			Events:           sess.EventSink(),
			SteerCh:          m.oneshotSteerCh,
			InterruptFactory: context.WithCancel,
		}

		orchestrator, err := oneshot.NewOrchestrator(deps)
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot resume failed: %v", err)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(identity.ID, err))
			return
		}

		runManifest, err := orchestrator.Resume(context.Background())
		sess.EventSink().Emit(output.NewOneshotFinishedEvent(identity.ID, err))
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot resume failed: %v", err)))
		}
		if runManifest.ReportPath != "" {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot report: %s", runManifest.ReportPath)))
		}
	}()

	m.content.AppendLine(fmt.Sprintf("status: resuming oneshot run: %s", runID))
	m.input.Reset()
	m.historyIdx = 0
	m.syncInputChrome()
	m.syncViewport()
	return m, nil
}

// oneshotAllowedAction checks if the input is an allowlisted command during a oneshot run.
func oneshotAllowedAction(value string) bool {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "/exit", trimmed == "/thinking":
		return true
	case strings.HasPrefix(trimmed, "/accent"):
		return true
	default:
		return false
	}
}
