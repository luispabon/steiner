package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
)

//nolint:gocyclo // event fan-out stays centralized here
func (m *Model) applyEvent(event output.Event) tea.Cmd {
	if m.shouldSuppressInterruptedRunEvent(event) {
		return nil
	}
	if m.suppressWorkflowHandoffRun {
		if cmd := m.handleSuppressedWorkflowHandoffEvent(event); cmd != nil || m.shouldSuppressWorkflowHandoffEvent(event) {
			return cmd
		}
	}

	// Context report events: short single-line content goes to the transcript;
	// long or multi-line content opens the overlay.
	if event.Type == output.EventTypeContextReport {
		if payload, ok := event.Payload.(output.ContextReportEvent); ok {
			if strings.Contains(payload.Content, "\n") || len(payload.Content) > 100 {
				m.contextOverlay = openContextOverlay(payload.Title, payload.Content, m.width, m.height, m.styles, m.content.glamourStyleSheet)
				m.syncViewport()
				return nil
			}
		}
	}

	if event.Type != output.EventTypeHistoryLoaded {
		m.content.AppendEvent(event)
	}

	// Sub-agent events update delegation transcript segments only.
	// They must not overwrite the main agent's sidebar, status bar,
	// or activity state.
	if event.Scope.AgentID != "" {
		var cmds []tea.Cmd
		if event.Type == output.EventTypeToolCallFinished || event.Type == output.EventTypeTurnFinished {
			cmds = append(cmds, gitRefreshCmd(m.git))
		}
		if event.Type != output.EventTypeAssistantChunk && event.Type != output.EventTypeThinkingChunk {
			m.contentDirty = true
			m.syncDebounceSeq++
			cmds = append(cmds, syncDebounceCmd(m.syncDebounceSeq))
		}
		return tea.Batch(cmds...)
	}

	switch payload := event.Payload.(type) {
	case output.HistoryLoadedEvent:
		if len(payload.Prompts) > 0 {
			for i, j := 0, len(payload.Prompts)-1; i < j; i, j = i+1, j-1 {
				payload.Prompts[i], payload.Prompts[j] = payload.Prompts[j], payload.Prompts[i]
			}
		}
		m.fileHistory = payload.Prompts
		m.fileHistoryIdx = -1
		return nil
	case output.RunStartedEvent:
		m.interruptPending = false
		m.compacting = false
		m.content.inCompaction = false
		if payload.MaxTurns > 0 {
			m.sidebar.maxTurns = payload.MaxTurns
		}
		m.status.mode = "running"
		m.activity = m.activity.waiting("starting run", strings.TrimSpace(payload.Model))
	case output.RunFinishedEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
		m.activity = m.activity.static("run finished", strings.TrimSpace(payload.Reason))
		m.resetTopLevelTerminalState(true)
	case output.StopReasonEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
		m.activity = m.activity.static("stopped", strings.TrimSpace(payload.Reason))
		m.resetTopLevelTerminalState(true)
	case output.TurnStartedEvent:
		m.sidebar.currentTurn = payload.Turn
		m.activity = m.activity.waiting("waiting on model", turnLabel(payload.Turn))
	case output.ModelCallStartedEvent:
		m.activity = m.activity.waiting("waiting on model", strings.TrimSpace(payload.Model))
	case output.ModelCallFinishedEvent:
		detail := strings.TrimSpace(payload.FinishReason)
		if detail == "" && payload.CompletionTokens > 0 {
			detail = fmt.Sprintf("%d tokens", payload.CompletionTokens)
		}
		m.activity = m.activity.static("model call complete", detail)
		m.sidebar.perfDurationMs = payload.DurationMs
		m.sidebar.perfTTFTMs = payload.TTFTMs
		m.sidebar.perfOutputTPS = payload.OutputTPS
	case output.APIRequestEvent:
		m.activity = m.activity.waiting("waiting on model", strings.TrimSpace(payload.Model))
	case output.APIResponseEvent:
		detail := strings.TrimSpace(payload.FinishReason)
		m.activity = m.activity.waiting("receiving response", detail)
	case output.ContextDiagnosticsEvent:
		m.applyContextBudget(payload)
		if payload.Kind == "compaction" {
			m.compacting = payload.Severity == "compacting"
			m.content.inCompaction = m.compacting
			if m.compacting {
				m.sidebar.compaction = compactionSidebarSummary(payload)
				m.activity = m.activity.waiting("compacting context", compactingLabel(payload))
			} else {
				m.sidebar.compaction = ""
				m.activity = m.activity.static("context compacted", compactedLabel(payload))
			}
			m.status.context = appendStatusContext(m.status.context, compactionStatusFragment(payload))
			// Append separator + summary text when compaction finishes with a summary.
			if payload.Severity != "compacting" && payload.SummaryText != "" {
				m.content.AppendCompactionResult("Compaction", payload.SummaryText)
			}
		}
		if payload.Kind == "session_health" {
			if m.compacting {
				m.sidebar.compaction = compactionSidebarSummary(payload)
			} else {
				m.sidebar.compaction = ""
			}
			if !m.compacting {
				m.status.context = appendStatusContext(m.status.context, compactionStatusFragment(payload))
			}
			m.sessionHealthCompactionCount = payload.CompactionCount
			m.sessionHealthTurn = payload.Turn
			m.sessionHealthState = payload.SessionState
			m.sessionHealthGuidance = payload.RestartGuidance
			m.sessionHealthNotes = append([]string(nil), payload.Notes...)
		}
	case output.ApprovalEvent:
		switch event.Type {
		case output.EventTypeApprovalRequested:
			// Guard: do not open a new approval tray if one is already active
			// (prevents visual duplicate when content buffer pill is already present).
			if !m.approval.active {
				m.approval = approvalState{
					active:         true,
					tool:           payload.Tool,
					mode:           payload.Mode,
					preview:        payload.Preview,
					selectedAction: 0,
				}
				m.status.mode = "approval"
				m.activity = m.activity.static("approval required", approvalDetail(payload))
				m.input.Reset()
				m.input.Blur()
			}
		case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
			m.approval = approvalState{}
			m.status.mode = "running"
			m.activity = m.activity.static(approvalResultLabel(event.Type), approvalDetail(payload))
			m.input.Focus()
		}
	case output.WorkflowHandoffEvent:
		switch event.Type {
		case output.EventTypeWorkflowHandoffRequested:
			m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, payload, m.workflowHandoffModelSelection(payload.Next))
			m.input.Blur()
			m.syncViewport()
			return nil
		case output.EventTypeWorkflowHandoffAccepted, output.EventTypeWorkflowHandoffDeclined:
			m.workflowHandoff = m.workflowHandoff.close()
			m.input.Focus()
		}
	case output.ToolCallStartedEvent:
		m.activity = m.activity.waiting("running tool", toolCallDetail(payload.Tool, payload.Arguments))
	case output.ToolCallFinishedEvent:
		m.activity = m.activity.static("tool complete", strings.TrimSpace(payload.Tool))
	case output.SteerReceivedEvent:
		m.content.AppendUser(payload.Text)
		m.steerQueued = false
		m.syncInputChrome()
	case output.PhaseTransitionEvent:
		return m.handlePhaseTransition(payload)
	case output.PhaseIndicatorEvent:
		m.handlePhaseIndicator(payload)
	}

	var cmds []tea.Cmd
	if event.Type == output.EventTypeToolCallFinished || event.Type == output.EventTypeTurnFinished {
		cmds = append(cmds, gitRefreshCmd(m.git))
	}
	m.syncInputChrome()
	m.syncSidebar()
	if event.Type != output.EventTypeAssistantChunk && event.Type != output.EventTypeThinkingChunk {
		m.contentDirty = true
		m.syncDebounceSeq++
		cmds = append(cmds, syncDebounceCmd(m.syncDebounceSeq))
	}
	return tea.Batch(cmds...)
}

