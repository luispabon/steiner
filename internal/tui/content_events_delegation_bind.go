package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) dequeuePendingByCallID(list *[]delegationLocator, callID string) (delegationLocator, bool) {
	if callID == "" {
		return delegationLocator{}, false
	}
	for i, loc := range *list {
		if loc.dd == nil || loc.seg < 0 || loc.seg >= len(b.segments) || loc.dd.parentCallID != callID {
			continue
		}
		*list = append((*list)[:i], (*list)[i+1:]...)
		return loc, true
	}
	return delegationLocator{}, false
}

func (b *contentBuffer) dequeuePendingDelegateParentByCallID(callID string) (delegationLocator, bool) {
	return b.dequeuePendingByCallID(&b.pendingDelegateParents, callID)
}

func (b *contentBuffer) drainPending(list *[]delegationLocator, eligible func(delegationLocator) bool) (delegationLocator, bool) {
	for len(*list) > 0 {
		loc := (*list)[0]
		*list = (*list)[1:]
		if loc.dd == nil {
			continue
		}
		if loc.seg < 0 || loc.seg >= len(b.segments) {
			continue
		}
		if !eligible(loc) {
			continue
		}
		return loc, true
	}
	return delegationLocator{}, false
}

func (b *contentBuffer) dequeuePendingDelegateParentSegment() (delegationLocator, bool) {
	return b.drainPending(&b.pendingDelegateParents, func(loc delegationLocator) bool {
		return loc.dd.agentID == ""
	})
}

// dequeuePendingDelegateParentByFollowUpAgentID matches a pending follow-up
// box against the DelegationStartedEvent's AgentID, so a follow-up call whose
// CallID lookup misses (e.g. a stale ParentCallID) still binds to the correct
// box instead of falling through to blind FIFO ordering, which can attach
// this event to an unrelated agent's pending box when several follow-ups or
// delegations are in flight together. Unlike drainPending, non-matching
// entries are left in place rather than discarded.
func (b *contentBuffer) dequeuePendingDelegateParentByFollowUpAgentID(agentID string) (delegationLocator, bool) {
	if agentID == "" {
		return delegationLocator{}, false
	}
	list := &b.pendingDelegateParents
	for i, loc := range *list {
		if loc.dd == nil || loc.seg < 0 || loc.seg >= len(b.segments) {
			continue
		}
		if loc.dd.agentID != "" || !loc.dd.isFollowUp || loc.dd.followUpAgentID != agentID {
			continue
		}
		*list = append((*list)[:i], (*list)[i+1:]...)
		return loc, true
	}
	return delegationLocator{}, false
}

func (b *contentBuffer) removeFromPendingDelegateParents(dd *delegationDisplayState) {
	for i, loc := range b.pendingDelegateParents {
		if loc.dd == dd {
			b.pendingDelegateParents = append(b.pendingDelegateParents[:i], b.pendingDelegateParents[i+1:]...)
			return
		}
	}
}

func (b *contentBuffer) registerDelegationOccurrence(loc delegationLocator) {
	if loc.dd == nil || loc.dd.parentCallID == "" {
		return
	}
	b.pendingDelegationOccurrences = append(b.pendingDelegationOccurrences, loc)
}

func (b *contentBuffer) appendDelegationSegment(dd *delegationDisplayState) int {
	b.segments = append(b.segments, contentSegment{kind: segmentDelegation, delegData: dd, renderDirty: true})
	return len(b.segments) - 1
}

func (b *contentBuffer) dequeuePendingDelegationStartByCallID(callID string) (delegationLocator, bool) {
	return b.dequeuePendingByCallID(&b.pendingDelegationStarts, callID)
}

func (b *contentBuffer) dequeuePendingDelegationStartSegment() (delegationLocator, bool) {
	return b.drainPending(&b.pendingDelegationStarts, func(loc delegationLocator) bool {
		return loc.dd.parentCallID == ""
	})
}

