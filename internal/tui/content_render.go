package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (b *contentBuffer) String(width int) string {
	b.segmentHeights = make([]int, len(b.segments))
	parts := make([]string, 0, len(b.segments)+2)
	for i := range b.segments {
		if b.skipHiddenSegment(i) {
			continue
		}
		seg := &b.segments[i]
		if seg.kind == segmentCompactionBanner && seg.compactionData != nil && !seg.compactionData.finished {
			seg.renderDirty = true
		}
		if seg.kind == segmentDelegation && seg.delegData != nil && seg.delegData.status == "active" {
			seg.renderDirty = true
		}
		if !seg.renderDirty && seg.cachedRenderWidth == width && seg.cachedRender != "" {
			b.segmentHeights[i] = strings.Count(seg.cachedRender, "\n")
			if seg.cachedRender != "" {
				parts = append(parts, seg.cachedRender)
			}
			continue
		}
		rendered := b.renderSegment(*seg, width)
		seg.cachedRender = rendered
		seg.cachedRenderWidth = width
		seg.renderDirty = false
		b.segmentHeights[i] = strings.Count(rendered, "\n")
		if rendered != "" {
			parts = append(parts, rendered)
		}
	}
	if preview := b.inProgressPreview(width); preview != "" {
		parts = append(parts, preview)
	}

	result := strings.Join(parts, "")
	return b.fillEmptyLines(result, width)
}

func (b *contentBuffer) skipHiddenSegment(index int) bool {
	seg := &b.segments[index]
	if seg.kind != segmentThinkingBlock || b.showThinking {
		return false
	}
	b.segmentHeights[index] = 0
	return true
}

func (b *contentBuffer) fillEmptyLines(s string, width int) string {
	if width < 1 {
		width = 1
	}
	bgLine := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Render(strings.Repeat(" ", width))
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if lipgloss.Width(line) == 0 {
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
	case segmentAssistantProse:
		return b.renderAssistantProseSegment(segment)
	case segmentApproval:
		return b.renderApprovalSegment(segment)
	case segmentTool:
		return b.renderToolSegment(segment)
	case segmentThinking:
		return b.renderThinkingSegment(segment)
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
		return theme.WithBg(b.renderThinkingBlockSegment(segment), lipgloss.Color(theme.BgElev))
	case segmentApprovalPill:
		return b.renderApprovalPillSegment(segment, width)
	case segmentCompactionBanner:
		return theme.WithBg(b.renderCompactionBannerSegment(segment, width), lipgloss.Color(theme.BgElev))
	case segmentSeparator:
		return b.renderSeparatorSegment(segment, width)
	case segmentInterrupted:
		return theme.WithBg(b.renderInterruptedSegment(), lipgloss.Color(theme.BgElev))
	case segmentDelegation:
		return b.renderDelegationSegment(segment, width)
	default:
		return b.renderDefaultSegment(segment)
	}
}

func (b *contentBuffer) renderToolCallSegment(segment contentSegment, width int) string {
	return b.renderToolCall(segment.toolData, width)
}

func (b *contentBuffer) renderApprovalSegment(segment contentSegment) string {
	return b.styles.ApprovalHighlight.Render(segment.text) + "\n"
}

func (b *contentBuffer) renderToolSegment(segment contentSegment) string {
	return b.styles.ToolBlock.Render(segment.text) + "\n"
}

func (b *contentBuffer) renderThinkingSegment(segment contentSegment) string {
	return b.styles.ThinkingBlock.Render(segment.text) + "\n"
}

func (b *contentBuffer) renderInterruptedSegment() string {
	return b.styles.FgMute.Render("interrupted") + "\n\n"
}

func (b *contentBuffer) renderDefaultSegment(segment contentSegment) string {
	return b.styles.AssistantProse.Render(segment.text) + "\n"
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
