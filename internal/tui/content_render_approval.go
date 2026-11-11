package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (b *contentBuffer) renderApprovalPillSegment(segment contentSegment, width int) string {
	if segment.approvalData == nil || segment.approvalData.cleared {
		return ""
	}
	return b.renderApprovalPill(segment.approvalData, width)
}

// renderApprovalPillButtons renders the pill's decision buttons in decision
// order and highlights the selected action the way the embedded tool-call tray
// highlights its buttons. The compact pill layout keeps its existing width.
func (b *contentBuffer) renderApprovalPillButtons(ad *approvalPillData) string {
	specs := []struct {
		label string
		key   string
		idx   int
	}{
		{label: "approve", key: "y", idx: 0},
		{label: "Allowed for session", key: "a", idx: 1},
		{label: "deny", key: "n", idx: 2},
	}
	bgElev2C := lipgloss.Color(theme.BgElev2)
	fgMuteC := lipgloss.Color(theme.FgMute)
	fgDimC := lipgloss.Color(theme.FgDim)
	accentFgC := lipgloss.Color(theme.Black)

	items := make([]string, 0, len(specs))
	for _, spec := range specs {
		selected := ad.selectedAction == spec.idx
		bg, keyFg, labelFg := bgElev2C, fgMuteC, fgDimC
		if selected {
			bg, keyFg, labelFg = b.styles.AccentColor, accentFgC, accentFgC
		}
		key := lipgloss.NewStyle().Background(bg).Foreground(keyFg).Bold(selected).Render("[" + spec.key + "]")
		label := lipgloss.NewStyle().Background(bg).Foreground(labelFg).Bold(selected).Render(" " + spec.label)
		items = append(items, key+label)
	}
	return strings.Join(items, " ")
}

func (b *contentBuffer) renderApprovalPill(ad *approvalPillData, width int) string {
	const indent = "   "
	innerWidth := width - len(indent)
	if innerWidth < 10 {
		innerWidth = 10
	}

	label := "approval required"
	if ad.kind == "mcp" && ad.server != "" {
		label = ad.server + " → " + ad.mcpToolName
	} else if ad.tool != "" {
		label = ad.tool
		if ad.mode != "" {
			label += " · " + ad.mode
		}
	}

	if ad.agentID != "" {
		label += " [agent: " + ad.agentID + "]"
	}
	if ad.queueDepth > 0 {
		label += " +" + fmt.Sprint(ad.queueDepth) + " waiting"
	}

	label = singleLineDisplayText(label)

	if ad.resolved {
		bar := b.styles.FgMute.Render("│")
		var statusStr string
		if ad.accepted {
			statusStr = b.styles.Added.Render("✓ approved")
		} else {
			statusStr = b.styles.Removed.Render("✗ denied")
		}
		statusW := lipgloss.Width(statusStr)
		qW := innerWidth - 3 - statusW
		if qW < 0 {
			qW = 0
		}
		question := lipgloss.NewStyle().Width(qW).Render(b.styles.FgDim.Render(label))
		return indent + bar + " " + question + " " + statusStr + "\n"
	}

	bar := b.styles.Accent.Render("│")

	buttons := b.renderApprovalPillButtons(ad)
	buttonsW := lipgloss.Width(buttons)

	bgW := innerWidth - 1
	qW := bgW - 2 - buttonsW
	if qW < 0 {
		qW = 0
	}

	question := lipgloss.NewStyle().
		Background(lipgloss.Color(b.styles.Palette.ContentBG)).
		Foreground(lipgloss.Color(theme.Fg)).
		Width(qW).Render(label)

	bgContent := " " + question + " " + buttons
	bgRow := lipgloss.NewStyle().Background(lipgloss.Color(b.styles.Palette.ContentBG)).Width(bgW).Render(bgContent)

	// MCP approvals render the handler-formatted ArgumentsPreview below the bar
	// as key:value lines.
	if ad.kind == "mcp" {
		return indent + bar + bgRow + "\n" + b.renderApprovalPreview(ad.preview, innerWidth)
	}
	return indent + bar + bgRow + "\n"
}
