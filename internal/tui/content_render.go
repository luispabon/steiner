package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// isUserSegment reports whether kind is a user-prompt segment kind.
func isUserSegment(kind contentSegmentKind) bool {
	return kind == segmentUser || kind == segmentUserMarkdown
}

func (b *contentBuffer) String(width int) string {
	// Check if we can return cached result.
	isBufferDirty := b.checkBufferDirty()
	if !isBufferDirty && b.stringCacheWidth == width && b.stringCacheRendered != "" {
		return b.stringCacheRendered
	}

	b.segmentHeights = make([]int, len(b.segments))
	parts := make([]string, 0, len(b.segments)+2)
	kinds := make([]contentSegmentKind, 0, len(b.segments)+2)
	for i := range b.segments {
		if b.skipHiddenSegment(i) {
			continue
		}
		b.processSegment(i, width, &parts, &kinds)
	}
	if preview := b.inProgressPreview(width); preview != "" {
		parts = append(parts, strings.TrimRight(preview, "\n"))
		kinds = append(kinds, contentSegmentKind(-1))
	}

	result := b.fillEmptyLines(joinWithUserMargin(parts, kinds), width)
	b.stringCacheWidth = width
	b.stringCacheRendered = result
	return result
}

// processSegment renders a single non-hidden segment, updating the per-segment
// render cache and appending to parts/kinds. Extracted from String to keep the
// outer loop readable; stays inlinable to preserve the per-frame hot path.
func (b *contentBuffer) processSegment(i, width int, parts *[]string, kinds *[]contentSegmentKind) {
	seg := &b.segments[i]
	if seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished {
		seg.renderDirty = true
	}
	if seg.kind == segmentDelegation && seg.delegData != nil && seg.delegData.status == "active" {
		seg.renderDirty = true
	}
	if !seg.renderDirty && seg.cachedRenderWidth == width && seg.cachedRender != "" {
		stripped := strings.TrimRight(seg.cachedRender, "\n")
		b.segmentHeights[i] = strings.Count(stripped, "\n") + 1
		if stripped != "" {
			*parts = append(*parts, stripped)
			*kinds = append(*kinds, seg.kind)
		}
		return
	}
	rendered := b.renderSegment(*seg, width)
	rendered = strings.TrimRight(rendered, "\n")
	seg.cachedRender = rendered
	seg.cachedRenderWidth = width
	seg.renderDirty = false
	b.segmentHeights[i] = strings.Count(rendered, "\n") + 1
	if rendered != "" {
		*parts = append(*parts, rendered)
		*kinds = append(*kinds, seg.kind)
	}
}

// checkBufferDirty checks if any condition requires a full re-render of the buffer.
func (b *contentBuffer) checkBufferDirty() bool {
	// If streaming with new content, invalidate cache.
	if b.streaming && b.streamBuffer != "" {
		return true
	}

	// Any segment is explicitly dirty.
	for i := range b.segments {
		if b.segments[i].renderDirty {
			return true
		}
	}
	// Active delegation or compaction forces per-frame rebuild.
	if b.compaction.Active() {
		return true
	}
	for _, seg := range b.segments {
		if seg.kind == segmentDelegation && seg.delegData != nil && seg.delegData.status == "active" {
			return true
		}
		if seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished {
			return true
		}
	}
	// showThinking toggle changes visibility.
	// (This would need tracking of previous showThinking value if we want to be more precise.)
	return false
}

// joinWithUserMargin joins parts with "\n", inserting a blank line ("\n\n")
// above and below any user-prompt segment. Parts whose kind entry is -1 are
// preview sentinels and only receive single-newline separators.
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
	if isUserSegment(prev) || isUserSegment(next) {
		return "\n\n"
	}
	return "\n"
}

func (b *contentBuffer) skipHiddenSegment(index int) bool {
	seg := &b.segments[index]
	if seg.kind != segmentThinkingBlock || b.showThinking {
		return false
	}
	b.segmentHeights[index] = 0
	// Clear renderDirty on skipped segments so hidden-thinking toggles
	// don't perpetually dirty the buffer cache.
	seg.renderDirty = false
	return true
}

func (b *contentBuffer) fillEmptyLines(s string, width int) string {
	if width < 1 {
		width = 1
	}
	var bgLine string
	if b.fillEmptyLinesCacheWidth == width && b.fillEmptyLinesCacheBgLine != "" {
		bgLine = b.fillEmptyLinesCacheBgLine
	} else {
		bgLine = lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Render(strings.Repeat(" ", width))
		b.fillEmptyLinesCacheWidth = width
		b.fillEmptyLinesCacheBgLine = bgLine
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = bgLine
		}
	}
	return strings.Join(lines, "\n")
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
		return theme.WithBg(b.renderThinkingBlockSegment(segment, width), lipgloss.Color(theme.BgElev))
	case segmentApprovalPill:
		return b.renderApprovalPillSegment(segment, width)
	case segmentCompactionBanner:
		return theme.WithBg(b.renderCompactionBannerSegment(segment, width), lipgloss.Color(theme.BgElev))
	case segmentSeparator:
		return b.renderSeparatorSegment(segment, width)
	case segmentInterrupted:
		return theme.WithBg(b.styles.FgMute.Render("interrupted")+"\n\n", lipgloss.Color(theme.BgElev))
	case segmentDelegation:
		return b.renderDelegationSegment(segment, width)
	case segmentStatus:
		return theme.WithBg(b.renderStatusSegment(segment, width), lipgloss.Color(theme.BgElev))
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
	cursor := ""
	if b.tickCount%2 == 0 {
		cursor = "█"
	}
	wrapped := ansi.Hardwrap(preview+cursor, max(1, width), true)
	return b.styles.AssistantProse.Render(wrapped) + "\n"
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
