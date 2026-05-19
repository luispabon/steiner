package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type delegationRowKind int

const (
	delegationRowBorderTop delegationRowKind = iota
	delegationRowHeader
	delegationRowPromptHeader
	delegationRowPromptBody
	delegationRowTranscript
	delegationRowOutput
	delegationRowStats
	delegationRowBorderBottom
	delegationRowHint
)

type delegationRow struct {
	kind delegationRowKind
	text string
}

func (b *contentBuffer) delegationRows(dd *delegationDisplayState, width int) []delegationRow {
	if dd == nil {
		return nil
	}
	headerWidth := width - 4
	if headerWidth < 1 {
		headerWidth = 1
	}
	rows := []delegationRow{
		{kind: delegationRowBorderTop},
		{
			kind: delegationRowHeader,
			text: theme.WithBg(b.renderDelegationHeader(dd, headerWidth), lipgloss.Color(theme.BgElev)),
		},
	}
	if !dd.collapsed && strings.TrimSpace(dd.promptText) != "" {
		rows = append(rows, delegationRow{kind: delegationRowPromptHeader, text: b.renderDelegationPromptHeader(dd)})
		if dd.promptCollapsed {
			if preview := previewDelegationText(dd.promptText); preview != "" {
				rows = append(rows, delegationRow{
					kind: delegationRowPromptBody,
					text: b.styles.FgMute.Render(truncateRunes(preview, max(1, headerWidth-2))),
				})
			}
		} else {
			for _, line := range b.renderDelegationPromptBody(dd, headerWidth) {
				rows = append(rows, delegationRow{kind: delegationRowPromptBody, text: line})
			}
		}
	}
	if !dd.collapsed {
		for _, line := range b.renderDelegationTranscript(dd, headerWidth) {
			rows = append(rows, delegationRow{kind: delegationRowTranscript, text: line})
		}
		for _, line := range b.renderDelegationOutput(dd, headerWidth) {
			rows = append(rows, delegationRow{kind: delegationRowOutput, text: line})
		}
		if dd.status == "complete" || dd.status == "partial" {
			if row := b.renderDelegationStatsRow(dd); row != "" {
				rows = append(rows, delegationRow{kind: delegationRowStats, text: row})
			}
		}
	}
	rows = append(rows,
		delegationRow{kind: delegationRowBorderBottom},
		delegationRow{kind: delegationRowHint, text: b.renderDelegationHint(dd)},
	)
	return rows
}

func delegationRowIsInteractive(row delegationRowKind) bool {
	return row == delegationRowHeader || row == delegationRowPromptHeader
}
