package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (m *Model) applyEvent(event output.Event) {
	if m.shouldSuppressInterruptedRunEvent(event) {
		return
	}

	// Context report events open the overlay instead of going into the transcript.
	if event.Type == output.EventTypeContextReport {
		if payload, ok := event.Payload.(output.ContextReportEvent); ok {
			m.contextOverlay = openContextOverlay(payload.Title, payload.Content, m.width, m.height, m.styles, m.content.glamourStyleSheet)
		}
		m.syncViewport()
		return
	}

	if event.Type != output.EventTypeHistoryLoaded {
		m.content.AppendEvent(event)
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
		return
	case output.RunStartedEvent:
		m.interruptPending = false
		m.compacting = false
		m.content.inCompaction = false
		if payload.Model != "" {
			m.status.model = payload.Model
			m.sidebar.model = payload.Model
		}
		if payload.MaxTurns > 0 {
			m.sidebar.maxTurns = payload.MaxTurns
		}
		m.status.mode = "running"
		m.activity = m.activity.waiting("starting run", strings.TrimSpace(payload.Model))
	case output.RunFinishedEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
		m.interruptPending = false
		m.activity = m.activity.static("run finished", strings.TrimSpace(payload.Reason))
	case output.StopReasonEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
		m.activity = m.activity.static("stopped", strings.TrimSpace(payload.Reason))
	case output.TurnStartedEvent:
		m.status.turn = payload.Turn
		m.sidebar.currentTurn = payload.Turn
		if payload.Model != "" {
			m.status.model = payload.Model
			m.sidebar.model = payload.Model
		}
		m.activity = m.activity.waiting("waiting on model", turnLabel(payload.Turn))
	case output.ModelCallStartedEvent:
		m.activity = m.activity.waiting("waiting on model", strings.TrimSpace(payload.Model))
	case output.ModelCallFinishedEvent:
		detail := strings.TrimSpace(payload.FinishReason)
		if detail == "" && payload.TotalTokens > 0 {
			detail = fmt.Sprintf("%d tokens", payload.TotalTokens)
		}
		m.activity = m.activity.static("model call complete", detail)
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
		case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
			m.approval = approvalState{}
			m.status.mode = "running"
			m.activity = m.activity.static(approvalResultLabel(event.Type), approvalDetail(payload))
			m.input.Focus()
		}
	case output.ToolCallStartedEvent:
		m.activity = m.activity.waiting("running tool", toolCallDetail(payload.Tool, payload.Arguments))
	case output.ToolCallFinishedEvent:
		m.activity = m.activity.static("tool complete", strings.TrimSpace(payload.Tool))
	}

	if event.Type == output.EventTypeToolCallFinished || event.Type == output.EventTypeTurnFinished {
		m.git.Refresh(context.Background())
	}
	m.syncInputChrome()
	m.syncSidebar()
	m.syncViewport()
}

func (m *Model) shouldSuppressInterruptedRunEvent(event output.Event) bool {
	if !m.interruptPending {
		return false
	}
	switch event.Type {
	case output.EventTypeRunStarted, output.EventTypeRunFinished, output.EventTypeStopReason, output.EventTypeHistoryLoaded, output.EventTypeContextReport:
		return false
	default:
		return true
	}
}
