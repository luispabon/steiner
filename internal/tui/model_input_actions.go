package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func (m *Model) executeInterruptAction() *Model {
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
	m.relayoutInput()
	m.syncViewport()
	return m
}

func (m *Model) executeClearAction() (tea.Model, tea.Cmd) {
	return m.clearConversationState()
}

func (m *Model) executeCompactAction(action inputAction) (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.TriggerManualCompaction{Steering: action.compactionSteering}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.input.Reset()
	m.historyIdx = 0
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

func (m *Model) executeInspectConfigAction() (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.RequestConfigReport{}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.input.Reset()
	m.historyIdx = 0
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
	m.historyIdx = 0
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

func (m *Model) executeToggleSkillAction(skill string, enable bool) (tea.Model, tea.Cmd) {
	m = m.updateSkillState(skill, enable)
	m.input.Reset()
	m.historyIdx = 0
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
	m.historyIdx = 0
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
	m.historyIdx = 0
	return m, nil
}

func (m *Model) executeShowMCPAction() (tea.Model, tea.Cmd) {
	m.mcpOverlay = m.mcpOverlay.Open(m.mcpServers, m.mcpEnabled)
	m.mcpOverlay.OverlayShell = m.mcpOverlay.WithDimensions(m.width, m.height)
	m.input.Reset()
	m.historyIdx = 0
	return m, nil
}

func (m *Model) executeToggleThinkingAction() (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.historyIdx = 0
	return m, func() tea.Msg { return toggleThinkingMsg{} }
}

func (m *Model) executeOpenAccentPickerAction() (tea.Model, tea.Cmd) {
	m.accentPicker = m.accentPicker.Open(m.accentPreset)
	m.accentPicker.width = m.width
	m.accentPicker.height = m.height
	m.input.SetValue("/accent ")
	m.input.CursorEnd()
	m.historyIdx = 0
	return m, nil
}

func (m *Model) executeSetAccentAction(preset string) (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.historyIdx = 0
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
			m.historyIdx = 0
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
	m.historyIdx = 0
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
	m.historyIdx = 0
	return m, nil
}

func (m *Model) executeSwitchProfileAction(name string) (tea.Model, tea.Cmd) {
	name = strings.TrimSpace(name)
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchProfile{Name: name}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		} else {
			m.content.AppendLine(fmt.Sprintf("status: profile switched to %s", name))
			m.sidebar.profile = name
		}
	}
	m.input.Reset()
	m.historyIdx = 0
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
	if m.controller == nil {
		m.content.AppendLine("status: controller not available")
		return false
	}

	// Type assertion is safe here; we know from wiring in cmd/steiner that
	// the controller is always a *Session
	sess := m.controller.(*interactive.Session)
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
		m.historyIdx = 0
		m.relayoutInput()
		m.syncViewport()
		return m, nil
	}
	m.input.Reset()
	m.syncInputChrome()
	return m, nil
}

func (m *Model) executeForkSessionAction() (tea.Model, tea.Cmd) {
	// Check if there are any segments/messages in the conversation
	if len(m.content.segments) == 0 {
		m.content.AppendLine("status: no conversation to fork")
		m.input.Reset()
		m.historyIdx = 0
		m.relayoutInput()
		m.syncViewport()
		return m, nil
	}

	m.input.Reset()
	m.historyIdx = 0
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
			_ = ctrl.Handle(context.Background(), interactive.ForkSession{})
			return nil
		}
	}
	return m, nil
}
func (m *Model) executeSubmitAction(value string, submitText string, displayText string) (tea.Model, tea.Cmd) {
	var sessionCmd tea.Cmd
	if m.sessionStartedAt == nil {
		now := time.Now()
		m.sessionStartedAt = &now
		m.syncSidebar()
		sessionCmd = sessionTickCmd()
	}
	if value != "" {
		m.inputHistory = append([]string{value}, m.inputHistory...)
		m.historyIdx = 0
	}
	// Capture images before clearing
	images := m.pendingImageBlocks()
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitPrompt{Text: submitText, Images: images}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
			m.input.Reset()
			return m, sessionCmd
		}
	}
	m.imageMarkers = nil
	m.content.AppendUser(displayText)
	if len(images) > 0 {
		m.content.AppendImagesAttached(images, m.sidebar.workingDir, m.sidebar.homeDir)
	}
	m.input.Reset()
	m.historyIdx = 0
	m.relayoutInput()
	m.syncViewport()
	return m, sessionCmd
}

// skillExecutionMode returns the execution mode a direct `/skillname`
// invocation should switch the session into. The plan skill is the only
// plan-only workflow; every other skill (implement, review, simplify,
// pull-request, and any project/user-defined skill) implies normal
// workspace editing, so it maps to build mode.
func skillExecutionMode(skillName string) config.ExecutionMode {
	if skillName == "plan" {
		return config.ExecutionModePlan
	}
	return config.ExecutionModeBuild
}

func (m *Model) executeInvokeSkillAction(skillName, args string) (tea.Model, tea.Cmd) {
	m = m.updateSkillState(skillName, true)

	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchMode{Mode: skillExecutionMode(skillName)}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
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
	return m.executeSubmitAction(displayText, displayText, displayText)
}

