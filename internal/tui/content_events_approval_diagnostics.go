package tui

import (
	"fmt"
	"strings"

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
			label:    "Compacting",
			subtitle: "summarizing context",
			finished: false,
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
		label:    "Context compacted",
		subtitle: summary,
		finished: true,
		summary:  summary,
		msgCount: msgCount,
	})
}

func (b *contentBuffer) upsertCompactionBanner(data compactionBannerData) {
	if len(b.segments) > 0 {
		last := &b.segments[len(b.segments)-1]
		if last.kind == segmentCompactionBanner && last.compactionData != nil && !last.compactionData.finished {
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