func (m *Model) resetTopLevelTerminalState(clearInterrupt bool) {
	m.approval = approvalState{}
	m.content.clearApprovalState()
	if m.workflowHandoff.IsOpen() {
		m.workflowHandoff = m.workflowHandoff.close()
	}
	if clearInterrupt {
		m.interruptPending = false
	}
	m.input.Focus()
	m.syncInputChrome()
	m.syncSidebar()
	m.syncViewport()
}

func (m *Model) shouldSuppressWorkflowHandoffEvent(event output.Event) bool {
	switch event.Type {
	case output.EventTypeWorkflowHandoffAccepted,
		output.EventTypeToolCallFinished,
		output.EventTypeTurnFinished:
		return true
	default:
		return false
	}
}

func (m *Model) handleSuppressedWorkflowHandoffEvent(event output.Event) tea.Cmd {
	if !m.suppressWorkflowHandoffRun {
		return nil
	}
	switch event.Type {
	case output.EventTypeWorkflowHandoffAccepted,
		output.EventTypeToolCallFinished,
		output.EventTypeTurnFinished:
		return nil
	case output.EventTypeStopReason:
		payload, ok := event.Payload.(output.StopReasonEvent)
		if !ok || payload.Reason != "workflow_handoff" {
			return nil
		}
		launch := m.pendingWorkflowHandoffLaunch
		m.suppressWorkflowHandoffRun = false
		m.pendingWorkflowHandoffLaunch = nil
		m.status.mode = ""
		m.activity = m.activity.clear()
		if launch == nil {
			return nil
		}
		next, cmd := m.launchWorkflowHandoff(launch.next, launch.target, launch.modelName)
		if updated, ok := next.(Model); ok {
			*m = updated
		}
		return cmd
	}
	return nil
}

