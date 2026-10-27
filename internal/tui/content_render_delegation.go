package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
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

	_, borderStyle := b.delegationStyles(dd.effectiveTypeLabel())
	box := renderStyledBox(strings.Join(lines, "\n"), borderStyle.GetForeground(), lipgloss.Color(b.styles.Palette.ContentBG), width) + "\n"
	return box
}

// effectiveTypeLabel returns the specialized agent type to use for rendering
// (label text, tag color, border color), preferring toolLabel (parsed from
// the spawning tool call's "type" argument) and falling back to agentType
// (carried authoritatively on DelegationStartedEvent) when toolLabel could
// not be resolved — e.g. a follow-up whose original delegation record isn't
// in this transcript, such as one resumed from a session recorded before
// specialized delegate tools were consolidated into sub_agent.
func (dd *delegationDisplayState) effectiveTypeLabel() string {
	if dd.toolLabel != "" {
		return dd.toolLabel
	}
	return dd.agentType
}

func (b *contentBuffer) renderDelegationGroupSegment(segment contentSegment, width int) string {
	group := segment.delegGroupData
	if group == nil || len(group.entries) == 0 {
		return ""
	}

	if width < 12 {
		width = 12
	}

	parts := make([]string, 0, len(group.entries)*2)
	dividerWidth := width - groupBoxChrome
	if dividerWidth < 1 {
		dividerWidth = 1
	}

	for i, dd := range group.entries {
		lines := b.groupEntryLines(group, dd, width)
		parts = append(parts, strings.Join(lines, "\n"))
		if i < len(group.entries)-1 {
			parts = append(parts, b.renderToolCallDivider(dividerWidth))
		}
	}

	borderLabel := delegationGroupBorderLabel(group)
	_, borderStyle := b.delegationStyles(borderLabel)
	box := renderGroupBox(&group.box, strings.Join(parts, "\n"), borderStyle.GetForeground(), lipgloss.Color(b.styles.Palette.ContentBG), width)
	if delegationVisualGroupName(group) == "" {
		return box + "\n"
	}
	boxLines := strings.Split(box, "\n")
	boxLines[len(boxLines)-1] = b.renderDelegationGroupFooter(group, lipgloss.Width(boxLines[0])-2, borderStyle.GetForeground())
	return strings.Join(boxLines, "\n") + "\n"
}

// delegationGroupBorderLabel returns the shared toolLabel of every entry, or ""
// when they differ. "" resolves to DelegateBorderDefault via delegationStyles,
// matching the tool-call precedent where a mixed group falls back to the
// default border. Derived at render time, not stored, because toolLabel is
// filled in late by bindParentDelegateCall — an append-time mixed flag would
// be stale.
func delegationGroupBorderLabel(group *delegationGroupSegment) string {
	if group == nil || len(group.entries) == 0 {
		return ""
	}
	first := strings.ToLower(strings.TrimSpace(group.entries[0].effectiveTypeLabel()))
	for _, dd := range group.entries[1:] {
		if strings.ToLower(strings.TrimSpace(dd.effectiveTypeLabel())) != first {
			return ""
		}
	}
	return first
}

func (b *contentBuffer) renderDelegationBoxRows(dd *delegationDisplayState, width int) []string {
	rows := b.delegationContentRows(dd, width)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.text)
	}
	return lines
}

func (b *contentBuffer) renderDelegationPromptHeader(dd *delegationDisplayState) string {
	disclosure := "▾"
	if dd.promptCollapsed {
		disclosure = "▸"
	}
	return b.styles.FgDim.Render(disclosure + " prompt")
}

func (b *contentBuffer) renderDelegationPromptBody(dd *delegationDisplayState, width int) []string {
	if dd.briefObjective != "" {
		return b.renderDelegationBriefBody(dd, width)
	}
	lines := b.wrapStyledDelegationLines(output.SanitizeDisplayText(dd.promptText), width, b.styles.FgFaint.Italic(true))
	if len(lines) == 0 {
		return nil
	}
	// Apply viewport height cap if set (maxDelegationBodyLines > 0).
	if b.maxDelegationBodyLines > 0 && len(lines) > b.maxDelegationBodyLines {
		lines = lines[:b.maxDelegationBodyLines]
	}
	return lines
}

func (b *contentBuffer) renderDelegationSectionHeader(label string) string {
	return b.styles.FgDim.Render("▾ " + label)
}

func (b *contentBuffer) renderAdvisorQuestionBody(dd *delegationDisplayState, width int) []string {
	lines := b.wrapStyledDelegationLines(output.SanitizeDisplayText(dd.advisorQuestion), width, b.styles.FgMute.Italic(true))
	if b.maxDelegationBodyLines > 0 && len(lines) > b.maxDelegationBodyLines {
		lines = lines[:b.maxDelegationBodyLines]
	}
	return lines
}

func (b *contentBuffer) renderAdvisorFilesBody(dd *delegationDisplayState, width int) []string {
	return b.renderBulletList(dd.advisorFiles, width, b.styles.FgMute)
}

func (b *contentBuffer) renderAdvisorDenialWarning(dd *delegationDisplayState, width int) string {
	count := dd.advisorDenied
	if count <= 0 {
		return ""
	}
	var text string
	if count == 1 {
		text = "⚠ 1 further request denied — budget exhausted"
	} else {
		text = fmt.Sprintf("⚠ %d further requests denied — budget exhausted", count)
	}
	return b.styles.Warn.Render(truncateRunes(text, max(1, width)))
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
		return b.wrapStyledDelegationLines(output.SanitizeDisplayText(entry.body), width, b.styles.FgDim)
	case delegationTranscriptEntryThinking:
		return b.renderDelegationThinkingEntry(entry, width)
	case delegationTranscriptEntryTool:
		return b.renderDelegationToolEntry(entry, width)
	default:
		return nil
	}
}

func (b *contentBuffer) renderDelegationThinkingEntry(entry delegationTranscriptEntry, width int) []string {
	style := b.styles.FgFaint.Italic(true)
	lines := b.wrapStyledDelegationLines(output.SanitizeDisplayText(stripThinkingMarkers(entry.body)), max(1, width), style)
	if len(lines) == 0 {
		return nil
	}
	rows := make([]string, 0, len(lines))
	rows = append(rows, lines...)
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
		wrapped := style.Width(width).Render(part)
		lines = append(lines, strings.Split(strings.TrimRight(wrapped, "\n"), "\n")...)
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
	lines = append(lines, b.wrapStyledDelegationLines(output.SanitizeDisplayText(outputText), width, b.baseTextStyle())...)
	return lines
}

func (b *contentBuffer) delegationOutputDuplicatesTranscript(dd *delegationDisplayState, outputText string) bool {
	normalizedOutput := normalizeDelegationText(output.SanitizeDisplayText(outputText))
	if normalizedOutput == "" {
		return true
	}
	for i := len(dd.entries) - 1; i >= 0; i-- {
		entry := dd.entries[i]
		if entry.kind != delegationTranscriptEntryAssistant {
			continue
		}
		if normalizeDelegationText(output.SanitizeDisplayText(entry.body)) == normalizedOutput {
			return true
		}
		break
	}
	return false
}
