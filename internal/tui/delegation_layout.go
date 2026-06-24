package tui

import (
	"strings"
)

type delegationRowKind int

const (
	delegationRowBorderTop delegationRowKind = iota
	delegationRowHeader
	delegationRowPromptHeader
	delegationRowPromptBody
	delegationRowTranscript
	delegationRowOutput
	delegationRowSeparator
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
			text: b.renderDelegationHeader(dd, headerWidth),
		},
	}
	var promptBodyRows []delegationRow
	if !dd.collapsed && strings.TrimSpace(dd.promptText) != "" {
		rows = append(rows, delegationRow{kind: delegationRowPromptHeader, text: b.renderDelegationPromptHeader(dd)})
		if dd.promptCollapsed {
			if preview := previewDelegationText(dd.promptText); preview != "" {
				promptBodyRows = append(promptBodyRows, delegationRow{
					kind: delegationRowPromptBody,
					text: b.styles.FgMute.Render(truncateRunes(preview, max(1, headerWidth-2))),
				})
			}
		} else {
			for _, line := range b.renderDelegationPromptBody(dd, headerWidth) {
				promptBodyRows = append(promptBodyRows, delegationRow{kind: delegationRowPromptBody, text: line})
			}
		}
		rows = append(rows, promptBodyRows...)
	}
	if !dd.collapsed {
		transcriptRows := b.renderDelegationTranscript(dd, headerWidth)
		if !dd.promptCollapsed && len(promptBodyRows) > 0 && len(transcriptRows) > 0 {
			rows = append(rows, delegationRow{kind: delegationRowSeparator, text: b.renderDelegationFooterSeparator(headerWidth)})
		}
		for _, line := range transcriptRows {
			rows = append(rows, delegationRow{kind: delegationRowTranscript, text: line})
		}
		for _, line := range b.renderDelegationOutput(dd, headerWidth) {
			rows = append(rows, delegationRow{kind: delegationRowOutput, text: line})
		}
		if row := b.renderDelegationStatsRow(dd); row != "" {
			rows = append(rows,
				delegationRow{kind: delegationRowSeparator, text: b.renderDelegationFooterSeparator(headerWidth)},
				delegationRow{kind: delegationRowStats, text: row},
			)
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
