package tui

import "time"

// delegationStallThreshold is how long an active delegation may go without any
// event before the TUI marks it stalled. It is display-only; the model is
// never told.
const delegationStallThreshold = 5 * time.Minute

// noteDelegationEvent records activity for a delegation, scoped or lifecycle.
func (b *contentBuffer) noteDelegationEvent(agentID string) {
	if agentID == "" {
		return
	}
	if b.lastDelegationEvent == nil {
		b.lastDelegationEvent = make(map[string]int64)
	}
	b.lastDelegationEvent[agentID] = nanoNow()
}

// stalledMinutes returns the whole minutes an active, running delegation has
// been silent, or 0 when it is not stalled.
func (b *contentBuffer) stalledMinutes(dd *delegationDisplayState, now time.Time) int {
	if dd == nil || dd.status != "active" || dd.queuedForSlot || dd.cacheWaiting {
		return 0
	}
	last := max(b.lastDelegationEvent[dd.agentID], dd.startTime)
	if last <= 0 {
		return 0
	}
	idle := now.Sub(time.Unix(0, last))
	if idle < delegationStallThreshold {
		return 0
	}
	return int(idle / time.Minute)
}
