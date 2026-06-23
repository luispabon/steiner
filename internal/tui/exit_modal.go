package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/interactive"
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
	shell := OverlayShell{}.WithPreferredWidth(60)
	shell = shell.WithDimensions(width, height).WithTitle("exit").openShell()
	return exitModalState{
		OverlayShell:   shell,
		selectedAction: exitModalActionExit,
	}
}

func (s exitModalState) closeExitModal() exitModalState {
	s.OverlayShell = s.closeShell()
	return s
}

func (s exitModalState) moveSelection(delta int) exitModalState {
	const actions = 2
	s.selectedAction = ((s.selectedAction+delta)%actions + actions) % actions
	return s
}

func (m *Model) renderExitModal() string {
	s := m.exitModal
	s.OverlayShell = s.WithDimensions(m.width, m.height)

	contentWidth := max(1, s.InnerWidth()-2)

	title := lipgloss.NewStyle().
		Foreground(m.styles.AccentColor).
		Bold(true).
		Width(contentWidth).
		Render("Exit steiner?")
	body := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(contentWidth).
		Render("Leave the interactive session and return to the shell.")

	cancelButton := m.renderExitModalButton("Cancel", s.selectedAction == exitModalActionCancel)
	exitButton := m.renderExitModalButton("Exit", s.selectedAction == exitModalActionExit)
	buttonRow := strings.Repeat(" ", contentWidth)
	buttonRow = composeOverlayLine(buttonRow, cancelButton, contentWidth, 0, lipgloss.Width(cancelButton))
	buttonRow = composeOverlayLine(buttonRow, exitButton, contentWidth, contentWidth-lipgloss.Width(exitButton), lipgloss.Width(exitButton))
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
	box := m.styles.PaletteOverlay.Width(s.InnerWidth()).Padding(0, 1).Render(content)
	return theme.WithBg(box, theme.BgElev)
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
		if m.controller != nil {
			if err := m.controller.Handle(context.Background(), interactive.RequestExit{}); err != nil {
				m.content.AppendLine("status: " + err.Error())
			}
			return m, nil
		}
		return m, tea.Quit
	}
}
