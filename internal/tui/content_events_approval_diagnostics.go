package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) appendApprovalRequestedEvent(event output.Event) {
	b.finishStreaming()
	if payload, ok := event.Payload.(output.ApprovalEvent); ok {
		// Embed approval into the most recent tool call segment.
		for i := len(b.segments) - 1; i >= 0; i-- {
			seg := &b.segments[i]
			switch seg.kind {
			case segmentToolCall:
				if seg.toolData == nil || seg.toolData.approvalPending || !callIDsMatch(seg.toolData.callID, payload.CallID) || !strings.EqualFold(normalizeToolName(seg.toolData.tool), normalizeToolName(payload.Tool)) {
					continue
				}
				seg.toolData.approvalPending = true
				seg.toolData.approvalMode = payload.Mode
				seg.toolData.approvalPreview = payload.Preview
				seg.toolData.approvalIdentity = payload.CallID
				seg.toolData.approvalKind = payload.Kind
				seg.toolData.approvalServer = payload.Server
				seg.toolData.approvalMCPTool = payload.ToolName
				seg.toolData.approvalAgentID = event.Scope.AgentID
				seg.toolData.approvalQueueDepth = 0
				seg.toolData.approvalSelectedAction = 0
				seg.toolData.collapsed = false
				seg.renderDirty = true
				b.gen++
				b.recomputeApprovalQueueDepths()
				return
			case segmentToolCallGroup:
				if seg.toolGroupData == nil {
					continue
				}
				for j := len(seg.toolGroupData.entries) - 1; j >= 0; j-- {
					entry := seg.toolGroupData.entries[j]
					if entry == nil || entry.approvalPending || !callIDsMatch(entry.callID, payload.CallID) || !strings.EqualFold(normalizeToolName(entry.tool), normalizeToolName(payload.Tool)) {
						continue
					}
					entry.approvalKind = payload.Kind
					entry.approvalServer = payload.Server
					entry.approvalMCPTool = payload.ToolName
					entry.approvalAgentID = event.Scope.AgentID
					entry.approvalIdentity = payload.CallID
					entry.approvalQueueDepth = 0
					entry.approvalPending = true
					entry.approvalMode = payload.Mode
					entry.approvalPreview = payload.Preview
					entry.approvalSelectedAction = 0
					entry.collapsed = false
					seg.renderDirty = true
					b.recomputeApprovalQueueDepths()
					b.gen++
					return
				}
			}
		}
		// Fallback: no tool segment found — render as standalone pill.
		b.segments = append(b.segments, contentSegment{
			kind: segmentApprovalPill,
			approvalData: &approvalPillData{
				tool:        payload.Tool,
				mode:        payload.Mode,
				preview:     payload.Preview,
				kind:        payload.Kind,
				server:      payload.Server,
				mcpToolName: payload.ToolName,
				identity:    payload.CallID,
				agentID:     event.Scope.AgentID,
				queueDepth:  0,
			},
			renderDirty: true,
		})
		b.recomputeApprovalQueueDepths()
		return
	}
	b.appendStyled(formatApprovalEvent(event), segmentApproval)
}

func (b *contentBuffer) appendApprovalDecisionEvent(event output.Event) {
	b.finishStreaming()
	accepted := event.Type == output.EventTypeApprovalAccepted
	var callID string
	if payload, ok := event.Payload.(output.ApprovalEvent); ok {
		callID = payload.CallID
	}
	if b.resolveEmbeddedApproval(accepted, callID) {
		b.recomputeApprovalQueueDepths()
		return
	}
	// Fallback: resolve a standalone approval pill.
	for i := range b.segments {
		if b.segments[i].kind != segmentApprovalPill || b.segments[i].approvalData == nil || b.segments[i].approvalData.resolved || b.segments[i].approvalData.identity != callID {
			continue
		}
		b.segments[i].approvalData.resolved = true
		b.segments[i].approvalData.accepted = accepted
		b.segments[i].renderDirty = true
		b.gen++
		b.recomputeApprovalQueueDepths()
		return
	}
	b.appendStyled(formatApprovalEvent(event), segmentApproval)
}