func delegateCallDetails(tool string, args map[string]any) (label, prompt string, brief *structuredDelegateBrief) {
	if !isSpecializedDelegateTool(tool) {
		return "", delegateArgText(args), nil
	}

	if typeArg, ok := args["type"].(string); ok {
		label = strings.ToLower(strings.TrimSpace(typeArg))
	}
	if parsed, ok := parseStructuredDelegateBrief(args); ok {
		brief = &parsed
		return label, parsed.objective, brief
	}
	return label, delegateArgText(args), nil
}

func (b *contentBuffer) bindParentDelegateCall(loc delegationLocator, payload output.ToolCallStartedEvent) {
	if loc.dd == nil {
		return
	}
	dd := loc.dd
	dd.parentCallID = payload.CallID
	dd.parentArgs = summarizeArgs(payload.Tool, payload.Arguments)
	toolLabel, promptText, brief := delegateCallDetails(payload.Tool, payload.Arguments)
	if brief != nil {
		dd.applyStructuredBrief(*brief)
	} else {
		dd.promptText = promptText
	}
	if dd.taskPreview == "" {
		dd.taskPreview = dd.parentArgs
	}
	if dd.toolLabel == "" {
		dd.toolLabel = toolLabel
	}
	b.markDelegationDirty(loc.seg)
}

func (b *contentBuffer) handleFollowUpToolCallStarted(payload output.ToolCallStartedEvent) {
	childAgentID := extractFollowUpAgentID(payload.Arguments)
	childToolLabel := ""
	var baselineTurns, baselineToolCalls int
	if childAgentID != "" {
		_, childToolLabel = b.findChildDelegationInfo(childAgentID)
		baselineTurns, baselineToolCalls = b.captureChildBaselineStats(childAgentID)
	}

	summary := summarizeFollowUpArgs(payload.Arguments)
	promptText := extractFollowUpMessage(payload.Arguments)

	loc, queued := b.takeQueuedDelegation(payload.CallID)
	if !queued {
		loc.dd = &delegationDisplayState{
			toolLabel:       childToolLabel,
			taskPreview:     summary,
			promptText:      promptText,
			promptCollapsed: true,
			parentCallID:    payload.CallID,
			parentArgs:      summary,
			status:          "active",
			collapsed:       true,
			isFollowUp:      true,
			followUpAgentID: childAgentID,
		}
		loc.seg = b.appendDelegationSegment(loc.dd)
	}
	dd := loc.dd
	dd.queuedForSlot = false
	dd.parentCallID = payload.CallID
	dd.parentArgs = summary
	dd.taskPreview = summary
	dd.promptText = promptText
	dd.status = "active"
	dd.startTime = nanoNow()
	dd.isFollowUp = true
	dd.followUpAgentID = childAgentID
	dd.baselineTurnCount = baselineTurns
	dd.baselineToolCallCount = baselineToolCalls
	dd.toolLabel = childToolLabel
	b.markDelegationDirty(loc.seg)
	b.pendingDelegateParents = append(b.pendingDelegateParents, loc)
	b.registerDelegationOccurrence(loc)
}

