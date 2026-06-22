package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const maxDelegationTranscriptRows = 40

func (b *contentBuffer) renderDelegationSegment(segment contentSegment, width int) string {
	dd := segment.delegData
	if dd == nil {
		return ""
	}
	if width < 12 {
		width = 12
	}

	lines := b.renderDelegationBoxRows(dd, width)

	_, borderStyle := b.delegationStyles(dd.toolLabel)
	box := renderStyledBox(strings.Join(lines, "\n"), borderStyle.GetForeground(), lipgloss.Color(theme.BgElev), width) + "\n"
	return box + b.renderDelegationHint(dd) + "\n"
}

func (b *contentBuffer) renderDelegationBoxRows(dd *delegationDisplayState, width int) []string {
	rows := b.delegationRows(dd, width)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.kind == delegationRowBorderTop || row.kind == delegationRowBorderBottom || row.kind == delegationRowHint {
			continue
		}
		if row.text != "" {
			lines = append(lines, row.text)
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
	lines := b.wrapStyledDelegationLines(dd.promptText, width, b.styles.FgMute.Italic(true))
	if len(lines) == 0 {
		return nil
	}
	// Apply viewport height cap if set (maxDelegationBodyLines > 0).
	if b.maxDelegationBodyLines > 0 && len(lines) > b.maxDelegationBodyLines {
		lines = lines[:b.maxDelegationBodyLines]
	}
	return lines
}

func (b *contentBuffer) renderDelegationHeader(dd *delegationDisplayState, width int) string {
	_, statusWidth := b.renderDelegationHeaderStatus(dd)
	meta := b.renderDelegationHeaderMeta(dd)
	metaWidth := lipgloss.Width(meta)

	disclosure := "▾"
	if dd.collapsed {
		disclosure = "▸"
	}

	left := disclosure + " " + b.renderDelegationHeaderIdentity(dd)

	if dd.status == "active" && dd.contextFillPct > 0 {
		left += " " + b.styles.FgDim.Render(fmt.Sprintf("ctx: %d%%", int(math.Round(dd.contextFillPct))))
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
	switch {
	case dd.isAdvisor:
		tagStyle, _ := b.delegationStyles("advisor")
		return "advisor", tagStyle
	case dd.toolLabel != "":
		tagStyle, _ := b.delegationStyles(dd.toolLabel)
		return dd.toolLabel, tagStyle
	default:
		return "delegate", b.styles.DelegateTagDefault
	}
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

func renderStyledBox(content string, borderColor lipgloss.TerminalColor, bgColor lipgloss.Color, width int) string {
	return lipgloss.NewStyle().
		Background(bgColor).
		Padding(0, 1).
		Border(lipgloss.NormalBorder()).
		BorderForeground(borderColor).
		Width(max(1, width-2)).
		Render(content)
}

// delegationStyles returns both the tag and border lipgloss styles for the given
// agent type label, performing the case-normalisation once for both callers.
func (b *contentBuffer) delegationStyles(toolLabel string) (tag lipgloss.Style, border lipgloss.Style) {
	key := strings.ToLower(strings.TrimSpace(toolLabel))
	return styleByKey(b.styles.DelegateTagStyles, key, b.styles.DelegateTagDefault), styleByKey(b.styles.DelegateBorderStyles, key, b.styles.DelegateBorderDefault)
}

func (b *contentBuffer) renderDelegationHeaderStatus(dd *delegationDisplayState) (string, int) {
	var styled string
	switch dd.status {
	case "active":
		frame := spinnerFrames[dd.spinnerFrame%len(spinnerFrames)]
		styled = b.styles.FgMute.Render(frame)
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
		if dd.startTime > 0 {
			parts = append(parts, b.styles.FgDim.Render(formatElapsed(dd.startTime, nanoNow())))
		}
	case "complete":
		parts = append(parts, b.styles.FgDim.Render(strings.Join(delegationCompleteMeta(dd), " · ")))
	case "budget_exhausted":
		parts = append(parts, b.styles.FgDim.Render(strings.Join(delegationBudgetMeta(dd), " · ")))
	case "failed":
		parts = append(parts, b.styles.FgDim.Render(strings.Join(delegationFailedMeta(dd), " · ")))
	}
	return strings.Join(parts, " ")
}

func delegationCompleteMeta(dd *delegationDisplayState) []string {
	status := strings.TrimSpace(dd.resultStatus)
	if status == "" {
		status = "complete"
	}
	meta := []string{status}
	if dd.turnCount > 0 {
		meta = append(meta, pluralTurns(dd.turnCount))
	}
	if dd.toolCallCount > 0 {
		meta = append(meta, pluralToolCalls(dd.toolCallCount))
	}
	if dd.tokenCount > 0 {
		meta = append(meta, fmt.Sprintf("%d tokens", dd.tokenCount))
	}
	if dd.elapsed != "" {
		meta = append(meta, dd.elapsed)
	}
	if dd.advisorUse > 0 && dd.advisorMaxUses > 0 {
		meta = append(meta, fmt.Sprintf("%d/%d", dd.advisorUse, dd.advisorMaxUses))
	}
	return meta
}

func delegationBudgetMeta(dd *delegationDisplayState) []string {
	meta := []string{"budget exhausted"}
	if dd.advisorUse > 0 && dd.advisorMaxUses > 0 {
		meta = append(meta, fmt.Sprintf("%d/%d", dd.advisorUse, dd.advisorMaxUses))
	}
	return meta
}

func delegationFailedMeta(dd *delegationDisplayState) []string {
	meta := []string{"failed"}
	if dd.elapsed != "" {
		meta = append(meta, dd.elapsed)
	}
	return meta
}

func (b *contentBuffer) renderDelegationHeaderOperation(dd *delegationDisplayState, width int) string {
	operation := strings.TrimSpace(dd.currentOperation)
	if operation == "" && dd.isAdvisor {
		operation = "stronger-model steering"
	}
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
	// Apply viewport height cap if set (maxDelegationBodyLines > 0).
	if b.maxDelegationBodyLines > 0 && len(rows) > b.maxDelegationBodyLines {
		rows = rows[len(rows)-b.maxDelegationBodyLines:]
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
	style := b.thinkingTextStyle()
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
	if dd.isAdvisor {
		return nil
	}
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

func (b *contentBuffer) renderDelegationFooterSeparator(width int) string {
	if width < 1 {
		width = 1
	}
	return b.styles.FgDim.Render(strings.Repeat("─", width))
}

func (b *contentBuffer) renderDelegationStatsRow(dd *delegationDisplayState) string {
	parts := delegationStatsParts(b, dd)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "    ")
}

func delegationStatsParts(b *contentBuffer, dd *delegationDisplayState) []string {
	parts := make([]string, 0, 7)
	if badge := renderModelBadge(b.styles, dd.modelName); badge != "" {
		parts = append(parts, badge)
	}
	if dd.isAdvisor && dd.advisorUse > 0 && dd.advisorMaxUses > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Use: %d/%d", dd.advisorUse, dd.advisorMaxUses)))
	}
	if dd.turnCount > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Turns: %d", dd.turnCount)))
	}
	if dd.toolCallCount > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Tool Calls: %d", dd.toolCallCount)))
	}
	if dd.tokenCount > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Tokens: %d", dd.tokenCount)))
	}
	if duration := delegationStatsDuration(dd); duration != "" {
		parts = append(parts, b.styles.FgDim.Render("Duration: "+duration))
	}
	if status := delegationStatsStatus(dd); status != "" {
		parts = append(parts, b.renderDelegationStatsStatus(status))
	}
	if ctx := delegationStatsContext(dd); ctx != "" {
		parts = append(parts, b.styles.FgDim.Render(ctx))
	}
	if dd.extMax > 0 {
		parts = append(parts, b.styles.FgDim.Render(fmt.Sprintf("Extension: %d/%d", dd.extCurrent, dd.extMax)))
	}
	return parts
}