func (b *contentBuffer) resolveEmbeddedApproval(accepted bool, callID string) bool {
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := &b.segments[i]
		switch seg.kind {
		case segmentToolCall:
			if seg.toolData != nil && seg.toolData.approvalPending && !seg.toolData.approvalResolved && callIDsMatch(seg.toolData.callID, callID) {
				seg.toolData.approvalResolved = true
				seg.toolData.approvalAccepted = accepted
				seg.renderDirty = true
				b.gen++
				return true
			}

		case segmentToolCallGroup:
			if seg.toolGroupData == nil {
				continue
			}
			for j := len(seg.toolGroupData.entries) - 1; j >= 0; j-- {
				entry := seg.toolGroupData.entries[j]
				if entry != nil && entry.approvalPending && !entry.approvalResolved && callIDsMatch(entry.callID, callID) {
					entry.approvalResolved = true
					entry.approvalAccepted = accepted
					seg.renderDirty = true
					b.gen++
					return true
				}
			}
		}
	}
	return false
}

func (b *contentBuffer) appendContextReportEvent(event output.Event) {
	if payload, ok := event.Payload.(output.ContextReportEvent); ok && strings.TrimSpace(payload.Content) != "" {
		b.finishStreaming()
		b.appendMarkdownBlock(payload.Content)
	}
}

func (b *contentBuffer) appendProviderDiagnosticEvent(event output.Event) {
	payload, ok := event.Payload.(output.ProviderDiagnosticEvent)
	if !ok || (payload.Severity == "info" && payload.Suppressible && payload.Kind == output.ProviderDiagnosticKindTransportOverride) {
		return
	}
	b.finishStreaming()
	b.appendLine(output.FormatEvent(event))
}

func (b *contentBuffer) appendModelCallDiagnosticsEvent(event output.Event) {
	b.finishStreaming()
	payload, ok := output.AsContextCompactionEvent(event.Payload)
	if ok {
		b.handleCompactionDiagnostics(payload)
	}
}

func (b *contentBuffer) handleCompactionDiagnostics(payload output.ContextCompactionEvent) {
	if payload.Severity == "compacting" {
		b.upsertCompactionBanner(compactionBannerData{
			label:           "Compacting",
			subtitle:        "summarizing context",
			finished:        false,
			startTime:       nanoNow(),
			compactionCount: payload.CompactionCount,
		})
		return
	}

	msgCount := payload.CompactedMessages
	summary := "context compacted"
	switch {
	case payload.SummaryTitle != "":
		summary = payload.SummaryTitle
	case msgCount > 0:
		summary = fmt.Sprintf("%d messages summarized into 1", msgCount)
	case payload.CompactedTurns > 0:
		summary = fmt.Sprintf("%d turns summarized", payload.CompactedTurns)
	}

	b.upsertCompactionBanner(compactionBannerData{
		label:             "Context compacted",
		subtitle:          summary,
		finished:          true,
		summary:           summary,
		msgCount:          msgCount,
		compactionCount:   payload.CompactionCount,
		compactedTurns:    payload.CompactedTurns,
		compactedMessages: payload.CompactedMessages,
		retainedTurns:     payload.RetainedTurns,
		retainedMessages:  payload.RetainedMessages,
		mode:              payload.Mode,
		beforeTokens:      payload.BeforePromptTokens,
		beforePct:         payload.BeforeUsagePercent,
		afterTokens:       payload.AfterPromptTokens,
		afterPct:          payload.AfterUsagePercent,
		summaryTitle:      payload.SummaryTitle,
		elapsed:           finishElapsed(b, nanoNow()),
	})
}

