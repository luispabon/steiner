package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) appendDelegationSegment(dd *delegationDisplayState) int {
	b.segments = append(b.segments, contentSegment{kind: segmentDelegation, delegData: dd, renderDirty: true})
	return len(b.segments) - 1
}

// takePendingDelegationStart removes the card a lifecycle event created before
// its parent tool call arrived: the card for callID, else the oldest card whose
// events carried no call ID.
func (b *contentBuffer) takePendingDelegationStart(callID string) (delegationLocator, bool) {
	if callID == "" {
		return delegationLocator{}, false
	}
	match := -1
	for i, loc := range b.pendingDelegationStarts {
		if loc.dd == nil {
			continue
		}
		if loc.dd.parentCallID == callID {
			match = i
			break
		}
		if match < 0 && loc.dd.parentCallID == "" {
			match = i
		}
	}
	if match < 0 {
		return delegationLocator{}, false
	}
	loc := b.pendingDelegationStarts[match]
	b.pendingDelegationStarts = append(b.pendingDelegationStarts[:match], b.pendingDelegationStarts[match+1:]...)
	return loc, true
}

// oldestUnboundDelegation returns the earliest parent card, open or admitted,
// that has not bound a child and satisfies eligible.
func (b *contentBuffer) oldestUnboundDelegation(eligible func(*delegationDisplayState) bool) (delegationLocator, bool) {
	var oldest delegationLocator
	found := false
	consider := func(loc delegationLocator) {
		if loc.dd == nil || loc.dd.agentID != "" || !eligible(loc.dd) {
			return
		}
		if !found || loc.seg < oldest.seg {
			oldest, found = loc, true
		}
	}
	for _, loc := range b.openDelegations {
		consider(loc)
	}
	for _, loc := range b.delegations {
		consider(loc)
	}
	return oldest, found
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
	b.openDelegation(loc)
}

func (b *contentBuffer) handleParentDelegateToolCallStarted(payload output.ToolCallStartedEvent) {
	if loc, found := b.takeQueuedDelegation(payload.CallID); found {
		loc.dd.queuedForSlot = false
		loc.dd.status = "active"
		loc.dd.collapsed = true
		loc.dd.startTime = nanoNow()
		b.bindParentDelegateCall(loc, payload)
		b.openDelegation(loc)
		return
	}
	if loc, found := b.takePendingDelegationStart(payload.CallID); found {
		keyed := loc.dd.parentCallID != ""
		b.bindParentDelegateCall(loc, payload)
		if !keyed {
			b.openDelegation(loc)
		}
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
	b.openDelegation(delegationLocator{seg: idx, dd: dd})
}

func (b *contentBuffer) handleDelegationCacheWaiting(event output.Event) {
	payload, ok := event.Payload.(output.DelegationCacheWaitingEvent)
	if !ok {
		return
	}
	loc, found := b.lookupOccurrence(keyOf(payload.DelegationOccurrence))
	if !found && payload.CallID == "" {
		loc, found = b.activeDelegations[payload.AgentID]
	}
	if !found || loc.dd == nil {
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
	loc := delegationLocator{seg: b.appendDelegationSegment(dd), dd: dd}
	b.queuedDelegations[payload.CallID] = loc
	b.openDelegation(loc)
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
	b.bindDelegation(payload.DelegationOccurrence, payload.AgentType, payload.TaskPreview, payload.ModelAlias, false)
}

// handleDelegationQueued binds a queued sub-agent to a delegation segment
// through the same path as a started one; DelegationStarted later activates it.
func (b *contentBuffer) handleDelegationQueued(event output.Event) {
	payload, ok := event.Payload.(output.DelegationQueuedEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	b.bindDelegation(payload.DelegationOccurrence, payload.AgentType, payload.TaskPreview, "", true)
}

// findDelegationToBind locates the card a queued or started event belongs to:
// its occurrence's card. Events without a call ID carry no occurrence, so they
// bind by agent: the agent's card still waiting for a slot or the prompt cache,
// else its unbound follow-up card, else the oldest unbound parent card.
func (b *contentBuffer) findDelegationToBind(occ output.DelegationOccurrence, queued bool) (delegationLocator, bool) {
	if occ.CallID != "" {
		return b.lookupOccurrence(keyOf(occ))
	}
	loc, active := b.activeDelegations[occ.AgentID]
	if active && loc.dd != nil && ((!queued && loc.dd.queuedForSlot) || loc.dd.cacheWaiting) {
		return loc, true
	}
	if loc, found := b.oldestUnboundDelegation(func(dd *delegationDisplayState) bool {
		return dd.isFollowUp && dd.followUpAgentID == occ.AgentID
	}); found {
		return loc, true
	}
	return b.oldestUnboundDelegation(func(*delegationDisplayState) bool { return true })
}

// agentRunsOtherCall reports whether the agent's active card belongs to a
// different parent call than occ.
func (b *contentBuffer) agentRunsOtherCall(occ output.DelegationOccurrence) bool {
	loc, active := b.activeDelegations[occ.AgentID]
	return active && loc.dd != nil && loc.dd.status == "active" && loc.dd.parentCallID != "" && loc.dd.parentCallID != occ.CallID
}

func (b *contentBuffer) bindDelegation(occ output.DelegationOccurrence, agentType, taskPreview, modelAlias string, queued bool) {
	agentID := occ.AgentID
	target, found := b.findDelegationToBind(occ, queued)
	if !found && !queued && b.agentRunsOtherCall(occ) {
		return
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
	if found {
		b.closeOpenDelegation(target.dd.parentCallID, target.dd)
		bind(target)
		return
	}
	dd := &delegationDisplayState{
		agentID:         agentID,
		agentType:       agentType,
		parentCallID:    occ.CallID,
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
	b.registerOccurrence(keyOf(occ), loc)
	b.pendingDelegationStarts = append(b.pendingDelegationStarts, loc)
}
