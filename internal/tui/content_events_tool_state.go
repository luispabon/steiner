package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) appendToolCallStartedEvent(event output.Event) {
	b.finishStreaming()
	b.streamingPhase = "tool"
	if payload, ok := event.Payload.(output.ToolCallStartedEvent); ok {
		if strings.EqualFold(payload.Tool, "display_file") {
			return
		}
		if strings.EqualFold(payload.Tool, "delegate") {
			b.handleParentDelegateToolCallStarted(payload)
			return
		}
		rawArgs := cloneToolArguments(payload.Arguments)
		toolName := normalizeToolName(payload.Tool)
		tc := &toolCallSegment{
			tool:                     toolName,
			args:                     summarizeArgs(payload.Tool, payload.Arguments),
			callID:                   payload.CallID,
			collapsed:                true,
			rawArgs:                  rawArgs,
			writeTargetExistedBefore: payload.WriteTargetExistedBefore,
		}
		tc.preview = output.BuildToolPreview(tc.tool, rawArgs, "", tc.writeTargetExistedBefore)
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
		if strings.EqualFold(payload.Tool, "display_file") {
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
	b.appendLine(formatStopReasonEvent(event))
}

func (b *contentBuffer) appendUserInputEvent(event output.Event) {
	if payload, ok := event.Payload.(output.UserInputEvent); ok && strings.TrimSpace(payload.Content) != "" {
		idx := len(b.segments)
		b.segments = append(b.segments, contentSegment{kind: segmentUserMarkdown, text: payload.Content, renderDirty: true})
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
	b.segments = append(b.segments, contentSegment{kind: segmentUserMarkdown, text: text, renderDirty: true})
	b.collapseState[idx] = false
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
		td.preview = output.BuildToolPreview(td.tool, td.rawArgs, payload.Result, td.writeTargetExistedBefore)
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
