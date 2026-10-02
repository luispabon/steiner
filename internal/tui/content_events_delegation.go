package tui

import (
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/usagestats"
)

const (
	delegationTranscriptLimit = 100
)

// forEachDelegationReverse walks every delegation newest-first across both
// segment kinds, stopping when fn returns true.
func (b *contentBuffer) forEachDelegationReverse(fn func(loc delegationLocator) bool) {
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := &b.segments[i]
		if seg.kind == segmentDelegation && seg.delegData != nil {
			if fn(delegationLocator{seg: i, dd: seg.delegData}) {
				return
			}
		} else if seg.kind == segmentDelegationGroup && seg.delegGroupData != nil {
			for j := len(seg.delegGroupData.entries) - 1; j >= 0; j-- {
				if fn(delegationLocator{seg: i, dd: seg.delegGroupData.entries[j]}) {
					return
				}
			}
		}
	}
}

func (b *contentBuffer) appendDelegationEvent(event output.Event) {
	b.finishStreaming()
	if b.activeDelegations == nil {
		b.activeDelegations = make(map[string]delegationLocator)
	}
	switch event.Type {
	case output.EventTypeDelegationStarted:
		b.handleDelegationStarted(event)
	case output.EventTypeDelegationQueued:
		b.handleDelegationQueued(event)
	case output.EventTypeDelegationCacheWaiting:
		b.handleDelegationCacheWaiting(event)
	case output.EventTypeDelegationComplete:
		b.handleDelegationComplete(event)
	case output.EventTypeDelegationFailed:
		b.handleDelegationFailed(event)
	default:
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
	}
}

func (b *contentBuffer) appendAdvisorEvent(event output.Event) {
	b.finishStreaming()
	switch event.Type {
	case output.EventTypeAdvisorStarted:
		b.handleAdvisorStarted(event)
	case output.EventTypeAdvisorComplete:
		b.handleAdvisorComplete(event)
	case output.EventTypeAdvisorBudgetExhausted:
		b.handleAdvisorBudgetExhausted(event)
	case output.EventTypeThinkingChunk:
		b.handleAdvisorThinkingChunk(event)
	default:
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
	}
}

func (b *contentBuffer) findDelegation(agentID string) (delegationLocator, bool) {
	var result delegationLocator
	var found bool
	b.forEachDelegationReverse(func(loc delegationLocator) bool {
		if loc.dd.agentID == agentID {
			result = loc
			found = true
			return true
		}
		return false
	})
	return result, found
}

func (b *contentBuffer) wasCancellationFinalized(agentID string) bool {
	loc, found := b.findDelegation(agentID)
	return found && loc.dd != nil && loc.dd.finalizedByCancellation
}

// finalizeActiveDelegation freezes one in-flight delegation as failed. The
// cancellation marker makes a late terminal event for this display a no-op.
func (b *contentBuffer) finalizeActiveDelegation(agentID string) {
	loc, active := b.activeDelegations[agentID]
	if !active {
		return
	}
	delete(b.activeDelegations, agentID)
	if loc.dd == nil {
		return
	}
	loc.dd.status = "failed"
	loc.dd.finalizedByCancellation = true
	if loc.dd.elapsed == "" && loc.dd.startTime > 0 {
		loc.dd.elapsed = formatElapsed(loc.dd.startTime, nanoNow())
	}
	b.markDelegationDirty(loc.seg)
}

func (b *contentBuffer) markDelegationDirty(idx int) {
	if idx < 0 || idx >= len(b.segments) {
		return
	}
	switch b.segments[idx].kind {
	case segmentDelegation:
		if b.segments[idx].delegData == nil {
			return
		}
	case segmentDelegationGroup:
		if b.segments[idx].delegGroupData == nil {
			return
		}
	default:
		return
	}
	b.segments[idx].renderDirty = true
	b.gen++
}

func (dd *delegationDisplayState) applyUsage(cacheRead, input, cacheCreate, tokenCount int) {
	dd.cacheReadTokens = cacheRead
	dd.inputTokens = input
	dd.cacheCreateTokens = cacheCreate
	dd.tokenCount = tokenCount
	dd.cacheHitRate, dd.cacheHitOK = usagestats.HitRate(cacheRead, input, cacheCreate)
}

