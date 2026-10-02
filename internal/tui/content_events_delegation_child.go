package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/usagestats"
)

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
	b.noteDelegationEvent(event.Scope.AgentID)
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
			if payload.PromptTokens > 0 {
				nonCached := max(0, payload.PromptTokens-payload.CacheReadTokens-payload.CacheCreateTokens)
				dd.cacheReadTokens += payload.CacheReadTokens
				dd.inputTokens += nonCached
				dd.cacheCreateTokens += payload.CacheCreateTokens
				dd.cacheHitRate, dd.cacheHitOK = usagestats.HitRate(dd.cacheReadTokens, dd.inputTokens, dd.cacheCreateTokens)
				dd.latestCacheHitRate, dd.latestCacheHitOK = usagestats.HitRate(payload.CacheReadTokens, nonCached, payload.CacheCreateTokens)
				dd.tokenCount += payload.CompletionTokens
			}
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

// isScopedChildTranscriptEvent reports whether an event type belongs to the set
// that applyScopedDelegationEvent handles (i.e., child transcript events that
// must not fall through to top-level handlers when the agent is not yet in
// activeDelegations). Delegation lifecycle events (Started, Complete, Failed,
// etc.) are excluded because they should fall through to appendDelegationEvent.
func isScopedChildTranscriptEvent(eventType string) bool {
	switch eventType {
	case output.EventTypeAssistantChunk,
		output.EventTypeThinkingChunk,
		output.EventTypeAssistantMessage,
		output.EventTypeToolCallStarted,
		output.EventTypeToolCallFinished,
		output.EventTypeStopReason,
		output.EventTypeModelCallStarted,
		output.EventTypeContextDiagnostics,
		output.EventTypeModelCallFinished,
		output.EventTypeAPIResponse,
		output.EventTypeAPIRequest:
		return true
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
