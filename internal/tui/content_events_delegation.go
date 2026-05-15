package tui

import (
	"fmt"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) appendDelegationEvent(event output.Event) {
	b.finishStreaming()
	if b.activeDelegations == nil {
		b.activeDelegations = make(map[string]int)
	}
	switch event.Type {
	case output.EventTypeDelegationStarted:
		b.handleDelegationStarted(event)
	case output.EventTypeDelegationComplete:
		b.handleDelegationComplete(event)
	case output.EventTypeDelegationFailed:
		b.handleDelegationFailed(event)
	default:
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
	}
}

func (b *contentBuffer) appendScopedDelegationEvent(event output.Event) bool {
	agentID := event.Scope.AgentID
	if agentID == "" {
		return false
	}
	if idx, active := b.activeDelegations[agentID]; active {
		b.markDelegationDirty(idx)
		return true
	}
	if idx, found := b.findDelegationSegment(agentID); found {
		b.markDelegationDirty(idx)
		return true
	}
	return false
}

func (b *contentBuffer) findDelegationSegment(agentID string) (int, bool) {
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := b.segments[i]
		if seg.kind != segmentDelegation || seg.delegData == nil {
			continue
		}
		if seg.delegData.agentID == agentID {
			return i, true
		}
	}
	return 0, false
}

func (b *contentBuffer) markDelegationDirty(idx int) {
	if idx < 0 || idx >= len(b.segments) {
		return
	}
	if b.segments[idx].kind != segmentDelegation || b.segments[idx].delegData == nil {
		return
	}
	b.segments[idx].renderDirty = true
}

func (b *contentBuffer) handleDelegationStarted(event output.Event) {
	payload, ok := event.Payload.(output.DelegationStartedEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	preview := payload.TaskPreview
	if runes := []rune(preview); len(runes) > 80 {
		preview = string(runes[:77]) + "..."
	}
	idx := len(b.segments)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			agentID:     payload.AgentID,
			taskPreview: preview,
			startTime:   nanoNow(),
			status:      "active",
			collapsed:   true,
		},
		renderDirty: true,
	})
	b.activeDelegations[payload.AgentID] = idx
}

func (b *contentBuffer) handleDelegationComplete(event output.Event) {
	payload, ok := event.Payload.(output.DelegationCompleteEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	if idx, active := b.activeDelegations[payload.AgentID]; active {
		dd := b.segments[idx].delegData
		if dd != nil {
			dd.status = "complete"
			dd.resultStatus = payload.Status
			dd.turnCount = payload.TurnCount
			dd.tokenCount = payload.TokenCount
			dd.elapsed = formatElapsed(dd.startTime, nanoNow())
			dd.output = payload.Output
		}
		b.segments[idx].renderDirty = true
		delete(b.activeDelegations, payload.AgentID)
		return
	}
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			agentID:      payload.AgentID,
			status:       "complete",
			resultStatus: payload.Status,
			turnCount:    payload.TurnCount,
			tokenCount:   payload.TokenCount,
			output:       payload.Output,
			collapsed:    true,
		},
		renderDirty: true,
	})
}

func (b *contentBuffer) handleDelegationFailed(event output.Event) {
	payload, ok := event.Payload.(output.DelegationFailedEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	if idx, active := b.activeDelegations[payload.AgentID]; active {
		if dd := b.segments[idx].delegData; dd != nil {
			dd.status = "failed"
			dd.elapsed = formatElapsed(dd.startTime, nanoNow())
		}
		b.segments[idx].renderDirty = true
		delete(b.activeDelegations, payload.AgentID)
		return
	}
	b.segments = append(b.segments, contentSegment{
		kind:        segmentDelegation,
		delegData:   &delegationDisplayState{agentID: payload.AgentID, status: "failed", collapsed: true},
		renderDirty: true,
	})
}

func (b *contentBuffer) HasActiveDelegations() bool {
	return len(b.activeDelegations) > 0
}

func (b *contentBuffer) AdvanceDelegationSpinners() {
	for _, idx := range b.activeDelegations {
		if idx < len(b.segments) {
			if dd := b.segments[idx].delegData; dd != nil {
				dd.spinnerFrame = (dd.spinnerFrame + 1) % len(spinnerFrames)
			}
			b.segments[idx].renderDirty = true
		}
	}
}

func (b *contentBuffer) ToggleLastDelegationOutput() {
	for i := len(b.segments) - 1; i >= 0; i-- {
		if b.segments[i].kind == segmentDelegation && b.segments[i].delegData != nil {
			b.segments[i].delegData.collapsed = !b.segments[i].delegData.collapsed
			b.segments[i].renderDirty = true
			return
		}
	}
}

var timeNow = time.Now

var nanoNow = func() int64 {
	return timeNow().UnixNano()
}

func formatElapsed(startNano, endNano int64) string {
	ns := endNano - startNano
	if ns < 0 {
		ns = 0
	}
	ms := ns / 1_000_000
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%ds", s/60, s%60)
}
