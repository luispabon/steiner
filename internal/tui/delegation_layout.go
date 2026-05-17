package tui

import "strings"

type delegationRowKind int

const (
	delegationRowBorderTop delegationRowKind = iota
	delegationRowHeader
	delegationRowPromptHeader
	delegationRowPromptBody
	delegationRowTranscript
	delegationRowOutput
	delegationRowBorderBottom
	delegationRowHint
)

type delegationRow struct {
	kind delegationRowKind
}

func (b *contentBuffer) delegationRows(dd *delegationDisplayState) []delegationRow {
	if dd == nil {
		return nil
	}
	rows := []delegationRow{{kind: delegationRowBorderTop}, {kind: delegationRowHeader}}
	if !dd.collapsed && strings.TrimSpace(dd.promptText) != "" {
		rows = append(rows, delegationRow{kind: delegationRowPromptHeader})
		rows = append(rows, delegationRow{kind: delegationRowPromptBody})
	}
	if !dd.collapsed {
		if len(dd.entries) > 0 {
			rows = append(rows, delegationRow{kind: delegationRowTranscript})
		}
		if strings.TrimSpace(dd.output) != "" && !b.delegationOutputDuplicatesTranscript(dd, strings.TrimSpace(dd.output)) {
			rows = append(rows, delegationRow{kind: delegationRowOutput})
		}
	}
	rows = append(rows, delegationRow{kind: delegationRowBorderBottom}, delegationRow{kind: delegationRowHint})
	return rows
}

func delegationRowIsInteractive(row delegationRowKind) bool {
	return row == delegationRowHeader || row == delegationRowPromptHeader
}
