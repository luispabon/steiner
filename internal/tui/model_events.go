package tui

import (
	"context"
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
	case output.RunFinishedEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
		m.interruptPending = false
	case output.StopReasonEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
	case output.TurnStartedEvent:
		m.status.turn = payload.Turn
		m.sidebar.currentTurn = payload.Turn
		if payload.Model != "" {
			m.status.model = payload.Model
			m.sidebar.model = payload.Model
		}
	case output.ContextDiagnosticsEvent:
		m.applyContextBudget(payload)
		if payload.Kind == "compaction" {
			m.compacting = payload.Severity == "compacting"
			m.content.inCompaction = m.compacting
			if m.compacting {
				m.sidebar.compaction = compactionSidebarSummary(payload)
			} else {
				m.sidebar.compaction = ""
			}
			m.status.context = appendStatusContext(m.status.context, compactionStatusFragment(payload))
		}
		if payload.Kind == "session_health" {
			m.sidebar.compaction = compactionSidebarSummary(payload)
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
			m.input.Reset()
			m.input.Blur()
		case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
			m.approval = approvalState{}
			m.status.mode = "running"
			m.input.Focus()
		}
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