func (b *contentBuffer) handleParentDelegateToolCallStarted(payload output.ToolCallStartedEvent) {
	if loc, found := b.takeQueuedDelegation(payload.CallID); found {
		loc.dd.queuedForSlot = false
		loc.dd.status = "active"
		loc.dd.collapsed = true
		loc.dd.startTime = nanoNow()
		b.bindParentDelegateCall(loc, payload)
		b.pendingDelegateParents = append(b.pendingDelegateParents, loc)
		b.registerDelegationOccurrence(loc)
		return
	}
	if loc, found := b.dequeuePendingDelegationStartByCallID(payload.CallID); found {
		b.bindParentDelegateCall(loc, payload)
		b.registerDelegationOccurrence(loc)
		return
	}
	if loc, found := b.dequeuePendingDelegationStartSegment(); found {
		b.bindParentDelegateCall(loc, payload)
		b.registerDelegationOccurrence(loc)
		return
	}

	summary := summarizeArgs(payload.Tool, payload.Arguments)
	toolLabel, promptText, brief := delegateCallDetails(payload.Tool, payload.Arguments)
	dd := &delegationDisplayState{
		toolLabel:       toolLabel,
		taskPreview:     summary,
		promptText:      promptText,
		promptCollapsed: true,
		parentCallID:    payload.CallID,
		parentArgs:      summary,
		status:          "active",
		collapsed:       true,
	}
	if brief != nil {
		dd.applyStructuredBrief(*brief)
	}
	idx := b.appendDelegationSegment(dd)
	loc := delegationLocator{seg: idx, dd: dd}
	b.pendingDelegateParents = append(b.pendingDelegateParents, loc)
	b.registerDelegationOccurrence(loc)
}

func (b *contentBuffer) handleDelegationCacheWaiting(event output.Event) {
	payload, ok := event.Payload.(output.DelegationCacheWaitingEvent)
	if !ok {
		return
	}
	loc, found := b.dequeuePendingDelegateParentByCallID(payload.CallID)
	if !found {
		if loc, active := b.activeDelegations[payload.AgentID]; active && loc.dd != nil {
			loc.dd.cacheWaiting = true
			loc.dd.cacheWaitDeadline = payload.DeadlineUnixNano
			b.markDelegationDirty(loc.seg)
		}
		return
	}
	loc.dd.agentID = payload.AgentID
	loc.dd.cacheWaiting = true
	loc.dd.cacheWaitDeadline = payload.DeadlineUnixNano
	b.activeDelegations[payload.AgentID] = loc
	b.markDelegationDirty(loc.seg)
}

// appendToolCallQueuedEvent records a delegation call announced as waiting for a
// parallelism slot. The box is created here so it is visible before the call is
// dispatched, then activated by the matching ToolCallStartedEvent.
func (b *contentBuffer) appendToolCallQueuedEvent(event output.Event) {
	b.finishStreaming()
	payload, ok := event.Payload.(output.ToolCallQueuedEvent)
	if !ok || !isDelegateOrSpecialized(payload.Tool) || payload.CallID == "" {
		// Without a call ID the box could not be bound or cleaned up later, so
		// leave it to the normal ToolCallStarted path instead of risking stale
		// queued state.
		return
	}
	if b.queuedDelegations == nil {
		b.queuedDelegations = make(map[string]delegationLocator)
	}
	summary := summarizeArgs(payload.Tool, payload.Arguments)
	toolLabel, promptText, brief := delegateCallDetails(payload.Tool, payload.Arguments)
	dd := &delegationDisplayState{
		toolLabel:       toolLabel,
		taskPreview:     summary,
		promptText:      promptText,
		promptCollapsed: true,
		parentCallID:    payload.CallID,
		parentArgs:      summary,
		queuedForSlot:   true,
		status:          "active",
		collapsed:       true,
	}
	if brief != nil {
		dd.applyStructuredBrief(*brief)
	}
	idx := b.appendDelegationSegment(dd)
	b.queuedDelegations[payload.CallID] = delegationLocator{seg: idx, dd: dd}
}

// clearQueuedDelegation drops any queued-delegation entry for a call ID so a
// finished event cannot leave stale queued state behind.
func (b *contentBuffer) clearQueuedDelegation(callID string) {
	if b.queuedDelegations == nil || callID == "" {
		return
	}
	delete(b.queuedDelegations, callID)
}

