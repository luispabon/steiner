package tui

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tui/prefs"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type syncDebounceFiredMsg struct{ seq int }

type modelReasoningResolvedMsg struct {
	capabilities map[string]provider.ReasoningCapabilities
	efforts      map[string]string
}

type updateCheckResultMsg struct {
	latestVersion string
	available     bool
}

type modelEntriesUpdatedMsg struct {
	entries []ModelEntry
	ok      bool
}

type controllerHandleFailedMsg struct{ err error }

// sessionTickInterval is the cadence of the session timer display.
const sessionTickInterval = time.Second

func sessionTickCmd() tea.Cmd {
	return tea.Tick(sessionTickInterval, func(time.Time) tea.Msg {
		return sessionTickMsg{}
	})
}

func syncDebounceCmd(seq int) tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(_ time.Time) tea.Msg {
		return syncDebounceFiredMsg{seq: seq}
	})
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.frame == nil {
		return m.update(msg)
	}
	start := time.Now()
	model, cmd := m.update(msg)
	m.frame.recordUpdate(msg, time.Since(start), m.snapshotFrame())
	return model, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.updateDispatch(msg)
	var cmds []tea.Cmd
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	if blinkCmd := model.(*Model).ensureComposerBlinking(); blinkCmd != nil {
		cmds = append(cmds, blinkCmd)
	}
	model.(*Model).reconcileRosterHover()
	if len(cmds) == 0 {
		return model, nil
	}
	return model, tea.Batch(cmds...)
}

// updateDispatch is the message dispatch logic for Update.
//
//nolint:gocyclo // message dispatch intentionally stays explicit
func (m *Model) updateDispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case toggleThinkingMsg:
		return m.handleToggleThinkingMsg(msg)
	case setAccentMsg:
		return m.handleSetAccentMsg(msg)
	case tickMsg:
		return m.handleTickMsg(msg)
	case sessionTickMsg:
		return m.handleSessionTickMsg(msg)
	case composerBlinkMsg:
		return m.handleComposerBlinkMsg(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg)
	case worktreeCountMsg:
		return m.handleWorktreeCountMsg(msg)
	case runtimeEventMsg:
		return m.handleRuntimeEventMsg(msg)
	case bridgeClosedMsg:
		return m.handleBridgeClosedMsg(msg)
	case workflowHandoffSettledMsg:
		return m.handleWorkflowHandoffSettled(msg)
	case modelEntriesUpdatedMsg:
		return m.handleModelEntriesUpdatedMsg(msg)
	case gitRefreshDoneMsg:
		return m.handleGitRefreshDoneMsg()
	case clipboardFailedMsg:
		return m.handleClipboardFailedMsg(msg)
	case controllerHandleFailedMsg:
		return m.handleControllerHandleFailedMsg(msg)
	case modelReasoningResolvedMsg:
		return m.handleModelReasoningResolvedMsg(msg)
	case quotaUpdateMsg:
		return m.handleQuotaUpdateMsg()
	case quotaRefreshDoneMsg:
		return m.handleQuotaRefreshDoneMsg()
	case usageViewsLoadedMsg:
		return m.handleUsageViewsLoadedMsg(msg)
	case updateCheckResultMsg:
		return m.handleUpdateCheckResultMsg(msg)
	case syncDebounceFiredMsg:
		return m.handleSyncDebounceFiredMsg(msg)
	case resizeReflowFiredMsg:
		return m.handleResizeReflowFiredMsg(msg)
	case clipboardImageMsg:
		return m.handleClipboardImageMsg(msg)
	case phaseTransitionFailedMsg:
		return m.handlePhaseTransitionFailedMsg(msg)
	case sidebarHoverMsg:
		return m.handleSidebarHoverMsg(msg)
	case jumpFlashTickMsg:
		return m.handleJumpFlashTick(msg)
	case mouseClickMsg, mouseMotionMsg, mouseReleaseMsg, mouseWheelMsg, dragAutoScrollTickMsg:
		return m.handleMouseEventMsg(msg)
	case tea.KeyPressMsg:
		return m.handleKeyMsg(msg)
	case tea.PasteMsg:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.relayoutInput()
		return m, cmd
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleGitRefreshDoneMsg() (tea.Model, tea.Cmd) {
	m.syncSidebar()
	return m, nil
}

