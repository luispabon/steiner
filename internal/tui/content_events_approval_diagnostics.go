package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) appendApprovalRequestedEvent(event output.Event) {
	b.finishStreaming()
	if payload, ok := event.Payload.(output.ApprovalEvent); ok {
		// An event without a request ID can never be matched to a decision, so
		// it is not rendered as an approval control that could never resolve.
		if payload.RequestID == "" {
			return
		}
		identity := payload.RequestID
		// Parent approvals embed into the tool call that raised them. A scoped
		// child approval renders as a standalone pill: its call ID may equal a
		// parent call's, so embedding could attribute it to the wrong segment.
		if event.Scope.AgentID == "" && b.embedApprovalRequest(payload, identity) {
			return
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
				identity:    identity,
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

// embedApprovalRequest marks the parent tool call that raised an unscoped
// approval request. It reports false when no eligible segment matches.
func (b *contentBuffer) embedApprovalRequest(payload output.ApprovalEvent, identity string) bool {
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
			seg.toolData.approvalIdentity = identity
			seg.toolData.approvalKind = payload.Kind
			seg.toolData.approvalServer = payload.Server
			seg.toolData.approvalMCPTool = payload.ToolName
			seg.toolData.approvalQueueDepth = 0
			seg.toolData.approvalSelectedAction = 0
			seg.toolData.collapsed = false
			seg.renderDirty = true
			b.gen++
			b.recomputeApprovalQueueDepths()
			return true
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
				entry.approvalIdentity = identity
				entry.approvalQueueDepth = 0
				entry.approvalPending = true
				entry.approvalMode = payload.Mode
				entry.approvalPreview = payload.Preview
				entry.approvalSelectedAction = 0
				entry.collapsed = false
				seg.renderDirty = true
				b.recomputeApprovalQueueDepths()
				b.gen++
				return true
			}
		}
	}
	return false
}

