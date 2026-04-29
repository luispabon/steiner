package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	exitModalActionExit = iota
	exitModalActionCancel
)

type exitModalState struct {
	OverlayShell
	selectedAction int
}

func openExitModal(width, height int) exitModalState {
	shell := OverlayShell{}
	shell = shell.WithDimensions(width, height).WithTitle("exit").openShell()
	return exitModalState{
		OverlayShell:   shell,
		selectedAction: exitModalActionExit,
	}
}

func (s exitModalState) closeExitModal() exitModalState {
	s.OverlayShell = s.OverlayShell.closeShell()
	return s
}

func (s exitModalState) moveSelection(delta int) exitModalState {
	const actions = 2
	s.selectedAction = ((s.selectedAction+delta)%actions + actions) % actions
	return s
}

func (m *Model) renderExitModal() string {
	s := m.exitModal
	s.OverlayShell = s.OverlayShell.WithDimensions(m.width, m.height)

	overlayWidth := 52
	if overlayWidth > m.width-4 {
		overlayWidth = m.width - 4
	}
	if overlayWidth < 40 {
		overlayWidth = 40
	}
	innerWidth := overlayWidth - 4

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Fg)).
		Bold(true).
		Width(innerWidth).
		Render("Exit steiner?")
	body := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(innerWidth).
		Render("Leave the interactive session and return to the shell.")

	buttons := lipgloss.JoinHorizontal(lipgloss.Left,
		m.renderExitModalButton("Exit", s.selectedAction == exitModalActionExit),
		"  ",
		m.renderExitModalButton("Cancel", s.selectedAction == exitModalActionCancel),
	)
	buttonRow := lipgloss.NewStyle().Width(innerWidth).Render(buttons)
	footerText := FooterChip("tab/←→") + " move   " + FooterChip("enter") + " confirm   " + FooterChip("esc") + " cancel"
	footer := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(innerWidth).
		Render(footerText)

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		s.Divider(),
		body,
		"",
		buttonRow,
		s.Divider(),
		footer,
	)
	return m.styles.PaletteOverlay.Width(innerWidth).Padding(1, 1).Render(content)
}

func (m *Model) renderExitModalButton(label string, selected bool) string {
	if selected {
		return m.styles.AccentBg.Padding(0, 2).Render(label)
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev2)).
		Foreground(lipgloss.Color(theme.Fg)).
		Padding(0, 2).
		Render(label)
}

func (m Model) openExitModal() Model {
	m.exitModal = openExitModal(m.width, m.height)
	return m
}

func (m Model) confirmExitModal() (tea.Model, tea.Cmd) {
	switch m.exitModal.selectedAction {
	case exitModalActionCancel:
		m.exitModal = m.exitModal.closeExitModal()
		return m, nil
	default:
		if m.onExitRequested != nil {
			m.onExitRequested()
			return m, nil
		}
		return m, tea.Quit
	}
}