func (m *Model) handleClipboardFailedMsg(msg clipboardFailedMsg) (tea.Model, tea.Cmd) {
	m.appendError(msg.err)
	m.syncViewport()
	return m, nil
}

func (m *Model) handleControllerHandleFailedMsg(msg controllerHandleFailedMsg) (tea.Model, tea.Cmd) {
	m.appendError(msg.err)
	m.syncViewport()
	return m, nil
}

func (m *Model) handlePhaseTransitionFailedMsg(msg phaseTransitionFailedMsg) (tea.Model, tea.Cmd) {
	m.content.AppendLine(fmt.Sprintf("status: phase transition failed: %v", msg.err))
	return m, nil
}

func (m *Model) handleSidebarHoverMsg(msg sidebarHoverMsg) (tea.Model, tea.Cmd) {
	m.sidebar.rosterHover = msg.agentID
	return m, nil
}

func (m *Model) handleModelEntriesUpdatedMsg(msg modelEntriesUpdatedMsg) (tea.Model, tea.Cmd) {
	if !msg.ok {
		m.modelEntriesUpdates = nil
		return m, nil
	}
	m.modelEntries = cloneModelEntries(msg.entries)
	if m.modelPicker.IsOpen() {
		m.modelPicker = m.modelPicker.ReplaceEntries(m.modelEntries)
	}
	if m.modelEntriesUpdates == nil {
		return m, nil
	}
	return m, waitForModelEntries(m.modelEntriesUpdates)
}

func (m *Model) handleSyncDebounceFiredMsg(msg syncDebounceFiredMsg) (tea.Model, tea.Cmd) {
	if msg.seq == m.syncDebounceSeq && m.contentDirty {
		m.syncViewport()
		m.contentDirty = false
	}
	return m, nil
}

func (m *Model) handleModelReasoningResolvedMsg(msg modelReasoningResolvedMsg) (tea.Model, tea.Cmd) {
	m.modelReasoningCapabilities = msg.capabilities
	m.modelReasoningEfforts = msg.efforts
	m.reasoningBatchResolved = true
	m.reasoningLabels = newReasoningLabels(m.modelReasoningEfforts, m.modelReasoningCapabilities)
	m.status.reasoning = m.reasoningLabels[m.currentModelAlias]
	m.sidebar.reasoning = m.reasoningLabels[m.currentModelAlias]
	m.syncSidebar()
	return m, nil
}

func (m *Model) handleUpdateCheckResultMsg(msg updateCheckResultMsg) (tea.Model, tea.Cmd) {
	if msg.available {
		m.sidebar.updateAvailable = msg.available
		m.sidebar.latestVersion = msg.latestVersion
		m.syncSidebar()
	}
	return m, nil
}

// sessionBusy reports whether a run, tool call, delegation, compaction or
// oneshot is in flight, so session-mutating actions must be refused.
func (m *Model) sessionBusy() bool {
	return m.pendingWorkflowHandoffLaunch != nil || m.asyncConversationBusy() || m.content.HasActiveDelegations() || m.hostWorkBusy()
}

// hostWorkBusy reports the busy signals owned outside the conversation
// driver's state.
func (m *Model) hostWorkBusy() bool {
	return m.content.HasActiveToolCalls() || m.activity.busy() || m.compaction.Active() || m.oneshotRunning
}

// refuseWhileBusy appends the busy notice for the named action and resets the
// composer.
func (m *Model) refuseWhileBusy(action string) {
	m.content.AppendLine("status: cannot " + action + " while a run is in progress")
	m.input.Reset()
	m.relayoutInput()
	m.syncViewport()
}

