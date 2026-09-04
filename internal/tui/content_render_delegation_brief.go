package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// renderBulletList renders items as a bullet list with the given style.
func (b *contentBuffer) renderBulletList(items []string, width int, style lipgloss.Style) []string {
	rows := make([]string, 0, len(items))
	for _, item := range items {
		line := "• " + strings.TrimSpace(item)
		rows = append(rows, style.Render(truncateRunes(line, max(1, width))))
	}
	return rows
}

// renderDelegationBriefBody renders the expanded body of a structured delegation brief.
func (b *contentBuffer) renderDelegationBriefBody(dd *delegationDisplayState, width int) []string {
	if dd.briefObjective == "" {
		return nil
	}

	var rows []string
	h2 := b.styles.AccentLine.Bold(true)

	// Objective (always present if we got here)
	if dd.briefObjective != "" {
		rows = append(rows, h2.Render("Objective"))
		objLines := b.wrapStyledDelegationLines(dd.briefObjective, width, b.styles.FgMute.Italic(true))
		rows = append(rows, objLines...)
	}

	// Context
	if dd.briefContext != "" {
		rows = append(rows, h2.Render("Context"))
		ctxLines := b.wrapStyledDelegationLines(dd.briefContext, width, b.styles.FgMute.Italic(true))
		rows = append(rows, ctxLines...)
	}

	// Deliverable
	if dd.briefDeliverable != "" {
		rows = append(rows, h2.Render("Deliverable"))
		delLines := b.wrapStyledDelegationLines(dd.briefDeliverable, width, b.styles.FgMute.Italic(true))
		rows = append(rows, delLines...)
	}

	// Constraints
	if len(dd.briefConstraints) > 0 {
		rows = append(rows, h2.Render("Constraints"))
		rows = append(rows, b.renderBulletList(dd.briefConstraints, width, b.styles.FgMute)...)
	}

	// Success criteria
	if len(dd.briefSuccessCriteria) > 0 {
		rows = append(rows, h2.Render("Success criteria"))
		rows = append(rows, b.renderBulletList(dd.briefSuccessCriteria, width, b.styles.FgMute)...)
	}

	// Checks
	if len(dd.briefChecks) > 0 {
		rows = append(rows, h2.Render("Checks"))
		rows = append(rows, b.renderBulletList(dd.briefChecks, width, b.styles.FgMute)...)
	}

	if len(rows) == 0 {
		return nil
	}

	// Apply viewport height cap if set (maxDelegationBodyLines > 0).
	if b.maxDelegationBodyLines > 0 && len(rows) > b.maxDelegationBodyLines {
		rows = rows[:b.maxDelegationBodyLines]
	}
	return rows
}
