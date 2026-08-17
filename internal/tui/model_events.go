package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/notify"
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

	// Context report events: display mode is set by the producer; the TUI
	// honours the hint without re-deciding.
	if event.Type == output.EventTypeContextReport {
		if payload, ok := event.Payload.(output.ContextReportEvent); ok && payload.Display == output.ContextReportDisplayOverlay {
			m.contextOverlay = openContextOverlay(payload.Title, payload.Content, m.width, m.height, m.styles, m.content.glamourStyleSheet)
			m.syncViewport()
			return nil
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
		if event.Type == output.EventTypeToolCallFinished || event.Type == output.EventTypeModelCallFinished {
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
		m.setCompaction(compactionState{})
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
	case output.ContextBudgetEvent:
		m.applyContextBudget(payload)
	case output.ContextCompactionEvent:
		m.applyCompactionEvent(payload)
	case output.ContextSessionHealthEvent:
		m.applySessionHealthEvent(payload)
	case output.ContextDiagnosticsEvent:
		if budget, ok := output.AsContextBudgetEvent(payload); ok {
			m.applyContextBudget(budget)
		}
		if compaction, ok := output.AsContextCompactionEvent(payload); ok {
			m.applyCompactionEvent(compaction)
		}
		if health, ok := output.AsContextSessionHealthEvent(payload); ok {
			m.applySessionHealthEvent(health)
		}
	case output.ApprovalEvent:
		switch event.Type {
		case output.EventTypeApprovalRequested:
			// Content retains every request. The tray only prompts the first.
			if !m.approval.active {
				m.approval = approvalState{
					active:         true,
					identity:       payload.CallID,
					tool:           payload.Tool,
					mode:           payload.Mode,
					preview:        payload.Preview,
					kind:           payload.Kind,
					server:         payload.Server,
					mcpToolName:    payload.ToolName,
					selectedAction: 0,
				}
				m.status.mode = "approval"
				m.activity = m.activity.static("approval required", approvalDetail(payload))
				m.input.Reset()
				m.input.Blur()
				m.notifyBlocking(fmt.Sprintf("Tool approval required: %s", payload.Tool))
			}
		case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
			m.approval = approvalState{}
			if !m.promoteNextApproval(payload.CallID) {
				m.status.mode = "running"
				m.input.Focus()
			}
			m.activity = m.activity.static(approvalResultLabel(event.Type), approvalDetail(payload))
		}
	case output.WorkflowHandoffEvent:
		switch event.Type {
		case output.EventTypeWorkflowHandoffRequested:
			m.workflowHandoff = openWorkflowHandoffModal(m.width, m.height, payload, m.workflowHandoffModelSelection(payload.Next))
			m.input.Blur()
			m.notifyBlocking("workflow handoff requested")
			m.syncViewport()
			return nil
		case output.EventTypeWorkflowHandoffAccepted, output.EventTypeWorkflowHandoffDeclined:
			m.workflowHandoff = m.workflowHandoff.close()
			m.input.Focus()
		}
	case output.ToolCallStartedEvent:
		m.activity = m.activity.waiting("running tool", toolCallDetail(payload.Tool))
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
	case output.ModeChangedEvent:
		m.mode = strings.TrimSpace(payload.Mode)
		m.sidebar.execMode = m.mode
		m.status.execMode = m.mode
		m.content.AppendLine(fmt.Sprintf("status: mode → %s", m.mode))
	case output.SandboxStatusEvent:
		m.sidebar.sandboxStatus = strings.TrimSpace(payload.Status)
		m.status.sandboxStatus = strings.TrimSpace(payload.Status)
		if payload.Message != "" {
			m.content.AppendLine(fmt.Sprintf("status: %s", payload.Message))
		}
	case output.ConfigWarningEvent:
		m.content.AppendLine(m.styles.WarningStyle.Render(payload.Message))
	case output.MCPStatusEvent:
		m.applyMCPStatusEvent(payload)
	case output.OneshotFinishedEvent:
		m.oneshotRunning = false
		m.oneshotPhase = ""
		m.oneshotSteerCh = nil
		m.status.oneshotPhase = ""
		m.sidebar.oneshotPhase = ""
		m.syncSidebar()
		m.syncInputChrome()
	}

	var cmds []tea.Cmd
	if event.Type == output.EventTypeToolCallFinished || event.Type == output.EventTypeModelCallFinished {
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

//nolint:gocyclo // approval segment variants stay centralized
func (m *Model) promoteNextApproval(resolvedIdentities ...string) bool {
	identity := ""
	if coordinator, ok := m.controller.(interface{ ApprovalHeadIdentity() string }); ok {
		identity = coordinator.ApprovalHeadIdentity()
	}
	if identity == "" && len(resolvedIdentities) > 0 {
		identity = resolvedIdentities[0]
	}
	for i := range m.content.segments {
		seg := &m.content.segments[i]
		if seg.kind == segmentToolCall && seg.toolData != nil && seg.toolData.approvalPending && !seg.toolData.approvalResolved && (identity == "" || seg.toolData.approvalIdentity == identity) {
			tc := seg.toolData
			m.approval = approvalState{active: true, identity: tc.approvalIdentity, tool: tc.tool, mode: tc.approvalMode, preview: tc.approvalPreview, kind: tc.approvalKind, server: tc.approvalServer, mcpToolName: tc.approvalMCPTool}
			m.status.mode = "approval"
			m.input.Blur()
			return true
		}
		if seg.kind == segmentToolCallGroup && seg.toolGroupData != nil {
			for _, tc := range seg.toolGroupData.entries {
				if tc != nil && tc.approvalPending && !tc.approvalResolved && (identity == "" || tc.approvalIdentity == identity) {
					m.approval = approvalState{active: true, identity: tc.approvalIdentity, tool: tc.tool, mode: tc.approvalMode, preview: tc.approvalPreview, kind: tc.approvalKind, server: tc.approvalServer, mcpToolName: tc.approvalMCPTool}
					m.status.mode = "approval"
					m.input.Blur()
					return true
				}
			}
		}
		if seg.kind == segmentApprovalPill && seg.approvalData != nil && !seg.approvalData.resolved && (identity == "" || seg.approvalData.identity == identity) {
			ad := seg.approvalData
			m.approval = approvalState{active: true, identity: ad.identity, tool: ad.tool, mode: ad.mode, preview: ad.preview, kind: ad.kind, server: ad.server, mcpToolName: ad.mcpToolName}
			m.status.mode = "approval"
			m.input.Blur()
			return true
		}
	}
	return false
}

// applyMCPStatusEvent replaces the TUI's MCP snapshot with the fresh values
// carried by the event, surfaces one warning line per server that newly entered
// a failure state, and recomputes the sidebar's MCP N/N row. The TUI never
// reads the manager or registry: the event is the only data source.
func (m *Model) applyMCPStatusEvent(payload output.MCPStatusEvent) {
	m.mcpEnabled = payload.Enabled
	m.mcpServers = mcpServersFromStatusEvent(payload.Servers)

	// Preserve existing origins when the payload carries none (states-only
	// pre-arm snapshots never include origins). Only replace when the payload
	// carries non-empty origins (post-arm full snapshots with real tool defs).
	// When origins DO arrive, also refresh m.content.mcpToolOrigins so transcript
	// tool attribution catches up once tool defs are registered.
	newOrigins := mcpToolOriginsFromStatusEvent(payload.Origins)
	if len(newOrigins) > 0 {
		m.mcpToolOrigins = newOrigins
		m.content.mcpToolOrigins = newOrigins
	}

	lines, warned := mcpTransitionWarnings(m.mcpServers, m.mcpWarned)
	m.mcpWarned = warned
	for _, line := range lines {
		m.content.AppendLine(m.styles.WarningStyle.Render(line))
	}
	m.syncSidebar()
}

// applyCompactionEvent updates compaction state, activity, and status context
// for a context compaction event. Shared by the direct ContextCompactionEvent
// case and the ContextDiagnosticsEvent envelope.
func (m *Model) applyCompactionEvent(payload output.ContextCompactionEvent) {
	cs := newCompactionState(payload)
	m.setCompaction(cs)
	if cs.Active() {
		m.activity = m.activity.waiting("compacting context", compactingLabel(payload))
	} else {
		m.activity = m.activity.static("context compacted", compactedLabel(payload))
		m.applyContextBudget(payload.BudgetSnapshot())
	}
	m.status.context = appendStatusContext(m.status.context, compactionStatusFragment(payload))
	// Append separator + summary text when compaction finishes with a summary.
	if !cs.Active() && payload.SummaryText != "" {
		m.content.AppendCompactionResult("Compaction", payload.SummaryText)
	}
}

// applySessionHealthEvent updates status context for a context session health
// event. Shared by the direct ContextSessionHealthEvent case and the
// ContextDiagnosticsEvent envelope.
func (m *Model) applySessionHealthEvent(payload output.ContextSessionHealthEvent) {
	if !m.compaction.Active() {
		m.status.context = appendStatusContext(m.status.context, sessionHealthStatusFragment(payload))
	}
}

func (m *Model) resetTopLevelTerminalState(clearInterrupt bool) {
	m.approval = approvalState{}
	m.content.clearApprovalState()
	m.content.ResetAdvisorSegment()
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
		output.EventTypeModelCallFinished:
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
		output.EventTypeModelCallFinished:
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
		_, cmd := m.launchWorkflowHandoff(launch.next, launch.target, launch.submission)
		return cmd
	}
	return nil
}

func (m *Model) workflowHandoffModelSelection(destination string) interactive.WorkflowHandoffModelSelection {
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
	case output.EventTypeRunStarted, output.EventTypeRunFinished, output.EventTypeStopReason, output.EventTypeHistoryLoaded, output.EventTypeContextReport, output.EventTypeToolCallFinished, output.EventTypeAdvisorComplete:
		return false
	default:
		return true
	}
}

// phaseTransitionFailedMsg reports that RotateSessionWithGroup failed while
// handling a phase transition. Update appends the status line to the live
// model, since the tea.Cmd closure that discovers the failure only has a
// copy of Model captured at Cmd-creation time.
type phaseTransitionFailedMsg struct{ err error }

func (m *Model) handlePhaseTransition(payload output.PhaseTransitionEvent) tea.Cmd {
	switch strings.TrimSpace(payload.Status) {
	case "starting":
		// Update model display when oneshot phase transitions to a different model.
		// PhaseTransitionEvent carries the per-phase model alias resolved by
		// phaseModelAlias; the sidebar and statusbar must reflect it.
		if modelName := strings.TrimSpace(payload.Model); modelName != "" {
			m.primaryModel = modelName
			m.status.model = modelName
			m.sidebar.model = modelName
			m.sidebar.contextBudget = m.contextBudgetForModel(modelName)
			m.sidebar.reasoning = m.reasoningLabels[modelName]
			m.sidebar.promptUsed = 0
			m.sidebar.budgetUsed = 0
			if m.sidebar.contextBudget > 0 {
				m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
			} else {
				m.status.context = ""
			}
		}
		// Insert phase divider with phase name
		phaseName := strings.TrimSpace(payload.To)
		if phaseName != "" {
			m.content.AppendPhaseDivider(phaseName)
		}
		// Rotate session with run group to stamp session and reset model context
		controller := m.controller
		runID := payload.RunID
		return func() tea.Msg {
			if controller == nil {
				return nil
			}
			if err := controller.Handle(context.Background(), interactive.RotateSessionWithGroup{
				Group: strings.TrimSpace(runID),
			}); err != nil {
				return phaseTransitionFailedMsg{err: err}
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

func (m *Model) notifyBlocking(reason string) {
	if m.notifier == nil {
		return
	}
	n := notify.Notification{
		Project: filepath.Base(m.sidebar.workingDir),
		Branch:  m.sidebar.branch,
		Reason:  reason,
	}
	go func() {
		_ = m.notifier.Notify(context.Background(), n) //nolint:errcheck // notification failures must not block the TUI
	}()
}