// clearConversationStateWithError refuses to clear while a run is in
// progress and reports whether the controller accepted the clear. The
// controller remains authoritative: a rejected clear leaves the transcript
// intact.
func (m *Model) clearConversationStateWithError() (tea.Model, bool, error) {
	if m.clearBusy() {
		m.refuseWhileBusy("clear")
		m.syncInputChrome()
		return m, false, nil
	}
	err := m.performClearConversationState()
	if err == nil && m.recorder != nil {
		m.recorder.ResetSession()
		m.syncSidebar()
	}
	return m, err == nil, err
}

// performClearConversationState clears the controller before resetting TUI
// state. It does not check whether a run is active; callers own that decision.
func (m *Model) performClearConversationState() error {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.ClearConversation{}); err != nil {
			m.appendError(err)
			m.syncViewport()
			return err
		}
	}
	m.resetConversationUI()
	return nil
}

// resetConversationUI resets conversation content and TUI chrome without
// issuing a controller request. Callers use it after an atomic clear operation.
func (m *Model) resetConversationUI() {
	if m.sessionResetCleanup != nil {
		m.sessionResetCleanup()
	}
	m.sessionStartedAt = nil
	m.status.mode = ""
	m.removePendingImages()
	m.resetConversationDisplay()
	m.input.Reset()
	m.syncInputChrome()
	m.syncViewport()
}

// resetConversationDisplay drops the transcript and the display state derived
// from it. It does not touch the image store (callers decide whether pending
// images are deleted or merely forgotten). It leaves the composer, status mode and session cleanup callbacks
// alone, so it is safe to run mid-stream when a loaded session replaces the
// conversation.
func (m *Model) resetConversationDisplay() {
	m.content.Clear()
	m.jumpTarget = occurrenceKey{}
	m.jumpAt = time.Time{}
	m.jumpFlashLeft = 0
	m.jumpFlashEpoch++
	m.convState = output.ConversationStateEvent{}
	m.convStateSeen = false
	m.convLabelShown = false
	m.clearStrandedResults()
	m.roster = subAgentRoster{}
	m.selection = m.selection.clear()
	m.clearDragState()
	m.activity = m.activity.clear()
	m.status.approvalActive = false
	m.status.streaming = false
	m.approval = approvalState{}
	m.interruptPending = false
	m.sidebar.promptUsed = 0
	m.sidebar.budgetUsed = 0
	m.status.promptUsed = 0
	m.sidebar.perfDurationMs = 0
	m.sidebar.perfTTFTMs = 0
	m.sidebar.perfOutputTPS = 0
	m.sidebar.currentTurn = 0
	m.sidebar.maxTurns = 0
	for name := range m.enabledSkills {
		m.enabledSkills[name] = false
	}
	if m.sidebar.contextBudget > 0 {
		m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
	} else {
		m.status.context = ""
	}
	m.setCompaction(compactionState{})
	m.syncSidebar()
}

func (m *Model) handleToggleThinkingMsg(_ toggleThinkingMsg) (tea.Model, tea.Cmd) {
	m.showThinking = !m.showThinking
	m.content.showThinking = m.showThinking
	if err := prefs.SetShowThinking(m.showThinking); err != nil {
		m.content.AppendNotice(noticeWarn, fmt.Sprintf("prefs save failed: %v", err), "")
	}
	for i := range m.content.segments {
		if m.content.segments[i].kind == segmentThinkingBlock {
			m.content.segments[i].renderDirty = true
		}
	}
	m.content.gen++
	m.syncViewport()
	return m, nil
}

