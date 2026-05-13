package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (b *contentBuffer) renderApprovalPillSegment(segment contentSegment, width int) string {
	if segment.approvalData == nil {
		return ""
	}
	return b.renderApprovalPill(segment.approvalData, width)
}

func (b *contentBuffer) renderCompactionBannerSegment(segment contentSegment, width int) string {
	if segment.compactionData == nil {
		return ""
	}
	return b.renderCompactionBanner(segment.compactionData, width)
}

func (b *contentBuffer) renderApprovalPill(ad *approvalPillData, width int) string {
	const indent = "   "
	innerWidth := width - len(indent)
	if innerWidth < 10 {
		innerWidth = 10
	}

	label := "approval required"
	if ad.tool != "" {
		label = ad.tool
		if ad.mode != "" {
			label += " · " + ad.mode
		}
	}

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

	accentSoft := lipgloss.Color(theme.AccentSoft)
	bgElev2C := lipgloss.Color(theme.BgElev2)
	fgMuteC := lipgloss.Color(theme.FgMute)
	fgDimC := lipgloss.Color(theme.FgDim)
	accentC := lipgloss.Color(theme.AccentAmber)

	btnApprove := lipgloss.NewStyle().Background(accentSoft).Foreground(fgMuteC).Render("[y]") +
		lipgloss.NewStyle().Background(accentSoft).Foreground(accentC).Render(" approve")
	btnDeny := lipgloss.NewStyle().Background(bgElev2C).Foreground(fgMuteC).Render("[n]") +
		lipgloss.NewStyle().Background(bgElev2C).Foreground(fgDimC).Render(" deny")
	btnAlways := lipgloss.NewStyle().Background(bgElev2C).Foreground(fgMuteC).Render("[a]") +
		lipgloss.NewStyle().Background(bgElev2C).Foreground(fgDimC).Render(" always")
	buttons := btnApprove + " " + btnDeny + " " + btnAlways
	buttonsW := lipgloss.Width(buttons)

	bgW := innerWidth - 1
	qW := bgW - 2 - buttonsW
	if qW < 0 {
		qW = 0
	}

	question := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Foreground(lipgloss.Color(theme.Fg)).
		Width(qW).Render(label)

	bgContent := " " + question + " " + buttons
	bgRow := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Width(bgW).Render(bgContent)

	return indent + bar + bgRow + "\n"
}

func (b *contentBuffer) renderCompactionBanner(cd *compactionBannerData, width int) string {
	if cd.finished {
		var body string
		switch {
		case cd.msgCount > 0:
			body = fmt.Sprintf("context compacted · %d messages summarized into 1", cd.msgCount)
		case cd.summary != "":
			body = "context compacted · " + cd.summary
		default:
			body = "context compacted"
		}
		return b.styles.ThinkingBar.Render("▼ "+body) + "\n"
	}

	if width < 10 {
		width = 10
	}
	bar := b.styles.Warn.Render("│")
	bgW := width - 1

	compacting := b.styles.Warn.Render(" Compacting")
	subtitle := b.styles.FgDim.Render(" " + cd.subtitle + " ")

	pctStr := ""
	if cd.pct > 0 {
		pctStr = fmt.Sprintf("%d%% ", cd.pct)
	}
	pctLabel := b.styles.FgDim.Render(pctStr)

	fixedW := lipgloss.Width(compacting) + lipgloss.Width(subtitle) + lipgloss.Width(pctLabel)
	barW := bgW - fixedW
	if barW < 0 {
		barW = 0
	}

	var filled int
	if cd.progress > 0 {
		filled = int(float64(barW) * cd.progress)
	} else if barW > 0 {
		filled = b.tickCount % (barW + 1)
	}
	if filled > barW {
		filled = barW
	}
	filledBar := b.styles.Warn.Render(strings.Repeat("█", filled))
	emptyBar := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev2)).Render(strings.Repeat(" ", barW-filled))
	progressBar := filledBar + emptyBar

	rowContent := compacting + subtitle + progressBar + pctLabel
	row := lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Width(bgW).Render(rowContent)

	return bar + row + "\n"
}

// renderDelegationSegment renders a delegation block in one of three states:
// active (spinner + elapsed), complete (compact result), or failed (error style).
func (b *contentBuffer) renderDelegationSegment(segment contentSegment) string {
	dd := segment.delegData
	if dd == nil {
		return ""
	}
	muted := b.styles.FgMute
	switch dd.status {
	case "active":
		frame := spinnerFrames[dd.spinnerFrame%len(spinnerFrames)]
		elapsedStr := ""
		if dd.startTime > 0 {
			elapsedStr = " " + formatElapsed(dd.startTime, nanoNow())
		}
		header := muted.Render("⟩ delegate " + dd.agentID + elapsedStr + " " + frame)
		if dd.taskPreview != "" {
			return header + "\n" + muted.Render("  "+dd.taskPreview) + "\n"
		}
		return header + "\n"
	case "complete":
		meta := pluralTurns(dd.turnCount)
		if dd.tokenCount > 0 {
			meta += fmt.Sprintf(", %d tokens", dd.tokenCount)
		}
		if dd.elapsed != "" {
			meta += ", " + dd.elapsed
		}
		status := dd.resultStatus
		if status == "" {
			status = "complete"
		}
		line := fmt.Sprintf("✓ delegate %s — %s (%s)", dd.agentID, status, meta)
		rendered := b.styles.SuccessStyle.Render(line) + "\n"
		if dd.output != "" {
			if dd.collapsed {
				rendered += muted.Render("  [output hidden — ctrl+x to expand]") + "\n"
			} else {
				rendered += muted.Render("  "+dd.output) + "\n"
			}
		}
		return rendered
	case "failed":
		line := "✗ delegate " + dd.agentID + " — failed"
		if dd.elapsed != "" {
			line += " (" + dd.elapsed + ")"
		}
		return b.styles.ErrorStyle.Render(line) + "\n"
	default:
		return muted.Render("delegate "+dd.agentID) + "\n"
	}
}
