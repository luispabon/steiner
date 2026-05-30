package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) appendApprovalRequestedEvent(event output.Event) {
	b.finishStreaming()
	if payload, ok := event.Payload.(output.ApprovalEvent); ok {
		b.segments = append(b.segments, contentSegment{
			kind: segmentApprovalPill,
			approvalData: &approvalPillData{
				tool:    payload.Tool,
				mode:    payload.Mode,
				preview: payload.Preview,
			},
			renderDirty: true,
		})
		return
	}
	b.appendStyled(formatApprovalEvent(event), segmentApproval)
}

func (b *contentBuffer) appendApprovalDecisionEvent(event output.Event) {
	b.finishStreaming()
	accepted := event.Type == output.EventTypeApprovalAccepted
	for i := len(b.segments) - 1; i >= 0; i-- {
		if b.segments[i].kind != segmentApprovalPill || b.segments[i].approvalData == nil || b.segments[i].approvalData.resolved {
			continue
		}
		b.segments[i].approvalData.resolved = true
		b.segments[i].approvalData.accepted = accepted
		b.segments[i].renderDirty = true
		return
	}
	b.appendStyled(formatApprovalEvent(event), segmentApproval)
}

func (b *contentBuffer) appendContextReportEvent(event output.Event) {
	if payload, ok := event.Payload.(output.ContextReportEvent); ok && strings.TrimSpace(payload.Content) != "" {
		b.finishStreaming()
		b.appendMarkdownBlock(payload.Content)
	}
}

func (b *contentBuffer) appendModelCallDiagnosticsEvent(event output.Event) {
	b.finishStreaming()
	payload, ok := event.Payload.(output.ContextDiagnosticsEvent)
	if !ok {
		return
	}
	switch payload.Kind {
	case "compaction":
		b.handleCompactionDiagnostics(payload)
	case "session_health":
		if b.inCompaction {
			return
		}
		b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentThinking)
	}
}

func (b *contentBuffer) handleCompactionDiagnostics(payload output.ContextDiagnosticsEvent) {
	if payload.Severity == "compacting" {
		b.upsertCompactionBanner(compactionBannerData{
			label:           "Compacting",
			subtitle:        "summarizing context",
			finished:        false,
			startTime:       time.Now().UnixNano(),
			compactionCount: payload.CompactionCount,
			collapsed:       true,
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

	now := time.Now().UnixNano()
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
		elapsed:           finishElapsed(b, now),
		collapsed:         true,
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
