package tui

import (
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/usagestats"
)

const (
	delegationTranscriptLimit     = 100
	defaultDelegationExtensionMax = 5
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
	case output.EventTypeDelegationCacheWaiting:
		b.handleDelegationCacheWaiting(event)
	case output.EventTypeDelegationComplete:
		b.handleDelegationComplete(event)
	case output.EventTypeDelegationFailed:
		b.handleDelegationFailed(event)
	case output.EventTypeDelegationExtension:
		b.handleDelegationExtension(event)
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

func (b *contentBuffer) appendScopedDelegationEvent(event output.Event) bool {
	agentID := event.Scope.AgentID
	if agentID == "" {
		return false
	}
	if loc, active := b.activeDelegations[agentID]; active {
		return b.handleScopedDelegationEventAt(loc, event)
	}
	if loc, found := b.findDelegation(agentID); found {
		return b.handleScopedDelegationEventAt(loc, event)
	}
	return false
}

func (b *contentBuffer) handleScopedDelegationEventAt(loc delegationLocator, event output.Event) bool {
	if loc.dd == nil {
		return false
	}
	if loc.dd.cacheWaiting {
		loc.dd.cacheWaiting = false
		b.markDelegationDirty(loc.seg)
	}
	handled := b.applyScopedDelegationEvent(loc.dd, event)
	if handled {
		b.markDelegationDirty(loc.seg)
	}
	return handled
}

func (b *contentBuffer) applyScopedDelegationEvent(dd *delegationDisplayState, event output.Event) bool {
	switch event.Type {
	case output.EventTypeAssistantChunk:
		return b.applyDelegationAssistantChunk(dd, event)
	case output.EventTypeThinkingChunk:
		return b.applyDelegationThinkingChunk(dd, event)
	case output.EventTypeAssistantMessage:
		return b.applyDelegationAssistantMessage(dd, event)
	case output.EventTypeToolCallStarted:
		return b.applyDelegationToolCallStarted(dd, event)
	case output.EventTypeToolCallFinished:
		return b.applyDelegationToolCallFinished(dd, event)
	case output.EventTypeStopReason:
		return b.applyDelegationStopReason(dd, event)
	case output.EventTypeModelCallStarted:
		return b.applyDelegationModelCallStarted(dd, event)
	case output.EventTypeContextDiagnostics:
		return b.applyDelegationContextDiagnostics(dd, event)
	case output.EventTypeModelCallFinished:
		if payload, ok := event.Payload.(output.ModelCallFinishedEvent); ok {
			dd.outputTPS = payload.OutputTPS
		}
		return true
	case output.EventTypeAPIResponse:
		return true
	case output.EventTypeAPIRequest:
		return b.applyDelegationAPIRequest(dd, event)
	default:
		return false
	}
}

func (b *contentBuffer) applyDelegationModelCallStarted(dd *delegationDisplayState, event output.Event) bool {
	payload, ok := event.Payload.(output.ModelCallStartedEvent)
	if !ok {
		return false
	}
	if strings.TrimSpace(payload.Model) != "" && dd.modelName == "" {
		dd.modelName, dd.reasoning = b.resolveDelegationModel(payload.Model)
	}
	return true
}

func (b *contentBuffer) applyDelegationAPIRequest(dd *delegationDisplayState, event output.Event) bool {
	payload, ok := event.Payload.(output.APIRequestEvent)
	if !ok {
		return false
	}
	if strings.TrimSpace(payload.Model) != "" && dd.modelName == "" {
		dd.modelName, dd.reasoning = b.resolveDelegationModel(payload.Model)
	}
	return true
}

func (b *contentBuffer) applyDelegationContextDiagnostics(dd *delegationDisplayState, event output.Event) bool {
	if compaction, ok := output.AsContextCompactionEvent(event.Payload); ok {
		dd.currentOperation = previewDelegationText(delegationCompactionOperation(compaction))
		return true
	}
	payload, ok := output.AsContextBudgetEvent(event.Payload)
	if !ok {
		return false
	}
	displayPromptTokens := payload.RawPromptTokens
	if displayPromptTokens <= 0 {
		displayPromptTokens = payload.PromptTokens
	}
	if displayPromptTokens > 0 {
		dd.promptTokens = displayPromptTokens
	}
	if payload.ContextWindow > 0 {
		dd.contextWindow = payload.ContextWindow
	} else if payload.ContextTokens > 0 {
		dd.contextWindow = payload.ContextTokens
	}
	if displayPromptTokens > 0 && dd.contextWindow > 0 {
		dd.contextFillPct = float64(displayPromptTokens) / float64(dd.contextWindow) * 100
	}
	return true
}

func delegationCompactionOperation(payload output.ContextCompactionEvent) string {
	if payload.Severity == "compacting" {
		return "compacting context"
	}
	if payload.SummaryTitle != "" {
		return payload.SummaryTitle
	}
	return "context compacted"
}

func (b *contentBuffer) applyDelegationThinkingChunk(dd *delegationDisplayState, event output.Event) bool {
	if b.compaction.SuppressThinking() || !b.showThinking {
		return true
	}
	payload, ok := event.Payload.(output.ThinkingChunkEvent)
	if !ok {
		return false
	}
	if payload.Content == "" {
		return true
	}
	entry := dd.appendOrMergeThinkingEntry(payload.Content, payload.Source)
	dd.currentOperation = previewDelegationText(stripThinkingMarkers(entry.body))
	return true
}

func (b *contentBuffer) applyDelegationAssistantChunk(dd *delegationDisplayState, event output.Event) bool {
	if b.compaction.SuppressThinking() {
		return true
	}
	payload, ok := event.Payload.(output.AssistantChunkEvent)
	if !ok {
		return false
	}
	if payload.Content == "" {
		return true
	}
	entry := dd.appendOrMergeAssistantEntry(payload.Content)
	dd.currentOperation = previewDelegationText(entry.body)
	return true
}

func (b *contentBuffer) applyDelegationAssistantMessage(dd *delegationDisplayState, event output.Event) bool {
	if b.compaction.SuppressThinking() {
		return true
	}
	payload, ok := event.Payload.(output.AssistantMessageEvent)
	if !ok {
		return false
	}
	if strings.TrimSpace(payload.Content) == "" {
		return true
	}
	if last := dd.lastEntry(); last != nil &&
		last.kind == delegationTranscriptEntryAssistant &&
		normalizeDelegationText(last.body) == normalizeDelegationText(payload.Content) {
		dd.currentOperation = previewDelegationText(last.body)
		return true
	}
	idx := dd.appendTranscriptEntry(delegationTranscriptEntry{
		kind: delegationTranscriptEntryAssistant,
		body: payload.Content,
	})
	entry := &dd.entries[idx]
	dd.currentOperation = previewDelegationText(entry.body)
	return true
}

func (b *contentBuffer) applyDelegationToolCallStarted(dd *delegationDisplayState, event output.Event) bool {
	payload, ok := event.Payload.(output.ToolCallStartedEvent)
	if !ok {
		return false
	}
	if strings.EqualFold(payload.Tool, "display_file") {
		return true
	}
	entry := delegationTranscriptEntry{
		kind:   delegationTranscriptEntryTool,
		tool:   strings.ToLower(payload.Tool),
		args:   summarizeArgs(payload.Tool, payload.Arguments),
		callID: payload.CallID,
		status: "running",
	}
	idx := dd.appendTranscriptEntry(entry)
	if entry.callID != "" {
		dd.ensureChildToolEntries()
		dd.childToolEntries[entry.callID] = idx
	}
	dd.currentOperation = previewDelegationOperation(entry.tool, entry.args)
	return true
}

func (b *contentBuffer) applyDelegationToolCallFinished(dd *delegationDisplayState, event output.Event) bool {
	payload, ok := event.Payload.(output.ToolCallFinishedEvent)
	if !ok {
		return false
	}
	if strings.EqualFold(payload.Tool, "display_file") {
		return true
	}
	idx, found := dd.findChildToolEntry(payload.CallID)
	if !found {
		return true
	}
	entry := &dd.entries[idx]
	entry.status = "complete"
	entry.body = payload.Result
	entry.hasError = payload.Error != ""
	if entry.hasError {
		entry.status = "error"
	}
	dd.currentOperation = previewDelegationOperation(entry.tool, entry.args)
	return true
}

func (b *contentBuffer) applyDelegationStopReason(dd *delegationDisplayState, event output.Event) bool {
	payload, ok := event.Payload.(output.StopReasonEvent)
	if !ok {
		return false
	}
	if payload.Reason == "cancelled" {
		b.finalizeActiveDelegation(event.Scope.AgentID)
		return true
	}
	if payload.Reason == "complete" || payload.Reason == "max_turns" || payload.Reason == "max_tokens" {
		return true
	}
	status := formatStopReasonEvent(event)
	if strings.TrimSpace(status) == "" {
		return true
	}
	dd.currentOperation = previewDelegationText(status)
	return true
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

// appendAdjacentDelegation merges dd into the preceding delegation segment when
// adjacency allows, returning the segment index it landed in.
func (b *contentBuffer) appendAdjacentDelegation(dd *delegationDisplayState) (int, bool) {
	if dd == nil || dd.isAdvisor {
		return 0, false
	}
	if len(b.segments) == 0 {
		return 0, false
	}
	last := &b.segments[len(b.segments)-1]
	switch last.kind {
	case segmentDelegation:
		if last.delegData == nil || last.delegData.isAdvisor {
			return 0, false
		}
		last.delegGroupData = &delegationGroupSegment{entries: []*delegationDisplayState{last.delegData, dd}}
		last.delegData = nil
		last.kind = segmentDelegationGroup
		last.renderDirty = true
		b.gen++
		return len(b.segments) - 1, true
	case segmentDelegationGroup:
		if last.delegGroupData == nil {
			return 0, false
		}
		last.delegGroupData.entries = append(last.delegGroupData.entries, dd)
		last.renderDirty = true
		b.gen++
		return len(b.segments) - 1, true
	default:
		return 0, false
	}
}

func (b *contentBuffer) appendDelegationSegment(dd *delegationDisplayState) int {
	if idx, merged := b.appendAdjacentDelegation(dd); merged {
		return idx
	}
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
	var baselineTurns, baselineToolCalls, baselineTokens int
	if childAgentID != "" {
		_, childToolLabel = b.findChildDelegationInfo(childAgentID)
		baselineTurns, baselineToolCalls, baselineTokens = b.captureChildBaselineStats(childAgentID)
	}

	summary := summarizeFollowUpArgs(payload.Arguments)
	promptText := extractFollowUpMessage(payload.Arguments)

	dd := &delegationDisplayState{
		toolLabel:             childToolLabel,
		taskPreview:           summary,
		promptText:            promptText,
		promptCollapsed:       true,
		parentCallID:          payload.CallID,
		parentArgs:            summary,
		status:                "active",
		collapsed:             true,
		isFollowUp:            true,
		followUpAgentID:       childAgentID,
		baselineTurnCount:     baselineTurns,
		baselineToolCallCount: baselineToolCalls,
		baselineTokenCount:    baselineTokens,
		extMax:                defaultDelegationExtensionMax,
	}
	idx := b.appendDelegationSegment(dd)
	b.pendingDelegateParents = append(b.pendingDelegateParents, delegationLocator{seg: idx, dd: dd})
}

func (b *contentBuffer) handleParentDelegateToolCallStarted(payload output.ToolCallStartedEvent) {
	if loc, found := b.dequeuePendingDelegationStartByCallID(payload.CallID); found {
		b.bindParentDelegateCall(loc, payload)
		return
	}
	if loc, found := b.dequeuePendingDelegationStartSegment(); found {
		b.bindParentDelegateCall(loc, payload)
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
		extMax:          defaultDelegationExtensionMax,
	}
	if brief != nil {
		dd.applyStructuredBrief(*brief)
	}
	idx := b.appendDelegationSegment(dd)
	b.pendingDelegateParents = append(b.pendingDelegateParents, delegationLocator{seg: idx, dd: dd})
}

func (b *contentBuffer) handleDelegationExtension(event output.Event) {
	payload, ok := event.Payload.(output.DelegationExtensionEvent)
	if !ok {
		return
	}
	if loc, active := b.activeDelegations[payload.AgentID]; active {
		if loc.dd != nil {
			loc.dd.extCurrent = payload.Extension
			loc.dd.extMax = payload.MaxExtensions
			b.markDelegationDirty(loc.seg)
		}
		return
	}
	// Also check completed/failed segments that may no longer be active.
	if loc, found := b.findDelegation(payload.AgentID); found {
		if loc.dd != nil {
			loc.dd.extCurrent = payload.Extension
			loc.dd.extMax = payload.MaxExtensions
			b.markDelegationDirty(loc.seg)
		}
	}
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

func (b *contentBuffer) handleDelegationStarted(event output.Event) {
	payload, ok := event.Payload.(output.DelegationStartedEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	preview := payload.TaskPreview
	modelAlias := strings.TrimSpace(payload.ModelAlias)
	if runes := []rune(preview); len(runes) > 80 {
		preview = string(runes[:77]) + "..."
	}
	bind := func(loc delegationLocator) {
		dd := loc.dd
		dd.agentID = payload.AgentID
		if payload.AgentType != "" {
			dd.agentType = payload.AgentType
		}
		if preview != "" {
			dd.taskPreview = preview
		}
		if modelAlias != "" {
			dd.modelName, dd.reasoning = b.resolveAliasBadge(modelAlias)
		}
		dd.cacheWaiting = false
		dd.startTime = nanoNow()
		dd.status = "active"
		dd.collapsed = true
		dd.extMax = defaultDelegationExtensionMax
		b.activeDelegations[payload.AgentID] = loc
		b.markDelegationDirty(loc.seg)
	}
	if loc, active := b.activeDelegations[payload.AgentID]; active && loc.dd != nil && loc.dd.cacheWaiting && loc.dd.parentCallID == payload.CallID {
		bind(loc)
		return
	}
	if loc, found := b.dequeuePendingDelegateParentByCallID(payload.CallID); found {
		bind(loc)
		return
	}
	if loc, found := b.dequeuePendingDelegateParentByFollowUpAgentID(payload.AgentID); found {
		bind(loc)
		return
	}
	if loc, found := b.dequeuePendingDelegateParentSegment(); found {
		bind(loc)
		return
	}
	dd := &delegationDisplayState{
		agentID:         payload.AgentID,
		agentType:       payload.AgentType,
		taskPreview:     preview,
		promptText:      preview,
		promptCollapsed: true,
		startTime:       nanoNow(),
		status:          "active",
		collapsed:       true,
		extMax:          defaultDelegationExtensionMax,
	}
	if modelAlias != "" {
		dd.modelName, dd.reasoning = b.resolveAliasBadge(modelAlias)
	}
	idx := b.appendDelegationSegment(dd)
	loc := delegationLocator{seg: idx, dd: dd}
	b.activeDelegations[payload.AgentID] = loc
	b.pendingDelegationStarts = append(b.pendingDelegationStarts, loc)
}

func (dd *delegationDisplayState) applyUsage(cacheRead, input, cacheCreate, tokenCount int) {
	dd.cacheReadTokens = cacheRead
	dd.inputTokens = input
	dd.cacheCreateTokens = cacheCreate
	dd.tokenCount = tokenCount
	dd.cacheHitRate, dd.cacheHitOK = usagestats.HitRate(cacheRead, input, cacheCreate)
}

func (b *contentBuffer) handleDelegationComplete(event output.Event) {
	payload, ok := event.Payload.(output.DelegationCompleteEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	if loc, active := b.activeDelegations[payload.AgentID]; active {
		dd := loc.dd
		if dd != nil {
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
			dd.elapsed = formatElapsed(dd.startTime, nanoNow())
			dd.output = payload.Output
			dd.advisorBudget = payload.AdvisorBudget
			dd.advisorUses = payload.AdvisorUses
			dd.advisorDenied = payload.AdvisorDenied
		}
		b.markDelegationDirty(loc.seg)
		delete(b.activeDelegations, payload.AgentID)
		return
	}
	if b.wasCancellationFinalized(payload.AgentID) {
		return
	}
	dd := &delegationDisplayState{
		agentID:       payload.AgentID,
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
	dd.applyUsage(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens, payload.TokenCount)
	b.appendDelegationSegment(dd)
}

func (b *contentBuffer) handleDelegationFailed(event output.Event) {
	payload, ok := event.Payload.(output.DelegationFailedEvent)
	if !ok {
		b.appendStyled(formatDelegationEvent(event), segmentPlain)
		return
	}
	if loc, active := b.activeDelegations[payload.AgentID]; active {
		if dd := loc.dd; dd != nil {
			dd.status = "failed"
			dd.elapsed = formatElapsed(dd.startTime, nanoNow())
			if payload.AdvisorBudget > 0 {
				dd.advisorBudget = payload.AdvisorBudget
				dd.advisorUses = payload.AdvisorUses
				dd.advisorDenied = payload.AdvisorDenied
			}
		}
		b.markDelegationDirty(loc.seg)
		delete(b.activeDelegations, payload.AgentID)
		return
	}
	if b.wasCancellationFinalized(payload.AgentID) {
		return
	}
	dd := &delegationDisplayState{
		agentID:       payload.AgentID,
		status:        "failed",
		collapsed:     true,
		advisorBudget: payload.AdvisorBudget,
		advisorUses:   payload.AdvisorUses,
		advisorDenied: payload.AdvisorDenied,
	}
	b.appendDelegationSegment(dd)
}

func (b *contentBuffer) handleAdvisorStarted(event output.Event) {
	payload, ok := event.Payload.(output.AdvisorStartedEvent)
	if !ok {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
		return
	}

	agentID := event.Scope.AgentID
	if agentID != "" && b.appendScopedAdvisorStarted(agentID, payload) {
		return
	}

	idx := len(b.segments)
	modelName, reasoning := b.resolveDelegationModel(payload.Model)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			isAdvisor:        true,
			toolLabel:        "advisor",
			taskPreview:      "stronger-model steering",
			currentOperation: "consulting stronger-model advisor",
			status:           "active",
			collapsed:        true,
			modelName:        modelName,
			reasoning:        reasoning,
			advisorUse:       payload.UseNumber,
			advisorMaxUses:   payload.MaxUses,
			advisorQuestion:  payload.Question,
			advisorFiles:     payload.Files,
			startTime:        nanoNow(),
		},
		renderDirty: true,
	})
	b.activeAdvisorSegment = idx + 1
}

func (b *contentBuffer) appendScopedAdvisorStarted(agentID string, payload output.AdvisorStartedEvent) bool {
	loc, active := b.activeDelegations[agentID]
	if !active || loc.dd == nil {
		return false
	}
	dd := loc.dd
	dd.advisorBudget = payload.MaxUses
	dd.advisorUses = payload.UseNumber
	dd.advisorQuestion = payload.Question
	dd.advisorFiles = payload.Files
	b.markDelegationDirty(loc.seg)
	return true
}

func (b *contentBuffer) handleAdvisorComplete(event output.Event) {
	payload, ok := event.Payload.(output.AdvisorCompleteEvent)
	if !ok {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
		return
	}

	agentID := event.Scope.AgentID
	if agentID != "" && b.appendScopedAdvisorComplete(agentID, payload) {
		return
	}

	idx := b.activeAdvisorSegment - 1
	if idx < 0 || idx >= len(b.segments) || b.segments[idx].kind != segmentDelegation || b.segments[idx].delegData == nil || !b.segments[idx].delegData.isAdvisor {
		idx = len(b.segments)
		b.segments = append(b.segments, contentSegment{
			kind:        segmentDelegation,
			delegData:   &delegationDisplayState{isAdvisor: true, toolLabel: "advisor", collapsed: true},
			renderDirty: true,
		})
	}
	dd := b.segments[idx].delegData
	dd.modelName, dd.reasoning = b.resolveDelegationModel(payload.Model)
	dd.advisorUse = payload.UseNumber
	dd.advisorMaxUses = payload.MaxUses
	dd.elapsed = formatElapsed(dd.startTime, nanoNow())
	dd.output = payload.Note
	dd.status = "complete"
	dd.resultStatus = "complete"
	dd.applyUsage(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens, payload.TokenCount)
	if strings.TrimSpace(payload.Error) != "" {
		dd.output = payload.Error
		dd.status = "failed"
		dd.resultStatus = "failed"
	}
	b.segments[idx].renderDirty = true
	b.gen++
	b.activeAdvisorSegment = 0

	// Append labeled block with advisor note outside the box.
	if body := strings.TrimSpace(dd.output); body != "" {
		b.appendLabeledBlock("Advisor output", body)
	}

	// Trailing blank margin after the closing separator.
	b.segments = append(b.segments, contentSegment{
		kind:        segmentPlain,
		text:        " ",
		renderDirty: true,
	})
}

func (b *contentBuffer) appendScopedAdvisorComplete(agentID string, payload output.AdvisorCompleteEvent) bool {
	loc, active := b.activeDelegations[agentID]
	if !active || loc.dd == nil {
		return false
	}
	dd := loc.dd
	dd.advisorUses = payload.UseNumber
	dd.advisorBudget = payload.MaxUses
	b.markDelegationDirty(loc.seg)
	return true
}

func (b *contentBuffer) handleAdvisorBudgetExhausted(event output.Event) {
	payload, ok := event.Payload.(output.AdvisorBudgetExhaustedEvent)
	if !ok {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
		return
	}

	agentID := event.Scope.AgentID
	if agentID != "" && b.appendScopedAdvisorBudgetExhausted(agentID, payload) {
		return
	}

	modelName, reasoning := b.resolveDelegationModel(payload.Model)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			isAdvisor:       true,
			toolLabel:       "advisor",
			taskPreview:     "stronger-model steering",
			status:          "budget_exhausted",
			resultStatus:    "budget exhausted",
			output:          payload.Message,
			collapsed:       true,
			modelName:       modelName,
			reasoning:       reasoning,
			advisorUse:      payload.Used,
			advisorMaxUses:  payload.MaxUses,
			advisorQuestion: payload.Question,
			advisorFiles:    payload.Files,
		},
		renderDirty: true,
	})
}