// takeQueuedDelegation pops the queued delegation box for a call ID so the
// matching start event activates it instead of creating a duplicate.
func (b *contentBuffer) takeQueuedDelegation(callID string) (delegationLocator, bool) {
	if callID == "" || b.queuedDelegations == nil {
		return delegationLocator{}, false
	}
	loc, found := b.queuedDelegations[callID]
	if !found {
		return delegationLocator{}, false
	}
	delete(b.queuedDelegations, callID)
	if loc.dd == nil {
		return delegationLocator{}, false
	}
	return loc, true
}

func (b *contentBuffer) handleDelegationStarted(event output.Event) {
	payload, ok := event.Payload.(output.DelegationStartedEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	b.bindDelegation(payload.AgentID, payload.CallID, payload.AgentType, payload.TaskPreview, payload.ModelAlias, false)
}

// handleDelegationQueued binds a queued sub-agent to a delegation segment
// through the same path as a started one; DelegationStarted later activates it.
func (b *contentBuffer) handleDelegationQueued(event output.Event) {
	payload, ok := event.Payload.(output.DelegationQueuedEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	b.bindDelegation(payload.AgentID, payload.CallID, payload.AgentType, payload.TaskPreview, "", true)
}

// findDelegationToBind locates the existing segment a queued or started event
// belongs to: the queued or cache-waiting box for that agent, else a pending
// parent delegate call.
func (b *contentBuffer) findDelegationToBind(agentID, callID string, queued bool) (delegationLocator, bool) {
	if loc, active := b.activeDelegations[agentID]; active && loc.dd != nil {
		if !queued && loc.dd.queuedForSlot {
			return loc, true
		}
		if loc.dd.cacheWaiting && loc.dd.parentCallID == callID {
			return loc, true
		}
	}
	if loc, found := b.dequeuePendingDelegateParentByCallID(callID); found {
		return loc, true
	}
	if loc, found := b.dequeuePendingDelegateParentByFollowUpAgentID(agentID); found {
		return loc, true
	}
	return b.dequeuePendingDelegateParentSegment()
}

func (b *contentBuffer) bindDelegation(agentID, callID, agentType, taskPreview, modelAlias string, queued bool) {
	if !queued {
		if loc, active := b.activeDelegations[agentID]; active && loc.dd != nil && loc.dd.status == "active" && loc.dd.parentCallID != "" && loc.dd.parentCallID != callID {
			return
		}
	}
	preview := taskPreview
	modelAlias = strings.TrimSpace(modelAlias)
	if runes := []rune(preview); len(runes) > 80 {
		preview = string(runes[:77]) + "..."
	}
	now := nanoNow()
	b.noteDelegationEvent(agentID)
	bind := func(loc delegationLocator) {
		dd := loc.dd
		dd.agentID = agentID
		if agentType != "" {
			dd.agentType = agentType
		}
		if preview != "" {
			dd.taskPreview = preview
		}
		if modelAlias != "" {
			dd.modelName, dd.reasoning = b.resolveAliasBadge(modelAlias)
		}
		dd.cacheWaiting = false
		dd.queuedForSlot = queued
		if !queued {
			dd.startTime = now
		}
		dd.status = "active"
		dd.collapsed = true
		b.activeDelegations[agentID] = loc
		b.markDelegationDirty(loc.seg)
	}
	if loc, found := b.findDelegationToBind(agentID, callID, queued); found {
		bind(loc)
		return
	}
	dd := &delegationDisplayState{
		agentID:         agentID,
		agentType:       agentType,
		taskPreview:     preview,
		promptText:      preview,
		promptCollapsed: true,
		queuedForSlot:   queued,
		status:          "active",
		collapsed:       true,
	}
	if !queued {
		dd.startTime = now
	}
	if modelAlias != "" {
		dd.modelName, dd.reasoning = b.resolveAliasBadge(modelAlias)
	}
	idx := b.appendDelegationSegment(dd)
	loc := delegationLocator{seg: idx, dd: dd}
	b.activeDelegations[agentID] = loc
	b.pendingDelegationStarts = append(b.pendingDelegationStarts, loc)
}