// finishElapsed returns a formatted elapsed string using the startTime from the
// current in-progress compaction banner, if one exists. Falls back gracefully
// when no timing data is available (e.g. replayed history).
func finishElapsed(b *contentBuffer, nowNano int64) string {
	if len(b.segments) == 0 {
		return ""
	}
	last := &b.segments[len(b.segments)-1]
	if last.kind != segmentCompactionBanner || last.compactionData == nil || last.compactionData.finished {
		return ""
	}
	if last.compactionData.startTime == 0 {
		// No wall-clock start time available (replayed history); skip elapsed.
		return ""
	}
	return formatElapsed(last.compactionData.startTime, nowNano)
}

// HasActiveCompactions reports whether any compaction banner segment is currently
// in progress (not yet finished).
func (b *contentBuffer) HasActiveCompactions() bool {
	for i := range b.segments {
		seg := &b.segments[i]
		if seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished {
			return true
		}
	}
	return false
}

// AdvanceCompactionSpinners increments the spinnerFrame on every in-progress
// compaction banner segment and marks each one renderDirty.
func (b *contentBuffer) AdvanceCompactionSpinners() {
	for i := range b.segments {
		seg := &b.segments[i]
		if seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished {
			seg.compactionData.spinnerFrame = (seg.compactionData.spinnerFrame + 1) % len(spinnerFrames)
			seg.renderDirty = true
			b.gen++
		}
	}
}

func (b *contentBuffer) upsertCompactionBanner(data compactionBannerData) {
	if len(b.segments) > 0 {
		last := &b.segments[len(b.segments)-1]
		if last.kind == segmentCompactionBanner && last.compactionData != nil && !last.compactionData.finished {
			// Preserve the original startTime so elapsed can be computed on finish.
			if data.startTime == 0 {
				data.startTime = last.compactionData.startTime
			}
			replacement := data
			last.compactionData = &replacement
			last.renderDirty = true
			b.gen++
			return
		}
	}
	replacement := data
	b.segments = append(b.segments, contentSegment{
		kind:           segmentCompactionBanner,
		compactionData: &replacement,
		renderDirty:    true,
	})
}

// clearApprovalState resets approval state on all tool call and tool call group
// segments. Used when a run ends or is stopped to clear stale pending approvals
// from the content buffer.
func (b *contentBuffer) clearApprovalState() {
	b.gen++
	for i := range b.segments {
		seg := &b.segments[i]
		switch seg.kind {
		case segmentToolCall:
			if seg.toolData != nil {
				seg.toolData.approvalPending = false
				seg.toolData.approvalResolved = false
			}
		case segmentToolCallGroup:
			if seg.toolGroupData != nil {
				for _, entry := range seg.toolGroupData.entries {
					if entry != nil {
						entry.approvalPending = false
						entry.approvalResolved = false
					}
				}
			}
		}
	}
}

// recomputeApprovalQueueDepths sets depth to the number of unresolved approval
// requests queued behind each request. Only the FIFO head receives a suffix.
func (b *contentBuffer) recomputeApprovalQueueDepths() {
	var queued []*int
	for i := range b.segments {
		seg := &b.segments[i]
		switch seg.kind {
		case segmentApprovalPill:
			if seg.approvalData != nil {
				seg.approvalData.queueDepth = 0
				if !seg.approvalData.resolved {
					queued = append(queued, &seg.approvalData.queueDepth)
				}
			}
		case segmentToolCall:
			if seg.toolData != nil {
				seg.toolData.approvalQueueDepth = 0
				if seg.toolData.approvalPending && !seg.toolData.approvalResolved {
					queued = append(queued, &seg.toolData.approvalQueueDepth)
				}
			}
		case segmentToolCallGroup:
			queued = appendToolGroupApprovalQueueDepths(seg, queued)
		}
	}
	if len(queued) > 0 {
		*queued[0] = len(queued) - 1
	}
}

func appendToolGroupApprovalQueueDepths(seg *contentSegment, queued []*int) []*int {
	if seg.toolGroupData == nil {
		return queued
	}
	for _, entry := range seg.toolGroupData.entries {
		if entry == nil {
			continue
		}
		entry.approvalQueueDepth = 0
		if entry.approvalPending && !entry.approvalResolved {
			queued = append(queued, &entry.approvalQueueDepth)
		}
	}
	return queued
}
