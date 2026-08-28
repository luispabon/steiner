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
	return isSpecializedDelegateTool(tool)
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
			active:    payload.CallID != "",
			startTime: nanoNow(),
			rawArgs:   rawArgs,
		}
		tc.preview = output.BuildToolPreview(tc.tool, rawArgs, "")
		if tc.preview.Kind != output.ToolPreviewKindPlain {
			tc.bodyKind = previewBodyKind(tc.tool, tc.preview)
		}
		if b.appendAdjacentToolCall(tc) {
			b.registerActiveToolCall(len(b.segments)-1, tc)
			return
		}
		b.segments = append(b.segments, contentSegment{kind: segmentToolCall, toolData: tc, renderDirty: true})
		b.registerActiveToolCall(len(b.segments)-1, tc)
		return
	}
	b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
}

// applyFinishedToolCallToDelegation handles a ToolCallFinishedEvent for a
// segmentDelegation or segmentDelegationGroup segment. Returns true if the
// segment matched and was handled (caller should stop scanning).
func (b *contentBuffer) applyFinishedToolCallToDelegation(idx int, payload output.ToolCallFinishedEvent) bool {
	seg := &b.segments[idx]
	switch seg.kind {
	case segmentDelegation:
		dd := seg.delegData
		if dd == nil || dd.parentCallID == "" || !callIDsMatch(dd.parentCallID, payload.CallID) {
			return false
		}
		if dd.agentID == "" && payload.Error != "" {
			b.removeFromPendingDelegateParents(dd)
			dd.status = "failed"
			seg.renderDirty = true
			b.gen++
		}
		return true
	case segmentDelegationGroup:
		group := seg.delegGroupData
		if group == nil {
			return false
		}
		for j := len(group.entries) - 1; j >= 0; j-- {
			dd := group.entries[j]
			if dd == nil || dd.parentCallID == "" || !callIDsMatch(dd.parentCallID, payload.CallID) {
				continue
			}
			if dd.agentID == "" && payload.Error != "" {
				b.removeFromPendingDelegateParents(dd)
				dd.status = "failed"
				seg.renderDirty = true
				b.gen++
			}
			return true
		}
		return false
	default:
		return false
	}
}

func (b *contentBuffer) appendToolCallFinishedEvent(event output.Event) {
	b.finishStreaming()
	if payload, ok := event.Payload.(output.ToolCallFinishedEvent); ok {
		if shouldSkipToolEvent(payload.Tool) {
			return
		}
		if !isDelegateOrSpecialized(payload.Tool) && b.applyFinishedRegularToolCall(payload) {
			return
		}
		for i := len(b.segments) - 1; i >= 0; i-- {
			if b.applyFinishedToolCallToDelegation(i, payload) {
				return
			}
		}
		return
	}
	b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentTool)
}

func (b *contentBuffer) applyFinishedRegularToolCall(payload output.ToolCallFinishedEvent) bool {
	if loc, active := b.activeToolCalls[payload.CallID]; active {
		delete(b.activeToolCalls, payload.CallID)
		if loc.td != nil && loc.td.callID == payload.CallID && loc.seg >= 0 && loc.seg < len(b.segments) {
			b.applyFinishedToolCallResult(&b.segments[loc.seg], loc.td, payload)
			return true
		}
	}
	for i := len(b.segments) - 1; i >= 0; i-- {
		if b.applyFinishedRegularToolCallSegment(i, payload) {
			return true
		}
	}
	return false
}

