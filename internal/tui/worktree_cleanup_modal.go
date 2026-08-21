package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	worktreeCleanupActionSkip = iota
	worktreeCleanupActionPrune
)

type worktreeCleanupModalState struct {
	OverlayShell
	selectedAction int
	worktreeCount  int
}

func openWorktreeCleanupModal(width, height, count int) worktreeCleanupModalState {
	shell := OverlayShell{}.WithPreferredWidth(60)
	shell = shell.WithDimensions(width, height).WithTitle("worktrees").openShell()
	return worktreeCleanupModalState{
		OverlayShell:   shell,
		selectedAction: worktreeCleanupActionSkip,
		worktreeCount:  count,
	}
}

func (s worktreeCleanupModalState) closeWorktreeCleanupModal() worktreeCleanupModalState {
	s.OverlayShell = s.closeShell()
	return s
}

func (s worktreeCleanupModalState) moveSelection(delta int) worktreeCleanupModalState {
	const actions = 2
	s.selectedAction = ((s.selectedAction+delta)%actions + actions) % actions
	return s
}

func (m *Model) renderWorktreeCleanupModal() string {
	s := m.worktreeCleanupModal
	s.OverlayShell = s.WithDimensions(m.width, m.height)

	contentWidth := s.InnerWidth()
	title := lipgloss.NewStyle().
		Foreground(m.styles.AccentColor).
		Bold(true).
		Width(contentWidth).
		Render(fmt.Sprintf("Clean up %d worktrees?", s.worktreeCount))
	body := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(contentWidth).
		Render("Cleanup removes delegate branches and may discard unmerged child work.")

	skipButton := m.renderWorktreeCleanupButton("Exit without cleaning", s.selectedAction == worktreeCleanupActionSkip)
	pruneButton := m.renderWorktreeCleanupButton("Clean up on exit", s.selectedAction == worktreeCleanupActionPrune)
	buttonRow := strings.Repeat(" ", contentWidth)
	buttonRow = composeOverlayLine(buttonRow, skipButton, contentWidth, 0, lipgloss.Width(skipButton))
	buttonRow = composeOverlayLine(buttonRow, pruneButton, contentWidth, contentWidth-lipgloss.Width(pruneButton), lipgloss.Width(pruneButton))
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", contentWidth))
	footerText := FooterChip("tab/←→") + " move   " + FooterChip("enter") + " confirm   " + FooterChip("esc") + " cancel"
	footer := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(contentWidth).
		Render(footerText)

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		body,
		"",
		buttonRow,
		divider,
		footer,
	)
	return s.RenderWithBg(m.styles.PaletteOverlay, content, theme.BgElev)
}

func (m *Model) renderWorktreeCleanupButton(label string, selected bool) string {
	if selected {
		return m.styles.AccentBg.Padding(0, 2).Render(label)
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev2)).
		Foreground(lipgloss.Color(theme.Fg)).
		Padding(0, 2).
		Render(label)
}

//nolint:unparam // returns Model for modal helper consistency
func (m *Model) openWorktreeCleanupModal(width, height, count int) *Model {
	m.worktreeCleanupModal = openWorktreeCleanupModal(width, height, count)
	return m
}

func (m *Model) confirmWorktreeCleanupModal() (tea.Model, tea.Cmd) {
	selectedAction := m.worktreeCleanupModal.selectedAction
	m.worktreeCleanupModal = m.worktreeCleanupModal.closeWorktreeCleanupModal()
	m.exitFlowPhase = exitFlowPhaseNone
	if selectedAction == worktreeCleanupActionPrune && m.worktreePlan != nil {
		m.worktreePlan.Request()
	}
	return m.doExit()
}
