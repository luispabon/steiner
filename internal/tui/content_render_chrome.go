package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const maxDelegationTranscriptRows = 40

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

// renderDelegationSegment renders a parent delegate tool call as one boxed block.
func (b *contentBuffer) renderDelegationSegment(segment contentSegment, width int) string {
	dd := segment.delegData
	if dd == nil {
		return ""
	}
	if width < 12 {
		width = 12
	}

	headerWidth := width - 4
	if headerWidth < 1 {
		headerWidth = 1
	}
	lines := b.renderDelegationBoxRows(dd, headerWidth)

	boxStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Padding(0, 1).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(theme.BorderSoft))

	boxWidth := width - 2
	if boxWidth < 1 {
		boxWidth = 1
	}
	box := boxStyle.Width(boxWidth).Render(strings.Join(lines, "\n")) + "\n"
	return box + b.renderDelegationHint(dd) + "\n"
}

func (b *contentBuffer) renderDelegationBoxRows(dd *delegationDisplayState, width int) []string {
	rows := b.delegationRows(dd)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.kind == delegationRowBorderTop || row.kind == delegationRowBorderBottom || row.kind == delegationRowHint {
			continue
		}
		switch row.kind {
		case delegationRowHeader:
			lines = append(lines, theme.WithBg(b.renderDelegationHeader(dd, width), lipgloss.Color(theme.BgElev)))
		case delegationRowPromptHeader:
			lines = append(lines, b.renderDelegationPromptHeader(dd))
		case delegationRowPromptBody:
			if dd.promptCollapsed {
				if preview := previewDelegationText(dd.promptText); preview != "" {
					lines = append(lines, b.styles.FgMute.Render(truncateRunes(preview, max(1, width-2))))
				}
				continue
			}
			lines = append(lines, b.renderDelegationPromptBody(dd, width)...)
		case delegationRowTranscript:
			lines = append(lines, b.renderDelegationTranscript(dd, width)...)
		case delegationRowOutput:
			lines = append(lines, b.renderDelegationOutput(dd, width)...)
		}
	}
	return lines
}

func (b *contentBuffer) renderDelegationHint(dd *delegationDisplayState) string {
	action := "expand"
	if !dd.collapsed {
		action = "collapse"
	}
	return b.styles.FgDim.Render("ctrl+x or click header to " + action)
}

func (b *contentBuffer) renderDelegationPromptHeader(dd *delegationDisplayState) string {
	disclosure := "▾"
	if dd.promptCollapsed {
		disclosure = "▸"
	}
	return b.styles.FgDim.Render(disclosure + " prompt")
}

func (b *contentBuffer) renderDelegationPromptBody(dd *delegationDisplayState, width int) []string {
	lines := b.wrapStyledDelegationLines(dd.promptText, width, b.styles.FgMute)
	if len(lines) == 0 {
		return nil
	}
	return lines
}

