package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

const (
	conversationStateIdle       = "idle"
	conversationStateGenerating = "generating"
	conversationStateWaiting    = "waiting"
)

// applyConversationState records the driver's state, the only source for it,
// and derives the parent activity label while the conversation is waiting.
func (m *Model) applyConversationState(payload output.ConversationStateEvent) {
	m.convState = payload
	m.convStateSeen = true
	m.content.asyncMode = true
	m.applyConversationLabel()
	m.syncInputChrome()
	m.syncSidebar()
	m.syncViewport()
}

// applyConversationLabel sets the activity label for the waiting states and
// clears it once the conversation leaves them. Generating and idle labels stay
// with the per-run events.
func (m *Model) applyConversationLabel() {
	if !m.convStateSeen {
		return
	}
	if label := conversationWaitingLabel(m.convState); label != "" {
		m.activity = m.activity.static(label, "")
		m.convLabelShown = true
		return
	}
	if m.convLabelShown {
		m.activity = m.activity.clear()
		m.convLabelShown = false
	}
}

func conversationWaitingLabel(s output.ConversationStateEvent) string {
	if s.State != conversationStateWaiting {
		return ""
	}
	switch {
	case s.Held:
		return fmt.Sprintf("paused — %d results waiting; send a message to continue", s.Pending)
	case s.BudgetExhausted:
		return fmt.Sprintf("token budget reached — %d sub-agents still running", s.Pending)
	default:
		return fmt.Sprintf("waiting on %d sub-agents", s.Pending)
	}
}

// driverGenerating reports whether the conversation driver is mid-turn.
func (m *Model) driverGenerating() bool {
	return m.convStateSeen && m.convState.State == conversationStateGenerating
}

// asyncConversationBusy reports whether the driver has a turn or sub-agent
// results outstanding.
func (m *Model) asyncConversationBusy() bool {
	return m.convStateSeen && (m.convState.State != conversationStateIdle || m.convState.Pending > 0)
}

// compactBusy is sessionBusy for manual compaction: while the driver is only
// waiting, compaction is allowed; a generating turn still blocks it.
func (m *Model) compactBusy() bool {
	if !m.convStateSeen {
		return m.sessionBusy()
	}
	return m.driverGenerating() || m.content.HasActiveToolCalls() || m.compaction.Active() || m.oneshotRunning
}

// keepsFlowingWhileInterrupted reports whether an event must not be swallowed
// by the post-interrupt filter: in async mode delegates outlive a stopped turn.
func (m *Model) keepsFlowingWhileInterrupted(event output.Event) bool {
	if !m.content.asyncMode {
		return false
	}
	return event.Scope.AgentID != "" || strings.HasPrefix(event.Type, "delegation_")
}