// oneshotDrainSteers builds a DrainSteers closure over ch, collecting any
// buffered steer messages without blocking.
func oneshotDrainSteers(ch chan agent.SteerMessage) func() []agent.SteerMessage {
	return func() []agent.SteerMessage {
		var msgs []agent.SteerMessage
		for {
			select {
			case msg := <-ch:
				msgs = append(msgs, msg)
			default:
				return msgs
			}
		}
	}
}

// oneshotSessionStoreOrEmit casts sessionStore to oneshot.SessionStore,
// emitting the standard "not configured" failure events on the sink when it
// does not implement the interface.
func oneshotSessionStoreOrEmit(sessionStore interface{}, sink output.EventSink, runID string) (oneshot.SessionStore, bool) {
	oneshotSessionStore, ok := sessionStore.(oneshot.SessionStore)
	if !ok {
		sink.Emit(output.NewOverlayReportEvent("Context Report", "session store does not implement required oneshot interface"))
		sink.Emit(output.NewOneshotFinishedEvent(runID, nil))
		return nil, false
	}
	return oneshotSessionStore, true
}

// runOrchestratorAndReport runs run (Orchestrator.Run or Orchestrator.Resume)
// and emits the standard failed/report/finished event sequence.
func runOrchestratorAndReport(sink output.EventSink, runID, failureLabel string, run func() (oneshot.Manifest, error)) {
	manifest, err := run()
	if err != nil {
		sink.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("%s: %v", failureLabel, err)))
	}
	if manifest.ReportPath != "" {
		sink.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot report: %s", manifest.ReportPath)))
	}
	sink.Emit(output.NewOneshotFinishedEvent(runID, err))
}

// prepareOneshotRun applies the guard checks and steer-channel setup shared
// by launch and resume. ok is false when a guard failed and m already
// carries the corresponding status message and reset input.
func (m *Model) prepareOneshotRun() (*Model, bool) {
	if m.oneshotRunnerFactory == nil {
		m.content.AppendLine("status: oneshot runner factory not configured")
		m.input.Reset()
		m.historyIdx = 0
		m.relayoutInput()
		m.syncViewport()
		return m, false
	}

	if m.controller == nil {
		m.content.AppendLine("status: controller not available")
		m.input.Reset()
		m.historyIdx = 0
		m.relayoutInput()
		m.syncViewport()
		return m, false
	}

	m.oneshotSteerCh = make(chan agent.SteerMessage, 4)
	m.oneshotRunning = true
	m.oneshotPhase = ""
	return m, true
}

func (m *Model) executeLaunchOneshotAction(task string) (tea.Model, tea.Cmd) {
	m, ok := m.prepareOneshotRun()
	if !ok {
		return m, nil
	}

	// Create run identity
	runIdentity, err := oneshot.NewRunIdentity(strings.TrimSpace(task))
	if err != nil {
		m.content.AppendLine(fmt.Sprintf("status: launch oneshot failed: %v", err))
		m.oneshotRunning = false
		m.oneshotSteerCh = nil
		m.syncViewport()
		return m, nil
	}

	sessionStore := m.sessionStore

	// Spawn orchestrator goroutine
	go func() {
		// Type assertion is safe here; we know from wiring in cmd/steiner that
		// the controller is always a *Session
		sess := m.controller.(*interactive.Session)

		oneshotSessionStore, ok := oneshotSessionStoreOrEmit(sessionStore, sess.EventSink(), runIdentity.ID)
		if !ok {
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
			DrainSteers:      oneshotDrainSteers(m.oneshotSteerCh),
			InterruptFactory: context.WithCancel,
		}

		orchestrator, err := oneshot.NewOrchestrator(deps)
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot failed: %v", err)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(runIdentity.ID, err))
			return
		}

		runOrchestratorAndReport(sess.EventSink(), runIdentity.ID, "oneshot run failed", func() (oneshot.Manifest, error) {
			return orchestrator.Run(context.Background())
		})

		// Do not close the steer channel — sending to a closed channel panics.
		// The buffered channel becomes inert once the orchestrator goroutine exits;
		// sends hit the select/default branch and the channel is GC'd when replaced.
	}()

	m.content.AppendLine(fmt.Sprintf("status: launching oneshot run for: %s", task))
	m.input.Reset()
	m.historyIdx = 0
	m.syncInputChrome()
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

func (m *Model) executeResumeOneshotAction(runID string) (tea.Model, tea.Cmd) {
	m, ok := m.prepareOneshotRun()
	if !ok {
		return m, nil
	}

	sessionStore := m.sessionStore

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

		oneshotSessionStore, ok := oneshotSessionStoreOrEmit(sessionStore, sess.EventSink(), identity.ID)
		if !ok {
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
			DrainSteers:      oneshotDrainSteers(m.oneshotSteerCh),
			InterruptFactory: context.WithCancel,
		}

		orchestrator, err := oneshot.NewOrchestrator(deps)
		if err != nil {
			sess.EventSink().Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("oneshot resume failed: %v", err)))
			sess.EventSink().Emit(output.NewOneshotFinishedEvent(identity.ID, err))
			return
		}

		runOrchestratorAndReport(sess.EventSink(), identity.ID, "oneshot resume failed", func() (oneshot.Manifest, error) {
			return orchestrator.Resume(context.Background())
		})
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
