package tui

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
)

func (b *contentBuffer) renderDelegationHeader(dd *delegationDisplayState, width int) string {
	_, statusWidth := b.renderDelegationHeaderStatus(dd)
	meta := b.renderDelegationHeaderMeta(dd)
	metaWidth := lipgloss.Width(meta)

	disclosure := "▾"
	if dd.collapsed {
		disclosure = "▸"
	}

	left := disclosure + " " + b.renderDelegationHeaderIdentity(dd)

	gap := 0
	if metaWidth > 0 {
		gap = 2
	}
	leftWidth := lipgloss.Width(left)
	operationWidth := width - leftWidth - statusWidth - metaWidth - gap
	if operationWidth < 0 {
		operationWidth = 0
	}

	operation := b.renderDelegationHeaderOperation(dd, operationWidth)
	header := left
	if operation != "" {
		header += " " + operation
	}
	if meta != "" {
		padding := width - lipgloss.Width(header) - metaWidth
		if padding < 1 {
			padding = 1
		}
		header += strings.Repeat(" ", padding) + meta
	}
	return header
}

func (b *contentBuffer) renderDelegationHeaderIdentity(dd *delegationDisplayState) string {
	label, labelStyle := b.delegationHeaderLabel(dd)
	left := labelStyle.Render(label)
	switch {
	case dd.isFollowUp && dd.followUpAgentID != "":
		left += b.styles.FgDim.Render(" - follow up " + dd.followUpAgentID)
	case delegationHeaderAgentID(dd) != "":
		left += " " + b.styles.FgDim.Render(delegationHeaderAgentID(dd))
	}
	return left
}

func (b *contentBuffer) delegationHeaderLabel(dd *delegationDisplayState) (string, lipgloss.Style) {
	if dd.isAdvisor {
		tagStyle, _ := b.delegationStyles("advisor")
		return "advisor", tagStyle
	}
	label := dd.effectiveTypeLabel()
	tagStyle, _ := b.delegationStyles(label)
	return label, tagStyle
}

func delegationHeaderAgentID(dd *delegationDisplayState) string {
	if dd == nil || dd.isAdvisor {
		return ""
	}
	agentID := strings.TrimSpace(dd.agentID)
	if agentID == "" {
		return "pending"
	}
	return agentID
}

func renderStyledBox(content string, borderColor color.Color, bgColor color.Color, width int) string {
	return boxStyle(borderColor, bgColor, width, true).Render(content)
}

// boxStyle returns the style renderStyledBox draws with. withTopBottom also
// draws the top and bottom borders; otherwise only the left and right sides are
// drawn.
func boxStyle(borderColor, bgColor color.Color, width int, withTopBottom bool) lipgloss.Style {
	style := lipgloss.NewStyle().
		Background(bgColor).
		Padding(0, 1)
	if withTopBottom {
		style = style.Border(lipgloss.NormalBorder())
	} else {
		style = style.Border(lipgloss.NormalBorder(), false, true, false, true)
	}
	return style.
		BorderForeground(borderColor).
		Width(max(1, width))
}

// delegationStyles returns both the tag and border lipgloss styles for the given
// agent type label, performing the case-normalisation once for both callers.
func (b *contentBuffer) delegationStyles(toolLabel string) (tag lipgloss.Style, border lipgloss.Style) {
	key := strings.ToLower(strings.TrimSpace(toolLabel))
	return styleByKey(b.styles.DelegateTagStyles, key, b.styles.ToolTagDefault), styleByKey(b.styles.DelegateBorderStyles, key, b.styles.DelegateBorderDefault)
}

func (b *contentBuffer) renderDelegationHeaderStatus(dd *delegationDisplayState) (string, int) {
	var styled string
	switch dd.status {
	case "active":
		if dd.queuedForSlot || dd.cacheWaiting {
			styled = b.styles.FgMute.Render("⧖")
		} else {
			frame := spinnerFrames[dd.spinnerFrame%len(spinnerFrames)]
			styled = b.styles.FgMute.Render(frame)
		}
	case "complete":
		styled = b.styles.SuccessStyle.Render("✓")
	case "budget_exhausted":
		styled = b.styles.Warn.Render("!")
	case "failed":
		styled = b.styles.ErrorStyle.Render("✗")
	default:
		styled = b.styles.FgMute.Render("•")
	}
	return styled, lipgloss.Width(styled)
}

