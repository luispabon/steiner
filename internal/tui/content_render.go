package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// isUserSegment reports whether kind is a user-prompt segment kind.
func isUserSegment(kind contentSegmentKind) bool {
	return kind == segmentUser || kind == segmentUserMarkdown
}

func (b *contentBuffer) String(width int) string {
	b.segmentHeights = make([]int, len(b.segments))
	parts := make([]string, 0, len(b.segments)+2)
	kinds := make([]contentSegmentKind, 0, len(b.segments)+2)
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
			stripped := strings.TrimRight(seg.cachedRender, "\n")
			b.segmentHeights[i] = strings.Count(stripped, "\n") + 1
			if stripped != "" {
				parts = append(parts, stripped)
				kinds = append(kinds, seg.kind)
			}
			continue
		}
		rendered := b.renderSegment(*seg, width)
		rendered = strings.TrimRight(rendered, "\n")
		seg.cachedRender = rendered
		seg.cachedRenderWidth = width
		seg.renderDirty = false
		b.segmentHeights[i] = strings.Count(rendered, "\n") + 1
		if rendered != "" {
			parts = append(parts, rendered)
			kinds = append(kinds, seg.kind)
		}
	}
	if preview := b.inProgressPreview(width); preview != "" {
		parts = append(parts, strings.TrimRight(preview, "\n"))
		kinds = append(kinds, contentSegmentKind(-1))
	}

	// Custom join: insert blank line above and below user segments.
	var sb strings.Builder
	lastKind := contentSegmentKind(-1)
	for i, p := range parts {
		if i > 0 {
			// Preview (sentinel -1) always joins with single newline.
			if lastKind < 0 || kinds[i] < 0 {
				sb.WriteString("\n")
			} else if isUserSegment(lastKind) || isUserSegment(kinds[i]) {
				sb.WriteString("\n\n")
			} else {
				sb.WriteString("\n")
			}
		}
		sb.WriteString(p)
		if kinds[i] >= 0 {
			lastKind = kinds[i]
		}
	}
	result := sb.String()
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
		return theme.WithBg(b.renderThinkingBlockSegment(segment, width), lipgloss.Color(theme.BgElev))
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
	case segmentStatus:
		return theme.WithBg(b.renderStatusSegment(segment, width), lipgloss.Color(theme.BgElev))
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
