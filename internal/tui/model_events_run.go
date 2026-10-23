package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

// applyRunStartedEvent resets per-run state and shows the run as starting.
func (m *Model) applyRunStartedEvent(payload output.RunStartedEvent) {
	m.interruptPending = false
	m.setCompaction(compactionState{})
	if payload.MaxTurns > 0 {
		m.sidebar.maxTurns = payload.MaxTurns
	}
	m.status.mode = "running"
	m.activity = m.activity.waiting("starting run", strings.TrimSpace(payload.Model))
}

// applyRunFinishedEvent shows the finished run and clears terminal state.
func (m *Model) applyRunFinishedEvent(payload output.RunFinishedEvent) {
	m.status.mode = strings.TrimSpace(payload.Reason)
	m.activity = m.activity.static("run finished", strings.TrimSpace(payload.Reason))
	m.activity.retry = ""
	m.applyConversationLabel()
	m.resetTopLevelTerminalState(true)
}

// applyStopReasonEvent shows a stopped run and clears terminal state.
func (m *Model) applyStopReasonEvent(payload output.StopReasonEvent) {
	m.status.mode = strings.TrimSpace(payload.Reason)
	m.activity = m.activity.static("stopped", strings.TrimSpace(payload.Reason))
	m.activity.retry = ""
	if payload.Error != "" {
		m.stopErrorShown = true
	}
	m.applyConversationLabel()
	m.resetTopLevelTerminalState(true)
}

// applyConversationWarningEvent shows a failed save or compaction as a warning
// notice. A failed run is an error notice, unless a stop reason error already
// reported the failure.
func (m *Model) applyConversationWarningEvent(payload output.ConversationWarningEvent) {
	if payload.Kind != output.ConversationWarningKindRun {
		m.content.AppendNotice(noticeWarn, payload.Message, "")
		return
	}
	if !m.stopErrorShown {
		m.content.AppendNotice(noticeError, payload.Message, "")
	}
}

// applyModelCallFinishedEvent records the model call result and its timings.
func (m *Model) applyModelCallFinishedEvent(payload output.ModelCallFinishedEvent) {
	detail := strings.TrimSpace(payload.FinishReason)
	if detail == "" && payload.CompletionTokens > 0 {
		detail = fmt.Sprintf("%d tokens", payload.CompletionTokens)
	}
	m.activity = m.activity.static("model call complete", detail)
	m.activity.retry = ""
	m.sidebar.perfDurationMs = payload.DurationMs
	m.sidebar.perfTTFTMs = payload.TTFTMs
	m.sidebar.perfOutputTPS = payload.OutputTPS
}

// applyAPIResponseEvent shows the API response as received.
func (m *Model) applyAPIResponseEvent(payload output.APIResponseEvent) {
	detail := strings.TrimSpace(payload.FinishReason)
	m.activity = m.activity.waiting("receiving response", detail)
}