func (b *contentBuffer) applyFinishedRegularToolCallSegment(i int, payload output.ToolCallFinishedEvent) bool {
	switch b.segments[i].kind {
	case segmentToolCall:
		td := b.segments[i].toolData
		if td == nil || !callIDsMatch(td.callID, payload.CallID) {
			return false
		}
		b.applyFinishedToolCallResult(&b.segments[i], td, payload)
		delete(b.activeToolCalls, payload.CallID)
		return true
	case segmentToolCallGroup:
		group := b.segments[i].toolGroupData
		if group == nil {
			return false
		}
		for j := len(group.entries) - 1; j >= 0; j-- {
			td := group.entries[j]
			if td == nil || !callIDsMatch(td.callID, payload.CallID) {
				continue
			}
			b.applyFinishedToolCallResult(&b.segments[i], td, payload)
			delete(b.activeToolCalls, payload.CallID)
			return true
		}
	}
	return false
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

		if len(payload.Images) > 0 {
			images := make([]agent.ImageBlock, len(payload.Images))
			for i, img := range payload.Images {
				images[i] = agent.ImageBlock{
					ID:        img.ID,
					FilePath:  img.FilePath,
					MediaType: img.MediaType,
					Data:      img.Data,
					Width:     img.Width,
					Height:    img.Height,
					SizeBytes: img.SizeBytes,
				}
			}
			b.AppendImagesAttached(images, b.workingDir, b.homeDir)
		}
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
			b.gen++
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
	b.activeToolCalls = nil
	b.pendingDelegateParents = nil
	b.pendingDelegationStarts = nil
	b.activeAdvisorSegment = 0
	// Invalidate render caches.
	b.stringCacheWidth = 0
	b.stringCacheRendered = ""
	b.gen = 0
	b.prefixCacheSet = false
	b.prefixCacheRendered = ""
	b.prefixCacheLastKind = 0
	b.prefixCacheLen = 0
	b.prefixCacheWidth = 0
	b.prefixCacheShowThinking = false
	b.prefixCacheGen = 0
}

func (b *contentBuffer) registerActiveToolCall(seg int, td *toolCallSegment) {
	if td == nil || !td.active || td.callID == "" {
		return
	}
	if b.activeToolCalls == nil {
		b.activeToolCalls = make(map[string]toolCallLocator)
	}
	b.activeToolCalls[td.callID] = toolCallLocator{seg: seg, td: td}
}

func (b *contentBuffer) HasActiveToolCalls() bool {
	return len(b.activeToolCalls) > 0
}

func (b *contentBuffer) AdvanceToolCallSpinners() {
	if len(spinnerFrames) == 0 {
		return
	}
	for _, loc := range b.activeToolCalls {
		if loc.td == nil || loc.seg < 0 || loc.seg >= len(b.segments) {
			continue
		}
		loc.td.spinnerFrame = (loc.td.spinnerFrame + 1) % len(spinnerFrames)
		b.segments[loc.seg].renderDirty = true
		b.gen++
	}
}

// ResetAdvisorSegment clears the active advisor segment flag.
// Called on run teardown to prevent stale flag from persisting across runs.
func (b *contentBuffer) ResetAdvisorSegment() {
	b.activeAdvisorSegment = 0
}

func (b *contentBuffer) appendAdjacentToolCall(tc *toolCallSegment) bool {
	if tc == nil || len(b.segments) == 0 {
		return false
	}
	last := &b.segments[len(b.segments)-1]
	switch last.kind {
	case segmentToolCall:
		if last.toolData == nil {
			return false
		}
		last.toolGroupData = &toolCallGroupSegment{
			tool:    tc.tool,
			mixed:   last.toolData.tool != tc.tool,
			entries: []*toolCallSegment{last.toolData, tc},
		}
		last.toolData = nil
		last.kind = segmentToolCallGroup
		last.renderDirty = true
		b.gen++
		return true
	case segmentToolCallGroup:
		if last.toolGroupData == nil {
			return false
		}
		last.toolGroupData.mixed = last.toolGroupData.mixed || last.toolGroupData.tool != tc.tool
		last.toolGroupData.entries = append(last.toolGroupData.entries, tc)
		last.renderDirty = true
		b.gen++
		return true
	default:
		return false
	}
}

func (b *contentBuffer) applyFinishedToolCallResult(seg *contentSegment, td *toolCallSegment, payload output.ToolCallFinishedEvent) {
	td.active = false
	td.elapsed = formatElapsed(td.startTime, nanoNow())
	td.body = payload.Result
	td.hasError = payload.Error != ""
	td.meta = "✓"
	if td.hasError {
		td.meta = "✗"
	}
	if payload.Preview.Kind != "" && payload.Preview.Kind != output.ToolPreviewKindPlain {
		td.preview = payload.Preview
	} else {
		td.preview = output.BuildToolPreview(td.tool, td.rawArgs, payload.Result)
	}
	if td.tool == "mutate" && td.preview.HunksFailed > 0 {
		td.hasError = true
		td.meta = "✗"
	}
	if td.preview.Kind != output.ToolPreviewKindPlain {
		td.bodyKind = previewBodyKind(td.tool, td.preview)
	} else {
		td.bodyKind = inferBodyKind(td.tool, payload.Result)
	}
	seg.renderDirty = true
	b.gen++
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
