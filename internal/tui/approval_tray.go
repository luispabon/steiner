package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

var approvalDecisions = []ApprovalDecision{
	ApprovalDecisionAllowOnce,
	ApprovalDecisionAlwaysAllow,
	ApprovalDecisionDeny,
}

func approvalDecisionLabel(decision ApprovalDecision) string {
	switch decision {
	case ApprovalDecisionAlwaysAllow:
		return "Always allow"
	case ApprovalDecisionDeny:
		return "Deny"
	default:
		return "Allow once"
	}
}

func (m Model) approvalTrayHeight(width int) int {
	if !m.approval.active {
		return 0
	}
	return lipgloss.Height(m.renderApprovalTray(width))
}

func (m Model) renderApprovalTray(width int) string {
	if !m.approval.active || width < 1 {
		return ""
	}

	trayStyle := lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Background(lipgloss.Color(theme.BgElev2)).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(m.styles.AccentColor)
	innerWidth := max(1, width-4)

	headerParts := []string{
		m.styles.AccentBg.Render(" approval "),
		m.styles.CardLabel.Render(m.approval.tool),
	}
	if innerWidth >= 36 && strings.TrimSpace(m.approval.mode) != "" {
		headerParts = append(headerParts, m.styles.FgDim.Render("mode "+m.approval.mode))
	}

	previewStyle := m.styles.FgDim.Width(innerWidth)
	preview := strings.TrimSpace(m.approval.preview)
	if preview == "" {
		preview = "no preview available"
	}

	lines := []string{lipgloss.JoinHorizontal(lipgloss.Left, headerParts...)}
	if innerWidth >= 28 {
		lines = append(lines, m.styles.FgFaint.Render("review the request, then choose an action"))
	}
	lines = append(lines, previewStyle.Render(preview))
	lines = append(lines, m.renderApprovalActions(innerWidth))

	if innerWidth >= 44 {
		lines = append(lines, m.styles.FgFaint.Render("tab / arrows move • enter confirms • esc denies"))
	}

	return trayStyle.Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func (m Model) renderApprovalActions(width int) string {
	items := make([]string, 0, len(approvalDecisions))
	for idx, decision := range approvalDecisions {
		label := approvalDecisionLabel(decision)
		style := lipgloss.NewStyle().
			Padding(0, 1).
			Background(lipgloss.Color(theme.BgInput)).
			Foreground(lipgloss.Color(theme.Fg))
		if idx == m.approval.selectedAction {
			style = m.styles.AccentBg
		}
		items = append(items, style.Render(label))
	}

	line := strings.Join(items, " ")
	if lipgloss.Width(line) <= width {
		return line
	}
	return lipgloss.JoinVertical(lipgloss.Left, items...)
}