func (b *contentBuffer) appendScopedAdvisorBudgetExhausted(agentID string, payload output.AdvisorBudgetExhaustedEvent) bool {
	loc, active := b.activeDelegations[agentID]
	if !active || loc.dd == nil {
		return false
	}
	dd := loc.dd
	dd.advisorBudget = payload.MaxUses
	dd.advisorUses = payload.Used
	dd.advisorDenied++
	b.markDelegationDirty(loc.seg)
	return true
}

func (b *contentBuffer) handleAdvisorThinkingChunk(event output.Event) {
	idx := b.activeAdvisorSegment - 1
	if idx < 0 || idx >= len(b.segments) || b.segments[idx].kind != segmentDelegation || b.segments[idx].delegData == nil || !b.segments[idx].delegData.isAdvisor {
		return
	}
	dd := b.segments[idx].delegData
	if b.applyDelegationThinkingChunk(dd, event) {
		b.segments[idx].renderDirty = true
		b.gen++
	}
}

// delegateActiveRow is a TUI-local snapshot of one active delegate for the
// cancellation selector. Ordering is defined by the transcript, not by map
// iteration.
type delegateActiveRow struct {
	agentID     string
	agentType   string // lifecycle agent type; tool-label fallback for legacy events
	taskPreview string
	isCode      bool
}

// ActiveDelegateRows returns active, identified delegates in transcript order.
func (b *contentBuffer) ActiveDelegateRows() []delegateActiveRow {
	rows := make([]delegateActiveRow, 0)
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
// DelegationCompleteEvent payload values. Token counts remain whole-life totals.
// Returns zeroes when the segment is not found or has no data.
//
// Cache token counts are deliberately excluded: they are cumulative across
// follow-ups by construction (accumulated in internal/delegation and carried
// in the payload), so subtracting a baseline would be wrong — the payload
// values are rendered verbatim.
func (b *contentBuffer) captureChildBaselineStats(agentID string) (turns, toolCalls, tokens int) {
	if agentID == "" {
		return 0, 0, 0
	}
	if loc, ok := b.findDelegation(agentID); ok {
		return loc.dd.turnCount, loc.dd.toolCallCount, loc.dd.tokenCount
	}
	return 0, 0, 0
}
