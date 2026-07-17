package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

func isCompletionStopReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "complete", "end_turn":
		return true
	default:
		return false
	}
}

func shouldSkipToolEvent(tool string) bool {
	if strings.EqualFold(tool, "display_file") {
		return true
	}
	if strings.EqualFold(tool, "advisor") {
		return true
	}
	return false
}

func isDelegateOrSpecialized(tool string) bool {
	return strings.EqualFold(tool, "delegate") || isSpecializedDelegateTool(tool)
}

func (b *contentBuffer) appendToolCallStartedEvent(event output.Event) {
	b.finishStreaming()
	b.streamingPhase = "tool"
	if payload, ok := event.Payload.(output.ToolCallStartedEvent); ok {
		if shouldSkipToolEvent(payload.Tool) {
			return
		}
		if isDelegateOrSpecialized(payload.Tool) {
			if strings.EqualFold(payload.Tool, "follow_up") {
				b.handleFollowUpToolCallStarted(payload)
				return
			}
			b.handleParentDelegateToolCallStarted(payload)
			return
		}
		rawArgs := cloneToolArguments(payload.Arguments)
		toolName := normalizeToolName(payload.Tool)
		tc := &toolCallSegment{
			tool:      toolName,
			args:      summarizeArgs(payload.Tool, payload.Arguments),
			callID:    payload.CallID,
			collapsed: true,
			rawArgs:   rawArgs,
		}
		tc.preview = output.BuildToolPreview(tc.tool, rawArgs, "")
		if tc.preview.Kind != output.ToolPreviewKindPlain {
			tc.bodyKind = previewBodyKind(tc.tool, tc.preview)
		}
		if b.appendAdjacentToolCall(tc) {
			return
		}
		b.segments = append(b.segments, contentSegment{kind: segmentToolCall, toolData: tc, renderDirty: true})
		return
	}
	b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
}

func (b *contentBuffer) appendToolCallFinishedEvent(event output.Event) {
	b.finishStreaming()
	if payload, ok := event.Payload.(output.ToolCallFinishedEvent); ok {
		if shouldSkipToolEvent(payload.Tool) {
			return
		}
		for i := len(b.segments) - 1; i >= 0; i-- {
			switch b.segments[i].kind {
			case segmentToolCall:
				if td := b.segments[i].toolData; td != nil && callIDsMatch(td.callID, payload.CallID) {
					b.applyFinishedToolCallResult(&b.segments[i], td, payload)
					return
				}
			case segmentToolCallGroup:
				group := b.segments[i].toolGroupData
				if group == nil {
					continue
				}
				for j := len(group.entries); j >= 1; j-- {
					td := group.entries[j-1]
					if td == nil || !callIDsMatch(td.callID, payload.CallID) {
						continue
					}
					b.applyFinishedToolCallResult(&b.segments[i], td, payload)
					return
				}
			case segmentDelegation:
				if dd := b.segments[i].delegData; dd != nil && dd.parentCallID != "" && callIDsMatch(dd.parentCallID, payload.CallID) {
					if dd.agentID == "" && payload.Error != "" {
						b.removeFromPendingDelegateParents(i)
						dd.status = "failed"
						b.segments[i].renderDirty = true
					}
					return
				}
			}
		}
		return
	}
	b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
}

func (b *contentBuffer) appendDisplayFileEvent(event output.Event) {
	b.finishStreaming()
	if payload, ok := event.Payload.(output.DisplayFilePayload); ok {
		preview := payload.Preview
		idx := len(b.segments)
		if b.collapseState == nil {
			b.collapseState = make(map[int]bool)
		}
		b.segments = append(b.segments, contentSegment{
			kind: segmentToolCall,
			toolData: &toolCallSegment{
				tool:           "display_file",
				args:           payload.Path,
				bodyKind:       "file",
				collapsed:      false,
				displayPreview: &preview,
			},
			renderDirty: true,
		})
		b.collapseState[idx] = false
		return
	}
	b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
}

func (b *contentBuffer) appendStopReasonEvent(event output.Event) {
	b.finishStreaming()
	if payload, ok := event.Payload.(output.StopReasonEvent); ok && isCompletionStopReason(payload.Reason) && payload.Error == "" {
		if reason := strings.TrimSpace(payload.Reason); reason != "" {
			b.segments = append(b.segments, contentSegment{
				kind:        segmentStatus,
				text:        reason,
				timestamp:   timeNow(),
				renderDirty: true,
			})
		}
		return
	}
	b.appendLine(formatStopReasonEvent(event))
}

func (b *contentBuffer) appendUserInputEvent(event output.Event) {
	if payload, ok := event.Payload.(output.UserInputEvent); ok && strings.TrimSpace(payload.Content) != "" {
		idx := len(b.segments)
		if b.collapseState == nil {
			b.collapseState = make(map[int]bool)
		}
		b.segments = append(b.segments, contentSegment{kind: segmentUserMarkdown, text: payload.Content, timestamp: timeNow(), renderDirty: true})
		b.collapseState[idx] = false
	}
}

func (b *contentBuffer) AppendLine(line string) {
	b.finishStreaming()
	b.appendLine(line)
}