func (m Model) workflowHandoffModelSelection(destination string) interactive.WorkflowHandoffModelSelection {
	if selector, ok := m.controller.(interactive.WorkflowHandoffModelSelector); ok {
		selection := selector.WorkflowHandoffModelSelection(destination)
		if strings.TrimSpace(selection.ModelAlias) != "" {
			if strings.TrimSpace(selection.SourceLabel) == "" {
				selection.SourceLabel = "current session"
			}
			return selection
		}
	}

	modelAlias := strings.TrimSpace(m.primaryModel)
	if modelAlias == "" {
		return interactive.WorkflowHandoffModelSelection{}
	}
	return interactive.WorkflowHandoffModelSelection{
		ModelAlias:  modelAlias,
		SourceLabel: "current session",
	}
}

func (m *Model) shouldSuppressInterruptedRunEvent(event output.Event) bool {
	if !m.interruptPending {
		return false
	}
	switch event.Type {
	case output.EventTypeRunStarted, output.EventTypeRunFinished, output.EventTypeStopReason, output.EventTypeHistoryLoaded, output.EventTypeContextReport, output.EventTypeToolCallFinished:
		return false
	default:
		return true
	}
}

func (m *Model) handlePhaseTransition(payload output.PhaseTransitionEvent) tea.Cmd {
	switch strings.TrimSpace(payload.Status) {
	case "starting":
		// Insert phase divider with phase name
		phaseName := strings.TrimSpace(payload.To)
		if phaseName != "" {
			m.content.AppendPhaseDivider(phaseName)
		}
		// Rotate session with run group to stamp session and reset model context
		return func() tea.Msg {
			if m.controller != nil {
				if err := m.controller.Handle(context.Background(), interactive.RotateSessionWithGroup{
					Group: strings.TrimSpace(payload.RunID),
				}); err != nil {
					m.content.AppendLine(fmt.Sprintf("status: phase transition failed: %v", err))
				}
			}
			return nil
		}
	case "completed", "failed":
		m.activity = m.activity.static("phase "+strings.TrimSpace(payload.Status), strings.TrimSpace(payload.To))
	}
	return nil
}

func (m *Model) handlePhaseIndicator(payload output.PhaseIndicatorEvent) {
	phaseName := strings.TrimSpace(payload.Phase)
	state := strings.TrimSpace(payload.State)
	message := strings.TrimSpace(payload.Message)

	// Update status mode to show phase and state
	modeText := phaseName
	if state != "" && state != "running" {
		modeText = fmt.Sprintf("%s (%s)", phaseName, state)
	}
	m.status.mode = modeText
	m.oneshotPhase = phaseName
	m.status.oneshotPhase = phaseName
	m.sidebar.oneshotPhase = phaseName

	// Update activity
	if message != "" {
		m.activity = m.activity.waiting(state, message)
	} else {
		m.activity = m.activity.waiting(state, phaseName)
	}
}