func (m *Model) handleSetAccentMsg(msg setAccentMsg) (tea.Model, tea.Cmd) {
	accentHex := resolveAccentPreset(msg.preset, rand.Intn)
	if accentHex == "" {
		accentHex = theme.AccentPresets["amber"]
	}
	m.accentPreset = msg.preset
	s := theme.BuildStyles(accentHex, m.resolvedPalette())
	m.styles = &s
	m.content.styles = m.styles
	m.content.setGlamourStyleSheet(accentHex, m.resolvedPalette())
	m.sidebar.styles = m.styles
	m.status.styles = m.styles
	m.activity = m.activity.withStyles(m.styles)
	m.applyInputStyles()
	m.slashOverlay.styles = m.styles
	m.fileList.styles = m.styles
	m.mcpOverlay.styles = m.styles
	m.lspOverlay.styles = m.styles
	m.devLogOverlay.styles = m.styles
	m.usageOverlay.styles = m.styles
	m.filePicker.styles = m.styles
	m.sessionPicker.styles = m.styles
	m.subAgentPicker.styles = m.styles
	m.modelPicker.styles = m.styles
	m.reasoningPicker.styles = m.styles
	m.planPicker.styles = m.styles
	m.accentPicker.styles = m.styles
	m.profilePicker.styles = m.styles
	m.orchestrationPicker.styles = m.styles
	m.oneshotResumePicker.styles = m.styles
	if err := prefs.SetAccent(m.accentPreset); err != nil {
		m.content.AppendNotice(noticeWarn, fmt.Sprintf("prefs save failed: %v", err), "")
	}
	for i := range m.content.segments {
		m.content.segments[i].renderDirty = true
	}
	m.content.gen++
	m.syncViewport()
	return m, nil
}

func (m *Model) handleSessionTickMsg(_ sessionTickMsg) (tea.Model, tea.Cmd) {
	m.syncSidebar()
	if m.sessionStartedAt == nil {
		return m, nil
	}
	return m, sessionTickCmd()
}

func (m *Model) handleComposerBlinkMsg(_ composerBlinkMsg) (tea.Model, tea.Cmd) {
	if !m.input.Focused() {
		m.composerBlinking = false
		return m, nil
	}
	m.composerBlinkOn = !m.composerBlinkOn
	return m, composerBlinkCmd()
}

//nolint:gocyclo // per-tick fan-out intentionally stays explicit
func (m *Model) handleTickMsg(_ tickMsg) (tea.Model, tea.Cmd) {
	m.content.tickCount++
	m.sidebar.tickCount = m.content.tickCount
	if m.usageOverlay.IsOpen() && m.usageOverlay.refreshing() {
		m.usageOverlay.spinnerFrame++
	}
	m.syncRoster()
	m.refreshSubAgentPicker()
	if m.pollLSPStatesFunc != nil {
		m.lspServers = m.pollLSPStatesFunc()
		m.syncSidebar()
	}
	m.activity = m.activity.advance()
	m.status.promptUsed = m.sidebar.promptUsed
	m.status.contextBudget = m.sidebar.contextBudget
	m.syncInputChrome()
	if m.git != nil {
		if err := m.git.takeError(); err != nil {
			m.appendError(err)
			m.syncViewport()
		}
	}
	// Clear any render error captured during the last cycle.
	if m.content.lastRenderErr != nil {
		m.content.lastRenderErr = nil
	}
	// Advance delegation spinners when delegations are in flight.
	if m.content.HasActiveDelegations() {
		m.content.AdvanceDelegationSpinners()
	}
	if m.content.HasActiveToolCalls() {
		m.content.AdvanceToolCallSpinners()
	}
	// Advance compaction spinners when a compaction is in progress.
	if m.content.HasActiveCompactions() {
		m.content.AdvanceCompactionSpinners()
	}
	if m.contentDirty || m.content.streaming || m.compaction.Active() || m.content.HasActiveDelegations() || m.content.HasActiveToolCalls() || m.content.HasActiveCompactions() || m.sidebar.mcpConnecting || m.sidebar.lspStarting {
		m.syncViewport()
		m.contentDirty = false
	}
	if m.needsTicking() {
		m.ticking = true
		return m, tickCmd()
	}
	m.ticking = false
	return m, nil
}

