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
	if idx, active := b.activeDelegations[agentID]; active {
		return b.handleScopedDelegationEventAtIndex(idx, event)
	}
	if idx, found := b.findDelegationSegment(agentID); found {
		return b.handleScopedDelegationEventAtIndex(idx, event)
	}
	return false
}

func (b *contentBuffer) handleScopedDelegationEventAtIndex(idx int, event output.Event) bool {
	if idx < 0 || idx >= len(b.segments) {
		return false
	}
	seg := &b.segments[idx]
	if seg.kind != segmentDelegation || seg.delegData == nil {
		return false
	}
	handled := b.applyScopedDelegationEvent(seg.delegData, event)
	if handled {
		seg.renderDirty = true
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
	case output.EventTypeModelCallFinished,
		output.EventTypeAPIResponse:
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
	if strings.TrimSpace(payload.Model) != "" {
		dd.modelName = strings.TrimSpace(payload.Model)
	}
	return true
}

func (b *contentBuffer) applyDelegationAPIRequest(dd *delegationDisplayState, event output.Event) bool {
	payload, ok := event.Payload.(output.APIRequestEvent)
	if !ok {
		return false
	}
	if strings.TrimSpace(payload.Model) != "" {
		dd.modelName = strings.TrimSpace(payload.Model)
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
	if payload.ContextUsagePercent > 0 {
		dd.contextFillPct = payload.ContextUsagePercent
	}
	if payload.PromptTokens > 0 {
		dd.promptTokens = payload.PromptTokens
	}
	if payload.ContextWindow > 0 {
		dd.contextWindow = payload.ContextWindow
	} else if payload.ContextTokens > 0 {
		dd.contextWindow = payload.ContextTokens
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
	dd.currentOperation = previewDelegationText(entry.body)
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

func (b *contentBuffer) dequeuePendingDelegateParentSegment() (int, bool) {
	for len(b.pendingDelegateParents) > 0 {
		idx := b.pendingDelegateParents[0]
		b.pendingDelegateParents = b.pendingDelegateParents[1:]
		if idx < 0 || idx >= len(b.segments) {
			continue
		}
		seg := b.segments[idx]
		if seg.kind != segmentDelegation || seg.delegData == nil {
			continue
		}
		if seg.delegData.agentID != "" {
			continue
		}
		return idx, true
	}
	return 0, false
}

func (b *contentBuffer) removeFromPendingDelegateParents(idx int) {
	for i, v := range b.pendingDelegateParents {
		if v == idx {
			b.pendingDelegateParents = append(b.pendingDelegateParents[:i], b.pendingDelegateParents[i+1:]...)
			return
		}
	}
}

func (b *contentBuffer) dequeuePendingDelegationStartSegment() (int, bool) {
	for len(b.pendingDelegationStarts) > 0 {
		idx := b.pendingDelegationStarts[0]
		b.pendingDelegationStarts = b.pendingDelegationStarts[1:]
		if idx < 0 || idx >= len(b.segments) {
			continue
		}
		seg := b.segments[idx]
		if seg.kind != segmentDelegation || seg.delegData == nil {
			continue
		}
		if seg.delegData.parentCallID != "" {
			continue
		}
		return idx, true
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

func (b *contentBuffer) bindParentDelegateCall(idx int, payload output.ToolCallStartedEvent) bool {
	if idx < 0 || idx >= len(b.segments) {
		return false
	}
	seg := &b.segments[idx]
	if seg.kind != segmentDelegation || seg.delegData == nil {
		return false
	}
	dd := seg.delegData
	dd.parentCallID = payload.CallID
	dd.parentArgs = summarizeArgs(payload.Tool, payload.Arguments)
	dd.promptText = delegateArgText(payload.Arguments)
	if dd.taskPreview == "" {
		dd.taskPreview = dd.parentArgs
	}
	if dd.toolLabel == "" && isSpecializedDelegateTool(payload.Tool) {
		dd.toolLabel = strings.ToLower(strings.TrimSpace(payload.Tool))
	}
	seg.renderDirty = true
	return true
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

	idx := len(b.segments)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
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
		},
		renderDirty: true,
	})
	b.pendingDelegateParents = append(b.pendingDelegateParents, idx)
}

func (b *contentBuffer) handleParentDelegateToolCallStarted(payload output.ToolCallStartedEvent) {
	if idx, found := b.dequeuePendingDelegationStartSegment(); found {
		b.bindParentDelegateCall(idx, payload)
		return
	}

	summary := summarizeArgs(payload.Tool, payload.Arguments)
	promptText := delegateArgText(payload.Arguments)
	toolLabel := ""
	if isSpecializedDelegateTool(payload.Tool) {
		toolLabel = strings.ToLower(strings.TrimSpace(payload.Tool))
	}
	idx := len(b.segments)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			toolLabel:       toolLabel,
			taskPreview:     summary,
			promptText:      promptText,
			promptCollapsed: true,
			parentCallID:    payload.CallID,
			parentArgs:      summary,
			status:          "active",
			collapsed:       true,
			extMax:          defaultDelegationExtensionMax,
		},
		renderDirty: true,
	})
	b.pendingDelegateParents = append(b.pendingDelegateParents, idx)
}

