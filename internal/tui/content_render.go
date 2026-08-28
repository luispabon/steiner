package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// isUserSegment reports whether kind is a user-prompt segment kind.
func isUserSegment(kind contentSegmentKind) bool {
	return kind == segmentUser || kind == segmentUserMarkdown
}

// isDelegationSegment reports whether kind is a delegation segment kind.
func isDelegationSegment(kind contentSegmentKind) bool {
	return kind == segmentDelegation || kind == segmentDelegationGroup
}

// segmentHasActiveDelegation reports whether seg renders at least one delegation
// with status "active" (needing a per-frame re-render for its spinner).
func segmentHasActiveDelegation(seg *contentSegment) bool {
	switch seg.kind {
	case segmentDelegation:
		return seg.delegData != nil && seg.delegData.status == "active"
	case segmentDelegationGroup:
		if seg.delegGroupData == nil {
			return false
		}
		for _, dd := range seg.delegGroupData.entries {
			if dd != nil && dd.status == "active" {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (b *contentBuffer) String(width int) string {
	// Check if we can return cached result.
	isBufferDirty := b.checkBufferDirty(width)
	if !isBufferDirty && b.stringCacheWidth == width && b.stringCacheRendered != "" {
		return b.stringCacheRendered
	}

	// Extend the segment-height slice in place; heights of the cached prefix are
	// retained from the previous render, so streaming frames do not reallocate
	// per segment.
	if len(b.segmentHeights) < len(b.segments) {
		b.segmentHeights = append(b.segmentHeights, make([]int, len(b.segments)-len(b.segmentHeights))...)
	}

	// Reuse the cached settled prefix when it is still valid, otherwise rebuild
	// it. The tail after the prefix is re-walked every dirty frame.
	start := 0
	prefix := ""
	var prefixLastKind contentSegmentKind
	if b.prefixCacheValid(width) {
		start = b.prefixCacheLen
		prefix = b.prefixCacheRendered
		prefixLastKind = b.prefixCacheLastKind
	} else {
		start, prefix, prefixLastKind = b.rebuildPrefix(width)
	}

	parts := make([]string, 0, 8)
	kinds := make([]contentSegmentKind, 0, 8)
	anyRerender := false
	for i := start; i < len(b.segments); i++ {
		if b.skipHiddenSegment(i) {
			continue
		}
		if b.processSegment(i, width, &parts, &kinds) {
			anyRerender = true
		}
	}

	// Fold the freshly settled tail into the prefix cache so the next dirty
	// frame only walks the genuinely changing tail (preview, spinners, live
	// segments). Anything that re-rendered this frame stays in the tail.
	segmentJoin := joinWithUserMargin(parts, kinds)
	if !anyRerender && start < len(b.segments) {
		b.foldPrefix(parts, kinds, prefix, prefixLastKind, width)
	}

	result := segmentJoin
	if prefix != "" && len(parts) > 0 {
		result = prefix + joinSeparator(prefixLastKind, kinds[0]) + segmentJoin
	} else if prefix != "" {
		result = prefix
	}
	if preview := b.inProgressPreview(width); preview != "" {
		result = appendStreamPreview(result, preview)
	}

	b.stringCacheWidth = width
	b.stringCacheRendered = result
	return result
}

// appendStreamPreview appends the trimmed streaming preview to result with a
// single-newline separator, matching the sentinel rule of joinWithUserMargin.
func appendStreamPreview(result, preview string) string {
	trimmed := strings.TrimRight(preview, "\n")
	if result == "" {
		return trimmed
	}
	return result + "\n" + trimmed
}

// prefixCacheValid reports whether the settled-prefix cache can be reused for
// the given width: no in-place mutation since it was built, the same width, and
// the same showThinking visibility.
func (b *contentBuffer) prefixCacheValid(width int) bool {
	return b.prefixCacheSet &&
		b.prefixCacheGen == b.gen &&
		b.prefixCacheWidth == width &&
		b.prefixCacheShowThinking == b.showThinking &&
		b.prefixCacheLen <= len(b.segments)
}

// foldPrefix extends the settled-prefix cache to cover the whole buffer after a
// dirty frame in which nothing in the tail re-rendered. parts/kinds must be the
// tail's segment parts (no preview sentinel).
func (b *contentBuffer) foldPrefix(parts []string, kinds []contentSegmentKind, prefix string, prefixLastKind contentSegmentKind, width int) {
	segmentJoin := joinWithUserMargin(parts, kinds)
	switch {
	case prefix != "" && len(parts) > 0:
		b.prefixCacheRendered = prefix + joinSeparator(prefixLastKind, kinds[0]) + segmentJoin
		b.prefixCacheLastKind = kinds[len(kinds)-1]
	case prefix != "":
		b.prefixCacheRendered = prefix
	default:
		b.prefixCacheRendered = segmentJoin
		b.prefixCacheLastKind = lastPartKind(kinds)
	}
	b.prefixCacheSet = true
	b.prefixCacheLen = len(b.segments)
	b.prefixCacheWidth = width
	b.prefixCacheShowThinking = b.showThinking
	b.prefixCacheGen = b.gen
}

// rebuildPrefix caches the joined render of every settled segment up to the
// first segment that needs re-rendering. Returns the boundary index, the joined
// prefix, and the kind of the last part in it.
func (b *contentBuffer) rebuildPrefix(width int) (boundary int, prefix string, prefixLastKind contentSegmentKind) {
	parts := make([]string, 0, 8)
	kinds := make([]contentSegmentKind, 0, 8)
	for i := range b.segments {
		if b.skipHiddenSegment(i) {
			continue
		}
		if b.segmentNeedsRender(&b.segments[i], width) {
			break
		}
		stripped := strings.TrimRight(b.segments[i].cachedRender, "\n")
		b.segmentHeights[i] = strings.Count(stripped, "\n") + 1
		if stripped != "" {
			parts = append(parts, stripped)
			kinds = append(kinds, b.segments[i].kind)
		}
		boundary = i + 1
	}
	b.prefixCacheSet = true
	b.prefixCacheLen = boundary
	b.prefixCacheWidth = width
	b.prefixCacheShowThinking = b.showThinking
	b.prefixCacheGen = b.gen
	b.prefixCacheRendered = joinWithUserMargin(parts, kinds)
	b.prefixCacheLastKind = lastPartKind(kinds)
	return boundary, b.prefixCacheRendered, b.prefixCacheLastKind
}

// segmentNeedsRender reports whether processSegment would re-render seg instead
// of reusing its cached render. Must stay in sync with processSegment.
func (b *contentBuffer) segmentNeedsRender(seg *contentSegment, width int) bool {
	if seg.renderDirty {
		return true
	}
	if seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished {
		return true
	}
	if segmentHasActiveDelegation(seg) {
		return true
	}
	return seg.cachedRenderWidth != width || seg.cachedRender == ""
}

// lastPartKind returns the kind of the last non-hidden part, or -1 when empty.
func lastPartKind(kinds []contentSegmentKind) contentSegmentKind {
	if len(kinds) == 0 {
		return contentSegmentKind(-1)
	}
	return kinds[len(kinds)-1]
}

// processSegment renders a single non-hidden segment, updating the per-segment
// render cache and appending to parts/kinds. Returns true when the segment was
// re-rendered rather than served from its per-segment cache (which prevents the
// settled prefix from folding over it). Extracted from String to keep the
// outer loop readable; stays inlinable to preserve the per-frame hot path.
func (b *contentBuffer) processSegment(i, width int, parts *[]string, kinds *[]contentSegmentKind) bool {
	seg := &b.segments[i]
	if seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished {
		seg.renderDirty = true
	}
	if segmentHasActiveDelegation(seg) {
		seg.renderDirty = true
	}
	if !seg.renderDirty && seg.cachedRenderWidth == width && seg.cachedRender != "" {
		stripped := strings.TrimRight(seg.cachedRender, "\n")
		b.segmentHeights[i] = strings.Count(stripped, "\n") + 1
		if stripped != "" {
			*parts = append(*parts, stripped)
			*kinds = append(*kinds, seg.kind)
		}
		return false
	}
	rendered := b.renderSegment(*seg, width)
	rendered = strings.TrimRight(rendered, "\n")
	seg.cachedRender = rendered
	seg.cachedRenderWidth = width
	seg.renderDirty = false
	seg.renderGen++
	b.segmentHeights[i] = strings.Count(rendered, "\n") + 1
	if rendered != "" {
		*parts = append(*parts, rendered)
		*kinds = append(*kinds, seg.kind)
	}
	return true
}

// checkBufferDirty checks if any condition requires a full re-render of the buffer.
func (b *contentBuffer) checkBufferDirty(width int) bool {
	// If streaming with new content, invalidate cache.
	if b.streaming && b.streamBuffer != "" {
		return true
	}

	// Any segment is explicitly dirty. When the settled-prefix cache is valid,
	// segments before the prefix are known clean, so only the tail is scanned.
	start := 0
	if b.prefixCacheValid(width) {
		start = b.prefixCacheLen
	}
	for i := start; i < len(b.segments); i++ {
		if b.segments[i].renderDirty {
			return true
		}
	}
	// Active delegation or compaction forces per-frame rebuild.
	if b.compaction.Active() {
		return true
	}
	for i := range b.segments {
		if segmentHasActiveDelegation(&b.segments[i]) {
			return true
		}
		if b.segments[i].kind == segmentCompactionBanner && b.segments[i].compactionData != nil && !b.segments[i].compactionData.finished {
			return true
		}
	}
	// showThinking toggle changes visibility.
	if b.showThinking != b.lastShowThinking {
		b.lastShowThinking = b.showThinking
		return true
	}
	return false
}

// joinWithUserMargin joins parts using separators selected by segment kind.
// Parts whose kind entry is -1 are preview sentinels and only receive
// single-newline separators.
func joinWithUserMargin(parts []string, kinds []contentSegmentKind) string {
	var sb strings.Builder
	lastKind := contentSegmentKind(-1)
	for i, p := range parts {
		if i > 0 {
			sep := joinSeparator(lastKind, kinds[i])
			sb.WriteString(sep)
		}
		sb.WriteString(p)
		if kinds[i] >= 0 {
			lastKind = kinds[i]
		}
	}
	return sb.String()
}

// joinSeparator returns the separator to place between two adjacent parts.
// Preview sentinels (kind < 0) collapse to a single newline so the streaming
// preview does not introduce blank-line gaps.
func joinSeparator(prev, next contentSegmentKind) string {
	if prev < 0 || next < 0 {
		return "\n"
	}
	if prev == next {
		return "\n"
	}
	if isUserSegment(prev) && isUserSegment(next) {
		return "\n"
	}
	if isDelegationSegment(prev) && isDelegationSegment(next) {
		return "\n"
	}
	return "\n\n"
}

// isSegmentHidden reports whether a segment is excluded from rendering, without
// mutating render state. Only hidden thinking blocks are ever excluded.
func (b *contentBuffer) isSegmentHidden(index int) bool {
	return b.segments[index].kind == segmentThinkingBlock && !b.showThinking
}

func (b *contentBuffer) skipHiddenSegment(index int) bool {
	if !b.isSegmentHidden(index) {
		return false
	}
	if index < len(b.segmentHeights) {
		b.segmentHeights[index] = 0
	}
	// Clear renderDirty on skipped segments so hidden-thinking toggles
	// don't perpetually dirty the buffer cache.
	b.segments[index].renderDirty = false
	return true
}

//nolint:gocyclo // dispatch-heavy segment renderer
func (b *contentBuffer) renderSegment(segment contentSegment, width int) string {
	switch segment.kind {
	case segmentToolCall:
		return b.renderToolCallSegment(segment, width)
	case segmentToolCallGroup:
		return b.renderToolCallGroup(segment.toolGroupData, width)
	case segmentAssistantMarkdown:
		return b.renderAssistantMarkdownSegment(segment, width)
	case segmentApproval:
		return b.styles.ApprovalHighlight.Render(segment.text) + "\n"
	case segmentTool:
		return b.styles.ToolBlock.Render(segment.text) + "\n"
	case segmentThinking:
		return b.styles.ThinkingBlock.Render(segment.text) + "\n"
	case segmentUser:
		return b.renderUserSegment(segment, width)
	case segmentUserMarkdown:
		return b.renderUserMarkdownSegment(segment, width)
	case segmentPendingSteer:
		return b.renderPendingSteerSegment(segment, width)
	default:
		return b.renderSupplementalSegment(segment, width)
	}
}

func (b *contentBuffer) renderSupplementalSegment(segment contentSegment, width int) string {
	switch segment.kind {
	case segmentThinkingBlock:
		return b.renderThinkingBlockSegment(segment, width)
	case segmentApprovalPill:
		return b.renderApprovalPillSegment(segment, width)
	case segmentCompactionBanner:
		return b.renderCompactionBannerSegment(segment, width)
	case segmentSeparator:
		return b.renderSeparatorSegment(segment, width)
	case segmentInterrupted:
		return b.styles.FgMute.Render("interrupted") + "\n"
	case segmentDelegation:
		return b.renderDelegationSegment(segment, width)
	case segmentDelegationGroup:
		return b.renderDelegationGroupSegment(segment, width)
	case segmentStatus:
		return b.renderStatusSegment(segment, width)
	case segmentImagesAttached:
		return b.renderImagesAttachedSegment(segment, width)
	default:
		return b.styles.AssistantProse.Render(segment.text) + "\n"
	}
}

func (b *contentBuffer) renderToolCallSegment(segment contentSegment, width int) string {
	return b.renderToolCall(segment.toolData, width)
}

func (b *contentBuffer) inProgressPreview(width int) string {
	preview := strings.TrimRight(b.streamBuffer, "\n")
	if strings.TrimSpace(preview) == "" {
		return ""
	}
	return b.styles.AssistantProse.Width(max(1, width)).Render(preview) + "\n"
}

func (b *contentBuffer) baseTextStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
}

// setGlamourStyleSheet rebuilds the glamour stylesheet and invalidates the cached
// renderer so the next markdown render picks up the new accent colour.
func (b *contentBuffer) setGlamourStyleSheet(accentHex string) {
	b.glamourStyleSheet = theme.BuildGlamourStyleSheet(accentHex)
	b.renderer = nil
	b.renderWidth = 0
}