func (b *contentBuffer) appendApprovalDecisionEvent(event output.Event) {
	b.finishStreaming()
	accepted := event.Type == output.EventTypeApprovalAccepted
	var identity string
	if payload, ok := event.Payload.(output.ApprovalEvent); ok {
		identity = payload.RequestID
	}
	// A decision without a request ID cannot name a rendered request, so it
	// leaves the transcript untouched.
	if identity == "" {
		return
	}
	if b.resolveEmbeddedApproval(accepted, identity) {
		b.recomputeApprovalQueueDepths()
		return
	}
	// Fallback: resolve a standalone approval pill.
	for i := range b.segments {
		if b.segments[i].kind != segmentApprovalPill || b.segments[i].approvalData == nil || b.segments[i].approvalData.resolved || b.segments[i].approvalData.identity != identity {
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

func (b *contentBuffer) resolveEmbeddedApproval(accepted bool, identity string) bool {
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := &b.segments[i]
		switch seg.kind {
		case segmentToolCall:
			if seg.toolData != nil && seg.toolData.approvalPending && !seg.toolData.approvalResolved && seg.toolData.approvalIdentity == identity {
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
				if entry != nil && entry.approvalPending && !entry.approvalResolved && entry.approvalIdentity == identity {
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
	switch {
	case payload.Kind == output.DiagnosticKindRetry:
		// Retry attempts show on the activity row, not in the transcript.
		return
	case payload.Kind == output.DiagnosticKindToolPanic:
		b.AppendNotice(noticeError, "internal error: a tool panicked; full stack in the dev log", event.Scope.AgentID)
	case payload.Severity == "error":
		b.AppendNotice(noticeError, payload.Message, event.Scope.AgentID)
	case payload.Severity == "warning":
		b.AppendNotice(noticeWarn, payload.Message, event.Scope.AgentID)
	default:
		b.finishStreaming()
		b.appendLine(output.FormatEvent(event))
	}
}

func (b *contentBuffer) appendModelCallDiagnosticsEvent(event output.Event) {
	b.finishStreaming()
	// A scoped event that reaches this point has no delegation binding. It must
	// not update the orchestrator banner.
	if event.Scope.AgentID != "" {
		return
	}
	payload, ok := output.AsContextCompactionEvent(event.Payload)
	if ok {
		b.handleCompactionDiagnostics(payload)
	}
}

func (b *contentBuffer) handleCompactionDiagnostics(payload output.ContextCompactionEvent) {
	if payload.Severity == "compacting" {
		b.upsertCompactionBanner(compactionBannerData{
			label:                "Compacting",
			subtitle:             "summarizing context",
			finished:             false,
			startTime:            nanoNow(),
			compactionCount:      payload.CompactionCount,
			compactionModel:      payload.CompactionModel,
			compactionModelAlias: payload.CompactionModelAlias,
			fallbackStatus:       payload.FallbackStatus,
			fallbackReason:       payload.FallbackReason,
		})
		return
	}
	if payload.Severity == "skipped" {
		b.upsertCompactionBanner(compactionBannerData{
			label:                "Compaction skipped",
			subtitle:             "skipped: no stage applied",
			finished:             true,
			skipped:              true,
			compactionCount:      payload.CompactionCount,
			compactionModel:      payload.CompactionModel,
			compactionModelAlias: payload.CompactionModelAlias,
			fallbackStatus:       payload.FallbackStatus,
			fallbackReason:       payload.FallbackReason,
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
		label:                "Context compacted",
		subtitle:             summary,
		finished:             true,
		summary:              summary,
		msgCount:             msgCount,
		compactionCount:      payload.CompactionCount,
		compactedTurns:       payload.CompactedTurns,
		compactedMessages:    payload.CompactedMessages,
		retainedTurns:        payload.RetainedTurns,
		retainedMessages:     payload.RetainedMessages,
		retainedRawTurns:     payload.RetainedRawTurns,
		mode:                 payload.Mode,
		beforeTokens:         payload.BeforePromptTokens,
		beforePct:            payload.BeforeUsagePercent,
		afterTokens:          payload.AfterPromptTokens,
		afterPct:             payload.AfterUsagePercent,
		summaryTitle:         payload.SummaryTitle,
		summaryBytes:         payload.SummaryBytes,
		summaryTokenBudget:   payload.SummaryTokenBudget,
		contextWindow:        payload.ContextWindow,
		thresholdTokens:      payload.CompactionThresholdTokens,
		compactionThreshold:  payload.CompactionThreshold,
		thresholdAchieved:    payload.ThresholdAchieved,
		truncated:            payload.Truncated,
		elapsed:              finishElapsed(b, nanoNow()),
		cacheReadTokens:      payload.CacheReadTokens,
		inputTokens:          payload.InputTokens,
		cacheCreateTokens:    payload.CacheCreateTokens,
		compactionModel:      payload.CompactionModel,
		compactionModelAlias: payload.CompactionModelAlias,
		fallbackStatus:       payload.FallbackStatus,
		fallbackReason:       payload.FallbackReason,
	})
}

// finishElapsed returns a formatted elapsed string using the startTime from the
// most recent in-progress compaction banner, searching backward across all
// segments. Falls back gracefully when no timing data is available (e.g. replayed history).
func finishElapsed(b *contentBuffer, nowNano int64) string {
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := &b.segments[i]
		if seg.kind != segmentCompactionBanner || seg.compactionData == nil || seg.compactionData.finished {
			continue
		}
		if seg.compactionData.startTime == 0 {
			// No wall-clock start time available (replayed history); skip elapsed.
			return ""
		}
		return formatElapsed(seg.compactionData.startTime, nowNano)
	}
	return ""
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
	for i := len(b.segments) - 1; i >= 0; i-- {
		seg := &b.segments[i]
		if seg.kind != segmentCompactionBanner || seg.compactionData == nil || seg.compactionData.finished {
			continue
		}
		// Found an unfinished banner; unconditionally preserve its original startTime
		// so elapsed time is measured from the first progress event, not the latest.
		// The user's expansion state survives live updates and the finish event.
		oldStartTime := seg.compactionData.startTime
		oldExpanded := seg.compactionData.expanded
		replacement := data
		replacement.startTime = oldStartTime
		replacement.expanded = oldExpanded
		seg.compactionData = &replacement
		seg.renderDirty = true
		b.gen++
		return
	}
	// No unfinished banner found; create a new one.
	replacement := data
	b.segments = append(b.segments, contentSegment{
		kind:           segmentCompactionBanner,
		compactionData: &replacement,
		renderDirty:    true,
	})
}

// clearApprovalStateExcept resets approval state on tool call and tool call
// group segments, and drops unresolved approval pills, except for the pending
// requests whose identities are listed in preserve. A top-level terminal reset
// passes every request the coordinator still has registered, so live async
// child approvals keep their control and unregistered ones do not survive.
// Resolved pills keep their outcome line.
func (b *contentBuffer) clearApprovalStateExcept(preserve map[string]struct{}) {
	b.gen++
	for i := range b.segments {
		seg := &b.segments[i]
		switch seg.kind {
		case segmentApprovalPill:
			clearApprovalPill(seg, preserve)
		case segmentToolCall:
			clearToolCallApproval(seg, preserve)
		case segmentToolCallGroup:
			clearToolGroupApproval(seg, preserve)
		}
	}
}

// approvalIdentityPreserved reports whether the pending request identity is
// still registered and must keep its control.
func approvalIdentityPreserved(preserve map[string]struct{}, identity string) bool {
	if identity == "" {
		return false
	}
	_, ok := preserve[identity]
	return ok
}

func clearApprovalPill(seg *contentSegment, preserve map[string]struct{}) {
	if seg.approvalData == nil || seg.approvalData.resolved || seg.approvalData.cleared {
		return
	}
	if approvalIdentityPreserved(preserve, seg.approvalData.identity) {
		return
	}
	seg.approvalData.cleared = true
	seg.renderDirty = true
}

func clearToolCallApproval(seg *contentSegment, preserve map[string]struct{}) {
	if seg.toolData == nil {
		return
	}
	if approvalIdentityPreserved(preserve, seg.toolData.approvalIdentity) {
		return
	}
	seg.toolData.approvalPending = false
	seg.toolData.approvalResolved = false
	seg.renderDirty = true
}

func clearToolGroupApproval(seg *contentSegment, preserve map[string]struct{}) {
	if seg.toolGroupData == nil {
		return
	}
	for _, entry := range seg.toolGroupData.entries {
		if entry == nil {
			continue
		}
		if approvalIdentityPreserved(preserve, entry.approvalIdentity) {
			continue
		}
		entry.approvalPending = false
		entry.approvalResolved = false
	}
	seg.renderDirty = true
}

// queuedApproval pairs a pending approval's identity with its depth field so
// the FIFO head can be found by identity rather than transcript position.
type queuedApproval struct {
	identity string
	depth    *int
}

// recomputeApprovalQueueDepths sets depth to the number of unresolved approval
// requests queued behind each request. Only the coordinator's FIFO head receives
// a suffix; without a coordinator the first pending segment does.
func (b *contentBuffer) recomputeApprovalQueueDepths() {
	var queued []queuedApproval
	for i := range b.segments {
		seg := &b.segments[i]
		switch seg.kind {
		case segmentApprovalPill:
			if seg.approvalData != nil {
				seg.approvalData.queueDepth = 0
				if !seg.approvalData.resolved && !seg.approvalData.cleared {
					queued = append(queued, queuedApproval{identity: seg.approvalData.identity, depth: &seg.approvalData.queueDepth})
				}
			}
		case segmentToolCall:
			if seg.toolData != nil {
				seg.toolData.approvalQueueDepth = 0
				if seg.toolData.approvalPending && !seg.toolData.approvalResolved {
					queued = append(queued, queuedApproval{identity: seg.toolData.approvalIdentity, depth: &seg.toolData.approvalQueueDepth})
				}
			}
		case segmentToolCallGroup:
			queued = appendToolGroupApprovalQueueDepths(seg, queued)
		}
	}
	if len(queued) == 0 {
		return
	}
	head := b.approvalQueueHead(queued)
	if head < 0 {
		return
	}
	*queued[head].depth = len(queued) - 1
}

// approvalQueueHead resolves which pending approval should carry the queue
// suffix. Without a coordinator the first pending segment does. With one, only
// the segment matching the coordinator head does, so a coordinated queue whose
// head is empty (every request already decided) attaches no suffix, and an
// unknown head attaches no suffix rather than guessing from transcript order.
func (b *contentBuffer) approvalQueueHead(queued []queuedApproval) int {
	if !b.approvalCoordinated {
		return 0
	}
	for i := range queued {
		if queued[i].identity == b.approvalHead {
			return i
		}
	}
	return -1
}

func appendToolGroupApprovalQueueDepths(seg *contentSegment, queued []queuedApproval) []queuedApproval {
	if seg.toolGroupData == nil {
		return queued
	}
	for _, entry := range seg.toolGroupData.entries {
		if entry == nil {
			continue
		}
		entry.approvalQueueDepth = 0
		if entry.approvalPending && !entry.approvalResolved {
			queued = append(queued, queuedApproval{identity: entry.approvalIdentity, depth: &entry.approvalQueueDepth})
		}
	}
	return queued
}
