package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

const (
	delegationTranscriptLimit          = 100
	delegationOperationPreviewMaxRunes = 60
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
	case output.EventTypeAssistantMessage:
		return b.applyDelegationAssistantMessage(dd, event)
	case output.EventTypeToolCallStarted:
		return b.applyDelegationToolCallStarted(dd, event)
	case output.EventTypeToolCallFinished:
		return b.applyDelegationToolCallFinished(dd, event)
	case output.EventTypeStopReason:
		return b.applyDelegationStopReason(dd, event)
	case output.EventTypeTurnStarted,
		output.EventTypeTurnFinished,
		output.EventTypeModelCallStarted,
		output.EventTypeModelCallFinished,
		output.EventTypeAPIRequest,
		output.EventTypeAPIResponse,
		output.EventTypeContextDiagnostics:
		return true
	default:
		return false
	}
}

func (b *contentBuffer) applyDelegationAssistantChunk(dd *delegationDisplayState, event output.Event) bool {
	if b.inCompaction {
		return true
	}
	payload, ok := event.Payload.(output.AssistantChunkEvent)
	if !ok {
		return false
	}
	if payload.Source == output.ChunkSourceScaffoldInference && !b.showInternalScaffoldInference {
		return true
	}
	if payload.Content == "" {
		return true
	}
	entry := dd.appendOrMergeAssistantEntry(payload.Content)
	dd.currentOperation = previewDelegationText(entry.body)
	return true
}

func (b *contentBuffer) applyDelegationAssistantMessage(dd *delegationDisplayState, event output.Event) bool {
	if b.inCompaction {
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
	if dd.taskPreview == "" {
		dd.taskPreview = dd.parentArgs
	}
	seg.renderDirty = true
	return true
}

func (b *contentBuffer) handleParentDelegateToolCallStarted(payload output.ToolCallStartedEvent) {
	if idx, found := b.dequeuePendingDelegationStartSegment(); found {
		b.bindParentDelegateCall(idx, payload)
		return
	}

	summary := summarizeArgs(payload.Tool, payload.Arguments)
	idx := len(b.segments)
	b.segments = append(b.segments, contentSegment{
		kind: segmentDelegation,
		delegData: &delegationDisplayState{
			taskPreview:  summary,
			parentCallID: payload.CallID,
			parentArgs:   summary,
			status:       "active",
			collapsed:    true,
		},
		renderDirty: true,
	})
	b.pendingDelegateParents = append(b.pendingDelegateParents, idx)
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
		b.activeDelegations[payload.AgentID] = idx
		b.markDelegationDirty(idx)
		return
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

func (dd *delegationDisplayState) appendOrMergeAssistantEntry(content string) *delegationTranscriptEntry {
	if last := dd.lastEntry(); last != nil && last.kind == delegationTranscriptEntryAssistant {
		last.body += content
		return last
	}
	idx := dd.appendTranscriptEntry(delegationTranscriptEntry{
		kind: delegationTranscriptEntryAssistant,
		body: content,
	})
	return &dd.entries[idx]
}

func (dd *delegationDisplayState) appendTranscriptEntry(entry delegationTranscriptEntry) int {
	dd.entries = append(dd.entries, entry)
	dd.trimTranscriptEntries()
	return len(dd.entries) - 1
}

func (dd *delegationDisplayState) trimTranscriptEntries() {
	if len(dd.entries) <= delegationTranscriptLimit {
		return
	}
	drop := len(dd.entries) - delegationTranscriptLimit
	dd.entries = append([]delegationTranscriptEntry(nil), dd.entries[drop:]...)
	if len(dd.childToolEntries) == 0 {
		return
	}
	for callID, idx := range dd.childToolEntries {
		idx -= drop
		if idx < 0 || idx >= len(dd.entries) {
			delete(dd.childToolEntries, callID)
			continue
		}
		dd.childToolEntries[callID] = idx
	}
}

func (dd *delegationDisplayState) ensureChildToolEntries() {
	if dd.childToolEntries == nil {
		dd.childToolEntries = make(map[string]int)
	}
}

func (dd *delegationDisplayState) lastEntry() *delegationTranscriptEntry {
	if len(dd.entries) == 0 {
		return nil
	}
	return &dd.entries[len(dd.entries)-1]
}

func (dd *delegationDisplayState) findChildToolEntry(callID string) (int, bool) {
	if callID == "" {
		return 0, false
	}
	if len(dd.childToolEntries) == 0 {
		return 0, false
	}
	idx, ok := dd.childToolEntries[callID]
	if !ok || idx < 0 || idx >= len(dd.entries) {
		return 0, false
	}
	entry := dd.entries[idx]
	if entry.kind != delegationTranscriptEntryTool || entry.callID != callID {
		return 0, false
	}
	return idx, true
}

func previewDelegationOperation(tool, args string) string {
	head := strings.TrimSpace(tool)
	tail := normalizeDelegationText(args)
	switch {
	case head == "" && tail == "":
		return ""
	case tail == "":
		return previewDelegationText(head)
	case head == "":
		return previewDelegationText(tail)
	default:
		return previewDelegationText(head + ": " + tail)
	}
}

func previewDelegationText(text string) string {
	normalized := normalizeDelegationText(text)
	runes := []rune(normalized)
	if len(runes) <= delegationOperationPreviewMaxRunes {
		return normalized
	}
	return string(runes[:delegationOperationPreviewMaxRunes-3]) + "..."
}

func normalizeDelegationText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}