func (b *contentBuffer) AppendUser(text string) {
	b.finishStreaming()
	idx := len(b.segments)
	if b.collapseState == nil {
		b.collapseState = make(map[int]bool)
	}
	b.segments = append(b.segments, contentSegment{kind: segmentUserMarkdown, text: text, timestamp: timeNow(), renderDirty: true})
	b.collapseState[idx] = false
}

// AppendPendingSteer adds a steer message to the content buffer in pending/queued state.
// Call PromoteLastPendingSteer when SteerReceivedEvent arrives.
func (b *contentBuffer) AppendPendingSteer(text string) {
	b.finishStreaming()
	idx := len(b.segments)
	if b.collapseState == nil {
		b.collapseState = make(map[int]bool)
	}
	b.segments = append(b.segments, contentSegment{kind: segmentPendingSteer, text: text, renderDirty: true})
	b.collapseState[idx] = false
}

// AppendImagesAttached appends an images-attached segment with structured display data.
// Each image with a non-empty FilePath is stored as a display row; images with empty
// FilePath are skipped. No-op if images is empty.
func (b *contentBuffer) AppendImagesAttached(images []agent.ImageBlock, workingDir, homeDir string) {
	b.finishStreaming()

	if len(images) == 0 {
		return
	}

	const maxPathWidth = 70
	var rows []imagesAttachedRowData

	for _, img := range images {
		if img.FilePath == "" {
			continue
		}

		path := shortenAttachedImagePath(img.FilePath, workingDir, homeDir, maxPathWidth)
		rows = append(rows, imagesAttachedRowData{
			id:     img.ID,
			width:  img.Width,
			height: img.Height,
			size:   tuiFormatSize(img.SizeBytes),
			path:   path,
		})
	}

	if len(rows) == 0 {
		return
	}

	b.segments = append(b.segments, contentSegment{
		kind: segmentImagesAttached,
		imagesAttachedData: &imagesAttachedData{
			rows: rows,
		},
		renderDirty: true,
	})
}

// PromoteLastPendingSteer upgrades the most recent segmentPendingSteer to segmentUserMarkdown,
// indicating the steer was consumed by the agent loop and injected into the conversation.
func (b *contentBuffer) PromoteLastPendingSteer() {
	for i := len(b.segments) - 1; i >= 0; i-- {
		if b.segments[i].kind == segmentPendingSteer {
			b.segments[i].kind = segmentUserMarkdown
			b.segments[i].renderDirty = true
			return
		}
	}
}

func (b *contentBuffer) Clear() {
	b.segments = nil
	b.segmentHeights = nil
	b.streamBuffer = ""
	b.streaming = false
	b.streamingPhase = ""
	b.streamingSource = ""
	b.collapseState = make(map[int]bool)
	b.activeDelegations = nil
	b.pendingDelegateParents = nil
	b.pendingDelegationStarts = nil
	b.activeAdvisorSegment = 0
	// Invalidate render cache.
	b.stringCacheWidth = 0
	b.stringCacheRendered = ""
}

func (b *contentBuffer) appendAdjacentToolCall(tc *toolCallSegment) bool {
	if tc == nil || len(b.segments) == 0 {
		return false
	}
	last := &b.segments[len(b.segments)-1]
	switch last.kind {
	case segmentToolCall:
		if last.toolData == nil || last.toolData.tool != tc.tool {
			return false
		}
		last.toolGroupData = &toolCallGroupSegment{
			tool:    tc.tool,
			entries: []*toolCallSegment{last.toolData, tc},
		}
		last.toolData = nil
		last.kind = segmentToolCallGroup
		last.renderDirty = true
		return true
	case segmentToolCallGroup:
		if last.toolGroupData == nil || last.toolGroupData.tool != tc.tool {
			return false
		}
		last.toolGroupData.entries = append(last.toolGroupData.entries, tc)
		last.renderDirty = true
		return true
	default:
		return false
	}
}

func (b *contentBuffer) applyFinishedToolCallResult(seg *contentSegment, td *toolCallSegment, payload output.ToolCallFinishedEvent) {
	td.body = payload.Result
	td.hasError = payload.Error != ""
	td.meta = "✅"
	if td.hasError {
		td.meta = "❌"
	}
	if payload.Preview.Kind != "" && payload.Preview.Kind != output.ToolPreviewKindPlain {
		td.preview = payload.Preview
	} else {
		td.preview = output.BuildToolPreview(td.tool, td.rawArgs, payload.Result)
	}
	if td.tool == "mutate" && td.preview.HunksFailed > 0 {
		td.hasError = true
		td.meta = "❌"
	}
	if td.preview.Kind != output.ToolPreviewKindPlain {
		td.bodyKind = previewBodyKind(td.tool, td.preview)
	} else {
		td.bodyKind = inferBodyKind(td.tool, payload.Result)
	}
	seg.renderDirty = true
}

func callIDsMatch(existingCallID, payloadCallID string) bool {
	return existingCallID == "" || existingCallID == payloadCallID
}

func normalizeToolName(tool string) string {
	return strings.ToLower(strings.TrimSpace(tool))
}

func (b *contentBuffer) AppendInterrupted() {
	b.finishStreaming()
	b.segments = append(b.segments, contentSegment{kind: segmentInterrupted, renderDirty: true})
}
