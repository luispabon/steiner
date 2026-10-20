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

//nolint:gocyclo // pre-routing guards run in a fixed order
func (m *Model) applyEvent(event output.Event) tea.Cmd {
	if payload, ok := event.Payload.(output.ConversationStateEvent); ok {
		m.applyConversationState(payload)
		return nil
	}
	m.observeRoster(event)
	if event.Scope.AgentID == "" && event.Type == output.EventTypeStopReason && m.pendingWorkflowHandoffLaunch != nil {
		if payload, ok := event.Payload.(output.StopReasonEvent); !ok || payload.Reason != "workflow_handoff" {
			m.cancelWorkflowHandoffSettlement()
		}
	}
	if m.shouldSuppressInterruptedRunEvent(event) {
		return nil
	}
	if m.suppressWorkflowHandoffRun {
		if cmd := m.handleSuppressedWorkflowHandoffEvent(event); cmd != nil {
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

	// Sub-agent events are handled by applySubAgentEvent.
	if event.Scope.AgentID != "" {
		return m.applySubAgentEvent(event)
	}

	if event.Type != output.EventTypeHistoryLoaded {
		m.content.AppendEvent(event)
	}
	if cmd, done := m.applyPayloadEvent(event); done {
		return cmd
	}
	return m.finishAppliedEvent(event)
}

// applyPayloadEvent routes the event payload to its family handler. It
// reports done when applyEvent must return cmd without the shared tail.
//
//nolint:gocyclo // payload fan-out stays centralized here
func (m *Model) applyPayloadEvent(event output.Event) (tea.Cmd, bool) {
	switch payload := event.Payload.(type) {
	case output.HistoryLoadedEvent:
		// Copy before reversing: the payload slice belongs to the producer and
		// is also retained by the transcript, so reversing or aliasing it in
		// place would corrupt both. A nil or empty snapshot carries no history,
		// so it must not clear prompts the user is already browsing.
		if len(payload.Prompts) > 0 {
			prompts := make([]string, len(payload.Prompts))
			copy(prompts, payload.Prompts)
			for i, j := 0, len(prompts)-1; i < j; i, j = i+1, j-1 {
				prompts[i], prompts[j] = prompts[j], prompts[i]
			}
			m.fileHistory = prompts
			m.fileHistoryIdx = -1
		}
		return nil, true
	case output.RunStartedEvent:
		m.applyRunStartedEvent(payload)
	case output.RunFinishedEvent:
		m.applyRunFinishedEvent(payload)
	case output.StopReasonEvent:
		m.applyStopReasonEvent(payload)
	case output.ModelCallStartedEvent:
		m.activity = m.activity.waiting("waiting", "")
	case output.ModelCallFinishedEvent:
		m.applyModelCallFinishedEvent(payload)
	case output.APIRequestEvent:
		m.activity = m.activity.waiting("waiting", "")
	case output.APIResponseEvent:
		m.applyAPIResponseEvent(payload)
	case output.ContextBudgetEvent:
		m.applyContextBudgetEvent(payload)
	case output.ContextCompactionEvent:
		m.applyCompactionEvent(payload)
	case output.ContextSessionHealthEvent:
		m.applySessionHealthEvent(payload)
	case output.SubAgentResultsUnansweredEvent:
		m.applyStrandedResults(payload)
	case output.ContextDiagnosticsEvent:
		m.applyContextDiagnosticsEvent(payload)
	case output.ApprovalEvent:
		m.applyApprovalEvent(event, payload)
	case output.WorkflowHandoffEvent:
		if m.applyWorkflowHandoffEvent(event, payload) {
			return nil, true
		}
	case output.ToolCallStartedEvent:
		m.activity = m.activity.waiting("running tool", toolCallDetail(payload.Tool, payload.Arguments))
	case output.ToolCallFinishedEvent:
		m.activity = m.activity.static("tool complete", strings.TrimSpace(payload.Tool))
	case output.SteerReceivedEvent:
		m.applySteerReceivedEvent(payload)
	case output.PhaseTransitionEvent:
		return m.handlePhaseTransition(payload), true
	case output.PhaseIndicatorEvent:
		m.handlePhaseIndicator(payload)
	case output.ModeChangedEvent:
		m.applyModeChangedEvent(payload)
	case output.OrchestrationLevelChangedEvent:
		m.applyOrchestrationLevelChangedEvent(payload)
	case output.SandboxStatusEvent:
		m.applySandboxStatusEvent(payload)
	case output.ConfigWarningEvent:
		m.content.AppendLine(m.styles.WarningStyle.Render(payload.Message))
	case output.SkillTruncatedEvent:
		m.content.AppendLine(m.styles.WarningStyle.Render(payload.Message()))
	case output.SkillStateEvent:
		m.content.AppendLine(fmt.Sprintf("status: skill %s %s", payload.Name, payload.State))
	case output.MCPStatusEvent:
		m.applyMCPStatusEvent(payload)
	case output.ConversationResetEvent:
		m.applyConversationReset()
	case output.OneshotFinishedEvent:
		m.oneshotRunning = false
		m.oneshotPhase = ""
		m.status.oneshotPhase = ""
		m.sidebar.oneshotPhase = ""
		m.syncSidebar()
		m.syncInputChrome()
	}
	return nil, false
}

// finishAppliedEvent runs the shared post-apply work for events that
// applyPayloadEvent did not end early.
func (m *Model) finishAppliedEvent(event output.Event) tea.Cmd {
	var cmds []tea.Cmd
	if shouldRefreshGit(event) {
		cmds = append(cmds, gitRefreshCmd(m.git))
	}
	if _, ok := event.Payload.(output.RunFinishedEvent); ok {
		cmds = append(cmds, m.usageRunFinishedCmd())
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
		m.content.renderEpoch++
	}

	lines, warned := mcpTransitionWarnings(m.mcpServers, m.mcpWarned)
	m.mcpWarned = warned
	for _, line := range lines {
		m.content.AppendLine(m.styles.WarningStyle.Render(line))
	}
	m.syncSidebar()
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
	m.relayoutInput()
	m.syncSidebar()
	m.syncViewport()
}

func (m *Model) handleSuppressedWorkflowHandoffEvent(event output.Event) tea.Cmd {
	if !m.suppressWorkflowHandoffRun {
		return nil
	}
	if event.Type != output.EventTypeStopReason {
		return nil
	}
	payload, ok := event.Payload.(output.StopReasonEvent)
	if !ok || payload.Reason != "workflow_handoff" {
		return nil
	}
	if launch := m.pendingWorkflowHandoffLaunch; launch != nil && !launch.waiting {
		launch.waiting = true
		return beginWorkflowHandoffSettlement(m.controller, launch)
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
	if !m.interruptPending || m.keepsFlowingWhileInterrupted(event) {
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
		// Use applyModelSelection to ensure consistent model, provider, and alias updates.
		if modelName := strings.TrimSpace(payload.Model); modelName != "" {
			providerBaseURL := ""
			if baseURL, ok := m.modelBaseURLs[modelName]; ok {
				providerBaseURL = baseURL
			}
			m.applyModelSelection(modelName, providerBaseURL)
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
				return controllerHandleFailedMsg{err: err}
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