func (b *contentBuffer) renderDelegationHeader(dd *delegationDisplayState, width int) string {
	_, statusWidth := b.renderDelegationHeaderStatus(dd)
	meta := b.renderDelegationHeaderMeta(dd)
	metaWidth := lipgloss.Width(meta)

	agentID := strings.TrimSpace(dd.agentID)
	if agentID == "" {
		agentID = "pending"
	}

	disclosure := "▾"
	if dd.collapsed {
		disclosure = "▸"
	}

	left := disclosure + " " + b.styles.Accent.Bold(true).Render("delegate")
	if agentID != "" {
		left += " " + b.styles.FgDim.Render(agentID)
	}

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

func (b *contentBuffer) renderDelegationHeaderStatus(dd *delegationDisplayState) (string, int) {
	var styled string
	switch dd.status {
	case "active":
		frame := spinnerFrames[dd.spinnerFrame%len(spinnerFrames)]
		styled = b.styles.FgMute.Render(frame)
	case "complete":
		styled = b.styles.SuccessStyle.Render("✓")
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
		if dd.startTime > 0 {
			parts = append(parts, b.styles.FgDim.Render(formatElapsed(dd.startTime, nanoNow())))
		}
	case "complete":
		meta := []string{}
		statusText := strings.TrimSpace(dd.resultStatus)
		if statusText == "" {
			statusText = "complete"
		}
		meta = append(meta, statusText)
		if dd.turnCount > 0 {
			meta = append(meta, pluralTurns(dd.turnCount))
		}
		if dd.tokenCount > 0 {
			meta = append(meta, fmt.Sprintf("%d tokens", dd.tokenCount))
		}
		if dd.elapsed != "" {
			meta = append(meta, dd.elapsed)
		}
		parts = append(parts, b.styles.FgDim.Render(strings.Join(meta, " · ")))
	case "failed":
		label := "failed"
		if dd.elapsed != "" {
			label += " · " + dd.elapsed
		}
		parts = append(parts, b.styles.FgDim.Render(label))
	}
	return strings.Join(parts, " ")
}

func (b *contentBuffer) renderDelegationHeaderOperation(dd *delegationDisplayState, width int) string {
	operation := strings.TrimSpace(dd.currentOperation)
	if operation == "" && dd.status == "active" {
		operation = strings.TrimSpace(dd.taskPreview)
	}
	if operation == "" {
		return ""
	}
	if width < 4 {
		return ""
	}
	return b.styles.FgMute.Render(truncateRunes(operation, width))
}

func truncateRunes(text string, width int) string {
	if width < 1 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func (b *contentBuffer) renderDelegationTranscript(dd *delegationDisplayState, width int) []string {
	rows := make([]string, 0, len(dd.entries))
	for _, entry := range dd.entries {
		rows = append(rows, b.renderDelegationEntry(entry, width)...)
	}
	if len(rows) == 0 {
		return nil
	}
	if len(rows) > maxDelegationTranscriptRows {
		keep := maxDelegationTranscriptRows - 1
		if keep < 0 {
			keep = 0
		}
		rows = append([]string{b.styles.FgMute.Render("[old child events hidden]")}, rows[len(rows)-keep:]...)
	}
	return rows
}

func (b *contentBuffer) renderDelegationEntry(entry delegationTranscriptEntry, width int) []string {
	switch entry.kind {
	case delegationTranscriptEntryAssistant:
		return b.wrapStyledDelegationLines(entry.body, width, b.styles.FgMute)
	case delegationTranscriptEntryThinking:
		return b.renderDelegationThinkingEntry(entry, width)
	case delegationTranscriptEntryTool:
		return b.renderDelegationToolEntry(entry, width)
	default:
		return nil
	}
}

func (b *contentBuffer) renderDelegationThinkingEntry(entry delegationTranscriptEntry, width int) []string {
	style := b.thinkingTextStyle(entry.source)
	lines := b.wrapStyledDelegationLines(entry.body, max(1, width-2), style)
	if len(lines) == 0 {
		return nil
	}
	rows := make([]string, 0, len(lines)+1)
	rows = append(rows, style.Render("Thinking"))
	for _, line := range lines {
		rows = append(rows, style.Render("▎")+" "+line)
	}
	return rows
}

func (b *contentBuffer) renderDelegationToolEntry(entry delegationTranscriptEntry, width int) []string {
	toolName := strings.TrimSpace(entry.tool)
	if toolName == "" {
		toolName = "tool"
	}
	label := b.styles.Accent.Bold(true).Render(toolName)
	detail := strings.TrimSpace(entry.args)
	status := ""
	switch {
	case entry.hasError:
		status = b.styles.ErrorStyle.Render("error")
	case entry.status == "complete":
		status = b.styles.SuccessStyle.Render("✓")
	case entry.status == "running":
		status = b.styles.FgDim.Render("running")
	}

	line := label
	if detail != "" {
		line += " " + b.styles.FgDim.Render(truncateRunes(detail, max(1, width-lipgloss.Width(label)-1)))
	}
	if status != "" && lipgloss.Width(line)+1+lipgloss.Width(status) <= width {
		line += " " + status
	}
	return []string{line}
}

func (b *contentBuffer) wrapStyledDelegationLines(text string, width int, style lipgloss.Style) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if width < 1 {
		width = 1
	}
	parts := strings.Split(text, "\n")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimRight(part, " \t")
		if strings.TrimSpace(part) == "" {
			lines = append(lines, style.Render(""))
			continue
		}
		wrapped := ansi.Hardwrap(ansi.Wordwrap(part, width, ""), width, true)
		for _, line := range strings.Split(wrapped, "\n") {
			lines = append(lines, style.Render(line))
		}
	}
	return lines
}

func (b *contentBuffer) renderDelegationOutput(dd *delegationDisplayState, width int) []string {
	outputText := strings.TrimSpace(dd.output)
	if outputText == "" {
		return nil
	}
	if b.delegationOutputDuplicatesTranscript(dd, outputText) {
		return nil
	}
	lines := []string{b.styles.FgDim.Render("output")}
	lines = append(lines, b.wrapStyledDelegationLines(outputText, width, b.baseTextStyle())...)
	return lines
}

func (b *contentBuffer) delegationOutputDuplicatesTranscript(dd *delegationDisplayState, outputText string) bool {
	normalizedOutput := normalizeDelegationText(outputText)
	if normalizedOutput == "" {
		return true
	}
	for i := len(dd.entries) - 1; i >= 0; i-- {
		entry := dd.entries[i]
		if entry.kind != delegationTranscriptEntryAssistant {
			continue
		}
		if normalizeDelegationText(entry.body) == normalizedOutput {
			return true
		}
		break
	}
	return false
}