func (b *contentBuffer) renderDelegationHeaderMeta(dd *delegationDisplayState) string {
	status, _ := b.renderDelegationHeaderStatus(dd)
	parts := []string{status}
	switch dd.status {
	case "active":
		if dd.cacheHitOK {
			parts = append(parts, b.styles.FgDim.Render("cache "+formatCacheHitRate(dd.cacheHitRate, dd.cacheHitOK)))
		}
		if dd.contextFillPct > 0 {
			parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("ctx: %d%%", int(math.Round(dd.contextFillPct)))))
		}
		if dd.outputTPS > 0 {
			parts = append(parts, b.styles.FgDim.Render(formatTPS(dd.outputTPS)))
		}
		if modelEffort := delegationModelEffort(dd); modelEffort != "" {
			parts = append(parts, b.styles.FgDim.Render(modelEffort))
		}
		if !dd.queuedForSlot {
			if dd.cacheWaiting {
				parts = append(parts, b.styles.FgDim.Render(formatCountdown(dd.cacheWaitDeadline, nanoNow())))
			} else if dd.startTime > 0 {
				parts = append(parts, b.styles.FgDim.Render(formatElapsed(dd.startTime, nanoNow())))
			}
		}
		if n := b.stalledMinutes(dd, timeNow()); n > 0 {
			parts = append(parts, b.styles.Warn.Render(fmt.Sprintf("stalled %dm", n)))
		}
	case "complete":
		metaParts := delegationCompleteMeta(dd)
		parts = append(parts, b.renderStyledDelegationMeta(metaParts))
	case "budget_exhausted":
		metaParts := delegationBudgetMeta(dd)
		parts = append(parts, b.renderStyledDelegationMeta(metaParts))
	case "failed":
		metaParts := delegationFailedMeta(dd)
		parts = append(parts, b.renderStyledDelegationMeta(metaParts))
	}
	if dd.status == "active" && len(parts) > 1 {
		return parts[0] + " " + strings.Join(parts[1:], " · ")
	}
	return strings.Join(parts, " ")
}

func (b *contentBuffer) renderStyledDelegationMeta(metaParts []string) string {
	if len(metaParts) == 0 {
		return ""
	}
	var styled []string
	for _, part := range metaParts {
		styled = append(styled, b.styles.FgDim.Render(part))
	}
	return strings.Join(styled, " · ")
}

func delegationModelEffort(dd *delegationDisplayState) string {
	if strings.TrimSpace(dd.modelName) == "" {
		return ""
	}
	return formatModelEffort(dd.modelName, dd.reasoning)
}

func delegationCompleteMeta(dd *delegationDisplayState) []string {
	status := strings.TrimSpace(dd.resultStatus)
	if status == "" {
		status = "complete"
	}
	meta := []string{status}
	if modelEffort := delegationModelEffort(dd); modelEffort != "" {
		meta = append(meta, modelEffort)
	}
	if dd.cacheHitOK {
		meta = append(meta, "cache "+formatCacheHitRate(dd.cacheHitRate, dd.cacheHitOK))
	}
	if dd.elapsed != "" {
		meta = append(meta, dd.elapsed)
	}
	return meta
}

func delegationBudgetMeta(dd *delegationDisplayState) []string {
	meta := []string{"budget exhausted"}
	if modelEffort := delegationModelEffort(dd); modelEffort != "" {
		meta = append(meta, modelEffort)
	}
	if dd.advisorUse > 0 && dd.advisorMaxUses > 0 {
		meta = append(meta, fmt.Sprintf("%d/%d", dd.advisorUse, dd.advisorMaxUses))
	}
	return meta
}

func delegationFailedMeta(dd *delegationDisplayState) []string {
	meta := []string{"failed"}
	if modelEffort := delegationModelEffort(dd); modelEffort != "" {
		meta = append(meta, modelEffort)
	}
	if dd.elapsed != "" {
		meta = append(meta, dd.elapsed)
	}
	return meta
}

func (b *contentBuffer) renderDelegationHeaderOperation(dd *delegationDisplayState, width int) string {
	var operation string
	if dd.isAdvisor {
		operation = strings.TrimSpace(dd.advisorQuestion)
		if operation == "" {
			operation = strings.TrimSpace(dd.currentOperation)
		}
		if operation == "" {
			operation = "stronger-model steering"
		}
		if n := len(dd.advisorFiles); n > 0 {
			suffix := fmt.Sprintf(" · %d file", n)
			if n != 1 {
				suffix += "s"
			}
			operation = truncateRunes(operation, max(1, width-lipgloss.Width(suffix))) + suffix
		}
	} else {
		operation = strings.TrimSpace(dd.currentOperation)
		switch {
		case operation == "" && dd.queuedForSlot && dd.status == "active":
			operation = "Waiting for slot"
		case operation == "" && dd.cacheWaiting:
			operation = "waiting for cache warm-up…"
		case operation == "" && dd.status == "active":
			operation = strings.TrimSpace(dd.taskPreview)
		}
	}
	operation = output.SanitizeDisplayText(normalizeDelegationText(operation))
	if operation == "" {
		return ""
	}
	if width < 4 {
		return ""
	}
	return b.styles.FgMute.Render(truncateRunes(operation, width))
}
