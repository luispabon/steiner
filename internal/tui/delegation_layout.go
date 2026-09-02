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
	delegationRowQuestionHeader
	delegationRowQuestionBody
	delegationRowFilesHeader
	delegationRowFilesBody
	delegationRowTranscript
	delegationRowOutput
	delegationRowSeparator
	delegationRowStats
	delegationRowBorderBottom
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
	sectionRows, hasSections := b.delegationSectionRows(dd, headerWidth)
	rows = append(rows, sectionRows...)
	if !dd.collapsed {
		transcriptRows := b.renderDelegationTranscript(dd, headerWidth)
		if hasSections && len(transcriptRows) > 0 {
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
	)
	return rows
}

// delegationSectionRows renders the expanded-body section rows (prompt, and for
// advisor boxes the question/files) and reports whether any section rows were
// produced, so delegationRows can insert a separator before the transcript
// block. Prompt sections count only when non-collapsed body rows exist,
// matching the pre-existing separator rule.
func (b *contentBuffer) delegationSectionRows(dd *delegationDisplayState, width int) ([]delegationRow, bool) {
	if dd.collapsed {
		return nil, false
	}
	var rows []delegationRow
	hasSections := false
	if strings.TrimSpace(dd.promptText) != "" {
		rows = append(rows, delegationRow{kind: delegationRowPromptHeader, text: b.renderDelegationPromptHeader(dd)})
		if dd.promptCollapsed {
			if preview := previewDelegationText(dd.promptText); preview != "" {
				rows = append(rows, delegationRow{
					kind: delegationRowPromptBody,
					text: b.styles.FgMute.Render(truncateRunes(preview, max(1, width-2))),
				})
			}
		} else {
			for _, line := range b.renderDelegationPromptBody(dd, width) {
				rows = append(rows, delegationRow{kind: delegationRowPromptBody, text: line})
				hasSections = true
			}
		}
	}
	if dd.isAdvisor {
		if strings.TrimSpace(dd.advisorQuestion) != "" {
			hasSections = true
			rows = append(rows, delegationRow{kind: delegationRowQuestionHeader, text: b.renderDelegationSectionHeader("question")})
			for _, line := range b.renderAdvisorQuestionBody(dd, width) {
				rows = append(rows, delegationRow{kind: delegationRowQuestionBody, text: line})
			}
		}
		if len(dd.advisorFiles) > 0 {
			hasSections = true
			rows = append(rows, delegationRow{kind: delegationRowFilesHeader, text: b.renderDelegationSectionHeader("files")})
			for _, line := range b.renderAdvisorFilesBody(dd, width) {
				rows = append(rows, delegationRow{kind: delegationRowFilesBody, text: line})
			}
		}
		if dd.advisorDenied > 0 {
			hasSections = true
			denialLine := b.renderAdvisorDenialWarning(dd, width)
			rows = append(rows, delegationRow{kind: delegationRowFilesBody, text: denialLine})
		}
	}
	return rows, hasSections
}

func delegationRowIsInteractive(row delegationRowKind) bool {
	return row == delegationRowHeader || row == delegationRowPromptHeader
}

// delegationContentRows returns the rows renderDelegationBoxRows actually emits:
// delegationRows minus the two border rows and minus empty-text rows. Single
// source of truth for both an entry's rendered line count and the row-kind
// lookup used by click handling — these must not be computed from two
// different lists or clicks will silently hit the wrong row.
func (b *contentBuffer) delegationContentRows(dd *delegationDisplayState, width int) []delegationRow {
	rows := b.delegationRows(dd, width)
	filtered := make([]delegationRow, 0, len(rows))
	for _, row := range rows {
		if row.kind == delegationRowBorderTop || row.kind == delegationRowBorderBottom {
			continue
		}
		if row.text != "" {
			filtered = append(filtered, row)
		}
	}
	return filtered
}