func (b *contentBuffer) handleDelegationExtension(event output.Event) {
	payload, ok := event.Payload.(output.DelegationExtensionEvent)
	if !ok {
		return
	}
	if idx, active := b.activeDelegations[payload.AgentID]; active {
		if dd := b.segments[idx].delegData; dd != nil {
			dd.extCurrent = payload.Extension
			dd.extMax = payload.MaxExtensions
			b.markDelegationDirty(idx)
		}
		return
	}
	// Also check completed/failed segments that may no longer be active.
	if idx, found := b.findDelegationSegment(payload.AgentID); found {
		if dd := b.segments[idx].delegData; dd != nil {
			dd.extCurrent = payload.Extension
			dd.extMax = payload.MaxExtensions
			b.markDelegationDirty(idx)
		}
	}
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
	if idx, found := b.dequeuePendingDelegateParentSegment(); found {
		dd := b.segments[idx].delegData
		dd.agentID = payload.AgentID
		if preview != "" {
			dd.taskPreview = preview
		}
		dd.startTime = nanoNow()
		dd.status = "active"
		dd.collapsed = true
		dd.extMax = defaultDelegationExtensionMax
		b.activeDelegations[payload.AgentID] = idx
		b.markDelegationDirty(idx)
		return
	}
	idx := len(b.segments)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			agentID:         payload.AgentID,
			taskPreview:     preview,
			promptText:      preview,
			promptCollapsed: true,
			startTime:       nanoNow(),
			status:          "active",
			collapsed:       true,
			extMax:          defaultDelegationExtensionMax,
		},
		renderDirty: true,
	})
	b.activeDelegations[payload.AgentID] = idx
	b.pendingDelegationStarts = append(b.pendingDelegationStarts, idx)
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
			if dd.isFollowUp {
				dd.turnCount = max(0, payload.TurnCount-dd.baselineTurnCount)
				dd.toolCallCount = max(0, payload.ToolCallCount-dd.baselineToolCallCount)
				dd.tokenCount = max(0, payload.TokenCount-dd.baselineTokenCount)
				// Unlike TurnCount, the cache counters are cumulative across
				// follow-ups: the payload carries the child's whole-life totals
				// (accumulated in internal/delegation), so they are rendered
				// verbatim with no baseline subtraction.
				dd.cacheReadTokens = payload.CacheReadTokens
				dd.inputTokens = payload.InputTokens
				dd.cacheCreateTokens = payload.CacheCreateTokens
			} else {
				dd.turnCount = payload.TurnCount
				dd.tokenCount = payload.TokenCount
				dd.toolCallCount = payload.ToolCallCount
				dd.cacheReadTokens = payload.CacheReadTokens
				dd.inputTokens = payload.InputTokens
				dd.cacheCreateTokens = payload.CacheCreateTokens
			}
			dd.cacheHitRate, dd.cacheHitOK = usagestats.HitRate(dd.cacheReadTokens, dd.inputTokens, dd.cacheCreateTokens)
			dd.elapsed = formatElapsed(dd.startTime, nanoNow())
			dd.output = payload.Output
		}
		b.segments[idx].renderDirty = true
		delete(b.activeDelegations, payload.AgentID)
		return
	}
	cacheHitRate, cacheHitOK := usagestats.HitRate(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			agentID:           payload.AgentID,
			status:            "complete",
			resultStatus:      payload.Status,
			turnCount:         payload.TurnCount,
			tokenCount:        payload.TokenCount,
			toolCallCount:     payload.ToolCallCount,
			cacheReadTokens:   payload.CacheReadTokens,
			inputTokens:       payload.InputTokens,
			cacheCreateTokens: payload.CacheCreateTokens,
			cacheHitRate:      cacheHitRate,
			cacheHitOK:        cacheHitOK,
			output:            payload.Output,
			collapsed:         true,
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

func (b *contentBuffer) handleAdvisorStarted(event output.Event) {
	payload, ok := event.Payload.(output.AdvisorStartedEvent)
	if !ok {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
		return
	}
	idx := len(b.segments)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			isAdvisor:        true,
			toolLabel:        "advisor",
			taskPreview:      "stronger-model steering",
			currentOperation: "consulting stronger-model advisor",
			status:           "active",
			collapsed:        true,
			modelName:        payload.Model,
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

func (b *contentBuffer) handleAdvisorComplete(event output.Event) {
	payload, ok := event.Payload.(output.AdvisorCompleteEvent)
	if !ok {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
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
	dd.modelName = payload.Model
	dd.advisorUse = payload.UseNumber
	dd.advisorMaxUses = payload.MaxUses
	dd.elapsed = formatElapsed(dd.startTime, nanoNow())
	dd.output = payload.Note
	dd.status = "complete"
	dd.resultStatus = "complete"
	dd.cacheHitRate, dd.cacheHitOK = usagestats.HitRate(payload.CacheReadTokens, payload.InputTokens, payload.CacheCreateTokens)
	if strings.TrimSpace(payload.Error) != "" {
		dd.output = payload.Error
		dd.status = "failed"
		dd.resultStatus = "failed"
	}
	b.segments[idx].renderDirty = true
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

func (b *contentBuffer) handleAdvisorBudgetExhausted(event output.Event) {
	payload, ok := event.Payload.(output.AdvisorBudgetExhaustedEvent)
	if !ok {
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
		return
	}
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
			modelName:       payload.Model,
			advisorUse:      payload.Used,
			advisorMaxUses:  payload.MaxUses,
			advisorQuestion: payload.Question,
			advisorFiles:    payload.Files,
		},
		renderDirty: true,
	})
}

func (b *contentBuffer) handleAdvisorThinkingChunk(event output.Event) {
	idx := b.activeAdvisorSegment - 1
	if idx < 0 || idx >= len(b.segments) || b.segments[idx].kind != segmentDelegation || b.segments[idx].delegData == nil || !b.segments[idx].delegData.isAdvisor {
		return
	}
	dd := b.segments[idx].delegData
	if b.applyDelegationThinkingChunk(dd, event) {
		b.segments[idx].renderDirty = true
	}
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

// findChildDelegationInfo searches for a delegation segment by agentID and returns
// its label and toolLabel. Falls back gracefully if not found.
func (b *contentBuffer) findChildDelegationInfo(agentID string) (label, toolLabel string) {
	if agentID == "" {
		return "", ""
	}
	// Search backwards for the most recent delegation with this agentID
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := b.segments[i]
		if seg.kind != segmentDelegation || seg.delegData == nil {
			continue
		}
		if seg.delegData.agentID == agentID {
			return agentID, seg.delegData.toolLabel
		}
	}
	return agentID, ""
}

// captureChildBaselineStats searches for the most recent delegation segment
// with the given agentID and returns its cumulative turn, tool-call, and
// token counts. These form the baseline that must be subtracted from
// follow-up DelegationCompleteEvent payload values to obtain per-follow-up
// deltas. Returns zeroes when the segment is not found or has no data.
//
// Cache token counts are deliberately excluded: they are cumulative across
// follow-ups by construction (accumulated in internal/delegation and carried
// in the payload), so subtracting a baseline would be wrong — the payload
// values are rendered verbatim.
func (b *contentBuffer) captureChildBaselineStats(agentID string) (turns, toolCalls, tokens int) {
	if agentID == "" {
		return 0, 0, 0
	}
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := b.segments[i]
		if seg.kind != segmentDelegation || seg.delegData == nil {
			continue
		}
		if seg.delegData.agentID == agentID {
			dd := seg.delegData
			return dd.turnCount, dd.toolCallCount, dd.tokenCount
		}
	}
	return 0, 0, 0
}