func (b *contentBuffer) settleDelegation(loc delegationLocator, agentID string) {
	b.markDelegationDirty(loc.seg)
	if active, ok := b.activeDelegations[agentID]; ok && active.dd == loc.dd {
		delete(b.activeDelegations, agentID)
	}
}

func (b *contentBuffer) handleDelegationComplete(event output.Event) {
	payload, ok := event.Payload.(output.DelegationCompleteEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	if payload.CallID == "" {
		// Every emitter stamps a full occurrence; without one there is no card
		// to settle.
		return
	}
	if loc, found := b.lookupOccurrence(keyOf(payload.DelegationOccurrence)); found {
		if dd := loc.dd; !dd.finalizedByCancellation {
			dd.status = "complete"
			dd.resultStatus = payload.Status
			if dd.isFollowUp {
				dd.turnCount = max(0, payload.TurnCount-dd.baselineTurnCount)
				dd.toolCallCount = max(0, payload.ToolCallCount-dd.baselineToolCallCount)
				// All token counters are whole-life totals for follow-ups, so
				// they are rendered verbatim with no baseline subtraction.
				dd.applyUsage(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens, payload.TokenCount)
			} else {
				dd.turnCount = payload.TurnCount
				dd.toolCallCount = payload.ToolCallCount
				dd.applyUsage(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens, payload.TokenCount)
			}
			if dd.startTime > 0 {
				dd.elapsed = formatElapsed(dd.startTime, nanoNow())
			}
			dd.fillFromTerminalEvent(payload.AgentType, payload.DurationMs)
			dd.output = payload.Output
			dd.advisorBudget = payload.AdvisorBudget
			dd.advisorUses = payload.AdvisorUses
			dd.advisorDenied = payload.AdvisorDenied
		}
		b.settleDelegation(loc, payload.AgentID)
		return
	}
	if b.wasCancellationFinalized(payload.AgentID) {
		return
	}
	dd := &delegationDisplayState{
		agentID:       payload.AgentID,
		parentCallID:  payload.CallID,
		status:        "complete",
		resultStatus:  payload.Status,
		turnCount:     payload.TurnCount,
		toolCallCount: payload.ToolCallCount,
		output:        payload.Output,
		collapsed:     true,
		advisorBudget: payload.AdvisorBudget,
		advisorUses:   payload.AdvisorUses,
		advisorDenied: payload.AdvisorDenied,
	}
	dd.fillFromTerminalEvent(payload.AgentType, payload.DurationMs)
	dd.applyUsage(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens, payload.TokenCount)
	b.registerOccurrence(keyOf(payload.DelegationOccurrence), delegationLocator{seg: b.appendDelegationSegment(dd), dd: dd})
}

func (b *contentBuffer) handleDelegationFailed(event output.Event) {
	payload, ok := event.Payload.(output.DelegationFailedEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	if payload.CallID == "" {
		// Every emitter stamps a full occurrence; without one there is no card
		// to settle.
		return
	}
	if loc, found := b.lookupOccurrence(keyOf(payload.DelegationOccurrence)); found {
		if dd := loc.dd; !dd.finalizedByCancellation {
			if dd.agentID == "" {
				dd.agentID = payload.AgentID
				dd.agentType = event.Scope.AgentType
			}
			dd.status = "failed"
			dd.failureReason = payload.Error
			if dd.startTime > 0 {
				dd.elapsed = formatElapsed(dd.startTime, nanoNow())
			}
			dd.fillFromTerminalEvent(payload.AgentType, payload.DurationMs)
			if payload.AdvisorBudget > 0 {
				dd.advisorBudget = payload.AdvisorBudget
				dd.advisorUses = payload.AdvisorUses
				dd.advisorDenied = payload.AdvisorDenied
			}
		}
		b.settleDelegation(loc, payload.AgentID)
		return
	}
	if b.wasCancellationFinalized(payload.AgentID) {
		return
	}
	dd := &delegationDisplayState{
		agentID:       payload.AgentID,
		parentCallID:  payload.CallID,
		status:        "failed",
		failureReason: payload.Error,
		collapsed:     true,
		advisorBudget: payload.AdvisorBudget,
		advisorUses:   payload.AdvisorUses,
		advisorDenied: payload.AdvisorDenied,
	}
	dd.fillFromTerminalEvent(payload.AgentType, payload.DurationMs)
	loc := delegationLocator{seg: b.appendDelegationSegment(dd), dd: dd}
	b.registerOccurrence(keyOf(payload.DelegationOccurrence), loc)
	b.pendingDelegationStarts = append(b.pendingDelegationStarts, loc)
}

// delegateActiveRow is a TUI-local snapshot of one active delegate for the
// cancellation selector. Ordering is defined by the transcript, not by map
// iteration.
type delegateActiveRow struct {
	agentID     string
	agentType   string // lifecycle agent type; tool-label fallback for legacy events
	taskPreview string
	isCode      bool
	queued      bool
	stalledMin  int
}

// ActiveDelegateRows returns active, identified delegates in transcript order.
func (b *contentBuffer) ActiveDelegateRows() []delegateActiveRow {
	rows := make([]delegateActiveRow, 0)
	now := timeNow()
	appendRow := func(dd *delegationDisplayState) {
		if dd == nil || dd.isAdvisor || dd.agentID == "" || dd.status != "active" {
			return
		}
		agentType := dd.agentType
		if agentType == "" {
			agentType = dd.toolLabel
		}
		rows = append(rows, delegateActiveRow{
			agentID:     dd.agentID,
			agentType:   agentType,
			taskPreview: dd.taskPreview,
			isCode:      agentType == "code",
			queued:      dd.queuedForSlot,
			stalledMin:  b.stalledMinutes(dd, now),
		})
	}
	for _, seg := range b.segments {
		switch seg.kind {
		case segmentDelegation:
			appendRow(seg.delegData)
		case segmentDelegationGroup:
			if seg.delegGroupData == nil {
				continue
			}
			for _, dd := range seg.delegGroupData.entries {
				appendRow(dd)
			}
		}
	}
	return rows
}

func (b *contentBuffer) HasActiveDelegations() bool {
	return len(b.activeDelegations) > 0
}

func (b *contentBuffer) AdvanceDelegationSpinners() {
	for _, loc := range b.activeDelegations {
		if loc.seg < len(b.segments) {
			if dd := loc.dd; dd != nil {
				dd.spinnerFrame = (dd.spinnerFrame + 1) % len(spinnerFrames)
			}
			b.segments[loc.seg].renderDirty = true
			b.gen++
		}
	}
}

func (b *contentBuffer) ToggleLastDelegationOutput() {
	b.forEachDelegationReverse(func(loc delegationLocator) bool {
		// For groups, toggle the last (most recent) entry.
		if loc.dd != nil {
			loc.dd.collapsed = !loc.dd.collapsed
			b.segments[loc.seg].renderDirty = true
			b.gen++
			return true
		}
		return false
	})
}

var timeNow = time.Now

var nanoNow = func() int64 {
	return timeNow().UnixNano()
}

// findChildDelegationInfo searches for a delegation segment by agentID and returns
// its label and toolLabel. Falls back gracefully if not found.
func (b *contentBuffer) findChildDelegationInfo(agentID string) (label, toolLabel string) {
	if agentID == "" {
		return "", ""
	}
	if loc, ok := b.findDelegation(agentID); ok {
		return agentID, loc.dd.effectiveTypeLabel()
	}
	return agentID, ""
}

// captureChildBaselineStats searches for the most recent delegation segment
// with the given agentID and returns its cumulative turn and tool-call counts.
// These form the baseline that must be subtracted from follow-up
// DelegationCompleteEvent payload values. Returns zeroes when the segment is
// not found or has no data.
func (b *contentBuffer) captureChildBaselineStats(agentID string) (turns, toolCalls int) {
	if agentID == "" {
		return 0, 0
	}
	if loc, ok := b.findDelegation(agentID); ok {
		return loc.dd.turnCount, loc.dd.toolCallCount
	}
	return 0, 0
}

// fillFromTerminalEvent completes display fields a Complete/Failed event can
// supply: the type label when replay or compaction left it blank, and the
// elapsed text when the event carries a recorded duration.
func (dd *delegationDisplayState) fillFromTerminalEvent(agentType string, durationMs int64) {
	if dd.agentType == "" && dd.toolLabel == "" {
		dd.agentType = agentType
	}
	if durationMs > 0 {
		dd.elapsed = formatElapsed(0, durationMs*1_000_000)
	}
}