func delegationStatsDuration(dd *delegationDisplayState) string {
	duration := strings.TrimSpace(dd.elapsed)
	if duration == "" && dd.status == "active" && dd.startTime > 0 {
		duration = formatElapsed(dd.startTime, nanoNow())
	}
	return duration
}

func delegationStatsStatus(dd *delegationDisplayState) string {
	status := strings.TrimSpace(dd.resultStatus)
	if status == "" {
		status = dd.status
	}
	return status
}

func delegationStatsContext(dd *delegationDisplayState) string {
	if dd.contextFillPct <= 0 {
		return ""
	}
	ctx := fmt.Sprintf("Ctx: %d%%", int(math.Round(dd.contextFillPct)))
	if dd.promptTokens > 0 && dd.contextWindow > 0 {
		ctx += fmt.Sprintf(" (%s / %s)", formatCompactCount(dd.promptTokens), formatCompactCount(dd.contextWindow))
	}
	return ctx
}

func (b *contentBuffer) renderDelegationStatsStatus(status string) string {
	label := b.styles.FgDim.Render("Status: ")
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "complete":
		return label + b.styles.SuccessStyle.Render(status)
	case "budget exhausted":
		return label + b.styles.Warn.Render(status)
	case "failed", "error":
		return label + b.styles.ErrorStyle.Render(status)
	default:
		return label + status
	}
}

func formatCompactCount(value int) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(value)/1_000_000)
	case value >= 10_000:
		return fmt.Sprintf("%dk", int(math.Round(float64(value)/1_000)))
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}
