package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	workflowHandoffActionAccept = iota
	workflowHandoffActionDismiss
)

type workflowHandoffModalState struct {
	OverlayShell
	next           string
	target         string
	message        string
	selectedAction int
}

func openWorkflowHandoffModal(width, height int, payload output.WorkflowHandoffEvent) workflowHandoffModalState {
	shell := OverlayShell{}.WithPreferredWidth(72)
	shell = shell.WithDimensions(width, height).WithTitle("workflow handoff").openShell()
	return workflowHandoffModalState{
		OverlayShell:   shell,
		next:           strings.TrimSpace(payload.Next),
		target:         strings.TrimSpace(payload.Target),
		message:        strings.TrimSpace(payload.Message),
		selectedAction: workflowHandoffActionAccept,
	}
}

func (s workflowHandoffModalState) close() workflowHandoffModalState {
	s.OverlayShell = s.closeShell()
	return s
}

func (s workflowHandoffModalState) moveSelection(delta int) workflowHandoffModalState {
	const actions = 2
	s.selectedAction = ((s.selectedAction+delta)%actions + actions) % actions
	return s
}

func (s workflowHandoffModalState) acceptLabel() string {
	switch s.next {
	case "implement":
		return "Accept: Clear + Implement"
	case "review":
		return "Accept: Clear + Review"
	default:
		return "Accept"
	}
}

func (s workflowHandoffModalState) promptText() string {
	switch s.next {
	case "implement":
		return "Continue to implementation?"
	case "review":
		return "Continue to review?"
	default:
		return "Continue to the next workflow?"
	}
}

func (m *Model) renderWorkflowHandoffModal() string {
	s := m.workflowHandoff
	s.OverlayShell = s.WithDimensions(m.width, m.height)
	contentWidth := max(1, s.InnerWidth()-2)

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Fg)).
		Bold(true).
		Width(contentWidth).
		Render("Pending workflow handoff")

	bodyLines := []string{
		lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.FgMute)).
			Width(contentWidth).
			Render("This will clear the current conversation and start the next workflow."),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Width(contentWidth).Render(s.promptText()),
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Width(contentWidth).Render("Planning folder: " + s.target),
	}
	if s.message != "" {
		bodyLines = append(bodyLines,
			"",
			lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.FgMute)).
				Width(contentWidth).
				Render(s.message),
		)
	}

	acceptButton := m.renderExitModalButton(s.acceptLabel(), s.selectedAction == workflowHandoffActionAccept)
	dismissButton := m.renderExitModalButton("Dismiss", s.selectedAction == workflowHandoffActionDismiss)
	buttonRow := strings.Repeat(" ", contentWidth)
	buttonRow = composeOverlayLine(buttonRow, acceptButton, contentWidth, 0, lipgloss.Width(acceptButton))
	buttonRow = composeOverlayLine(buttonRow, dismissButton, contentWidth, contentWidth-lipgloss.Width(dismissButton), lipgloss.Width(dismissButton))

	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", contentWidth))
	footerText := FooterChip("tab/←→") + " move   " + FooterChip("enter") + " confirm   " + FooterChip("esc") + " dismiss"
	footer := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(contentWidth).
		Render(footerText)

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		lipgloss.JoinVertical(lipgloss.Left, bodyLines...),
		"",
		buttonRow,
		divider,
		footer,
	)
	box := m.styles.PaletteOverlay.Width(s.InnerWidth()).Padding(0, 1).Render(content)
	return theme.WithBg(box, lipgloss.Color(theme.BgElev))
}

func (m Model) handleWorkflowHandoffModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyLeft, tea.KeyUp:
		m.workflowHandoff = m.workflowHandoff.moveSelection(-1)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		m.workflowHandoff = m.workflowHandoff.moveSelection(1)
	case tea.KeyEnter:
		if m.workflowHandoff.selectedAction == workflowHandoffActionAccept {
			return m.acceptWorkflowHandoff()
		}
		return m.dismissWorkflowHandoff()
	case tea.KeyEsc:
		return m.dismissWorkflowHandoff()
	}
	return m, nil
}

func (m Model) acceptWorkflowHandoff() (tea.Model, tea.Cmd) {
	next := strings.TrimSpace(m.workflowHandoff.next)
	target := strings.TrimSpace(m.workflowHandoff.target)
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitWorkflowHandoff{Decision: "accept"}); err != nil {
			m.content.AppendLine("status: " + err.Error())
			m.syncViewport()
			return m, nil
		}
	}
	m.workflowHandoff = m.workflowHandoff.close()
	m.suppressWorkflowHandoffRun = true
	m.pendingWorkflowHandoffLaunch = &workflowHandoffLaunch{next: next, target: target}
	nextModel, cmd := m.clearConversationState()
	if cleared, ok := nextModel.(Model); ok {
		cleared.suppressWorkflowHandoffRun = true
		cleared.pendingWorkflowHandoffLaunch = &workflowHandoffLaunch{next: next, target: target}
		cleared.workflowHandoff = cleared.workflowHandoff.close()
		// Rotate session after conversation is cleared — new workflow gets a fresh identity
		// controller may be nil in tests; skip rotation when there's no backing session.
		if cleared.controller != nil {
			if err := cleared.controller.Handle(context.Background(), interactive.RotateSession{}); err != nil {
				cleared.content.AppendLine("status: " + err.Error())
				cleared.syncViewport()
				return cleared, nil
			}
		}
		return cleared, cmd
	}
	return nextModel, cmd
}

func (m Model) dismissWorkflowHandoff() (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitWorkflowHandoff{Decision: "dismiss"}); err != nil {
			m.content.AppendLine("status: " + err.Error())
		}
	}
	m.workflowHandoff = m.workflowHandoff.close()
	m.syncViewport()
	return m, nil
}

func (m Model) launchWorkflowHandoff(next, target string) (tea.Model, tea.Cmd) {
	return m.executeInvokeSkillAction(next, target)
}