func (m *Model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.fileList.OverlayShell = m.fileList.WithDimensions(msg.Width, msg.Height)
	m.mcpOverlay.OverlayShell = m.mcpOverlay.WithDimensions(msg.Width, msg.Height)
	m.lspOverlay.OverlayShell = m.lspOverlay.WithDimensions(msg.Width, msg.Height)
	m.devLogOverlay.OverlayShell = m.devLogOverlay.WithDimensions(msg.Width, msg.Height)
	m.usageOverlay.OverlayShell = m.usageOverlay.WithDimensions(msg.Width, msg.Height)

	if m.contextOverlay.IsOpen() {
		// An overflowing report that was showing its end stays at the end after the resize.
		wasAtEnd := m.contextOverlay.atEnd()
		m.contextOverlay.OverlayShell = m.contextOverlay.WithDimensions(msg.Width, msg.Height)
		m.contextOverlay = m.contextOverlay.reflow()
		if wasAtEnd {
			m.contextOverlay = m.contextOverlay.scrollToEnd()
		}
	}
	for _, s := range m.confirmModals() {
		s.OverlayShell = s.WithDimensions(msg.Width, msg.Height)
	}
	m.workflowHandoff.OverlayShell = m.workflowHandoff.WithDimensions(msg.Width, msg.Height)

	// Use content area width for bottom-anchored overlays so they don't
	// overflow into the sidebar area.
	contentW := msg.Width
	if m.sidebar.Visible(msg.Width) {
		contentW = msg.Width - sidebarWidth - 1
	}
	m.filePicker.OverlayShell = m.filePicker.WithDimensions(contentW, msg.Height)
	m.slashOverlay.OverlayShell = m.slashOverlay.WithDimensions(contentW, msg.Height)
	m.orchestrationPicker.OverlayShell = m.orchestrationPicker.WithDimensions(contentW, msg.Height)
	m.sessionPicker = m.sessionPicker.withDimensions(contentW, msg.Height)
	m.subAgentPicker = m.subAgentPicker.withDimensions(contentW, msg.Height)
	m.oneshotResumePicker = m.oneshotResumePicker.withDimensions(contentW, msg.Height)
	return m, m.layoutContent(true)
}

func (m *Model) handleRuntimeEventMsg(msg runtimeEventMsg) (tea.Model, tea.Cmd) {
	eventCmd := m.applyEvent(msg.Event)
	var cmds []tea.Cmd
	if eventCmd != nil {
		cmds = append(cmds, eventCmd)
	}
	if tickCmd := m.ensureTicking(); tickCmd != nil {
		cmds = append(cmds, tickCmd)
	}
	if m.external != nil {
		cmds = append(cmds, waitForExternalMsg(m.external))
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) handleBridgeClosedMsg(_ bridgeClosedMsg) (tea.Model, tea.Cmd) {
	m.cancelWorkflowHandoffSettlement()
	return m, nil
}

func (m *Model) handleClipboardImageMsg(msg clipboardImageMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		if !errors.Is(msg.err, ErrClipboardNoImage) {
			m.appendError(msg.err)
			m.syncViewport()
		}
		return m, nil
	}
	label := m.markerLabel(&msg.block)
	m.imageMarkers = append(m.imageMarkers, imageMarker{label: label, image: msg.block})
	m.input.InsertString(label)
	m.syncInputChrome()
	return m, nil
}

// appendError renders err as an error notice in the content view and records
// the full, untrimmed message in the dev log. A leading "status: " prefix is
// dropped from the notice because the notice glyph already marks the line.
func (m *Model) appendError(err error) {
	m.recordDevLog(devLogParams{Severity: devLogError, Source: "tui", Message: err.Error()})
	m.content.AppendNotice(noticeError, strings.TrimPrefix(err.Error(), "status: "), "")
}
