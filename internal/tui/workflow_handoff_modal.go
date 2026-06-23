package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	workflowHandoffActionAccept = iota
	workflowHandoffActionChangeModel
	workflowHandoffActionDismiss
)

type workflowHandoffModalState struct {
	OverlayShell
	next           string
	target         string
	message        string
	modelAlias     string
	modelSource    string
	selectedAction int
}

func openWorkflowHandoffModal(width, height int, payload output.WorkflowHandoffEvent, selection interactive.WorkflowHandoffModelSelection) workflowHandoffModalState {
	shell := OverlayShell{}.WithPreferredWidth(72)
	shell = shell.WithDimensions(width, height).WithTitle("workflow handoff").openShell()
	return workflowHandoffModalState{
		OverlayShell:   shell,
		next:           strings.TrimSpace(payload.Next),
		target:         strings.TrimSpace(payload.Target),
		message:        strings.TrimSpace(payload.Message),
		modelAlias:     strings.TrimSpace(selection.ModelAlias),
		modelSource:    strings.TrimSpace(selection.SourceLabel),
		selectedAction: workflowHandoffActionAccept,
	}
}

func (s workflowHandoffModalState) close() workflowHandoffModalState {
	s.OverlayShell = s.closeShell()
	return s
}

func (s workflowHandoffModalState) moveSelection(delta int) workflowHandoffModalState {
	const actions = 3
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
	contentWidth := s.InnerWidth()

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Fg)).
		Bold(true).
		Width(contentWidth).
		Render(s.promptText())

	warningText := s.warningText()
	bodyLines := []string{
		lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.FgMute)).
			Bold(true).
			Width(contentWidth).
			Render(warningText),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Width(contentWidth).Render(s.modelLine()),
		lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Width(contentWidth).Render(s.planningFolderLine()),
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
	changeModelButton := m.renderExitModalButton("Change Model", s.selectedAction == workflowHandoffActionChangeModel)
	dismissButton := m.renderExitModalButton("Dismiss", s.selectedAction == workflowHandoffActionDismiss)
	buttonRow := m.renderWorkflowHandoffActionRow(contentWidth, acceptButton, changeModelButton, dismissButton)

	sections := []string{
		title,
		"",
		lipgloss.JoinVertical(lipgloss.Left, bodyLines...),
	}
	if m.modelPicker.IsOpen() && m.modelPicker.IsWorkflowHandoff() {
		sections = append(sections, "", m.modelPicker.ViewAttached(contentWidth))
	}
	sections = append(sections, "", buttonRow)

	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", contentWidth))
	footerText := FooterChip("tab/←→") + " move   " + FooterChip("enter") + " confirm"
	if m.modelPicker.IsOpen() && m.modelPicker.IsWorkflowHandoff() {
		footerText += "   " + FooterChip("esc") + " back"
	} else {
		footerText += "   " + FooterChip("esc") + " dismiss"
	}
	footer := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(contentWidth).
		Render(footerText)

	sections = append(sections, divider, footer)
	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return s.RenderWithBg(m.styles.PaletteOverlay, content, theme.BgElev)
}

func (m Model) renderWorkflowHandoffActionRow(contentWidth int, acceptButton string, changeModelButton string, dismissButton string) string {
	row := lipgloss.JoinHorizontal(lipgloss.Top, acceptButton, "  ", changeModelButton, "  ", dismissButton)
	return lipgloss.NewStyle().Width(contentWidth).Render(row)
}

func (s workflowHandoffModalState) warningText() string {
	jobName := s.next
	if jobName == "" {
		jobName = "workflow"
	}
	return "This will clear the context and start the '" + jobName + "' job"
}

func (s workflowHandoffModalState) modelLine() string {
	modelAlias := strings.TrimSpace(s.modelAlias)
	modelSource := strings.TrimSpace(s.modelSource)
	label := lipgloss.NewStyle().Bold(true).Render("Model:")
	if modelAlias == "" {
		return label
	}
	if modelSource == "" {
		return label + " " + modelAlias
	}
	return label + " " + modelAlias + " (" + modelSource + ")"
}

func (s workflowHandoffModalState) planningFolderLine() string {
	label := lipgloss.NewStyle().Bold(true).Render("Planning folder:")
	return label + " " + s.target
}

func (m Model) handleWorkflowHandoffModalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		m.workflowHandoff = m.workflowHandoff.moveSelection(-1)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		m.workflowHandoff = m.workflowHandoff.moveSelection(1)
	case tea.KeyEnter:
		switch m.workflowHandoff.selectedAction {
		case workflowHandoffActionAccept:
			return m.acceptWorkflowHandoff()
		case workflowHandoffActionChangeModel:
			return m.openWorkflowHandoffModelPicker(), nil
		}
		return m.dismissWorkflowHandoff()
	case tea.KeyEsc:
		return m.dismissWorkflowHandoff()
	}
	return m, nil
}

func (m Model) openWorkflowHandoffModelPicker() Model {
	title := m.workflowHandoff.modelPickerTitle()
	m.modelPicker = m.modelPicker.OpenForWorkflowHandoff(title, m.modelNames, m.workflowHandoff.modelAlias)
	m.modelPicker.width = m.width
	m.modelPicker.height = m.height
	return m
}

func (m Model) acceptWorkflowHandoff() (tea.Model, tea.Cmd) {
	next := strings.TrimSpace(m.workflowHandoff.next)
	target := strings.TrimSpace(m.workflowHandoff.target)
	modelName := strings.TrimSpace(m.workflowHandoff.modelAlias)
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SubmitWorkflowHandoff{Decision: "accept"}); err != nil {
			m.content.AppendLine("status: " + err.Error())
			m.syncViewport()
			return m, nil
		}
	}
	if modelName != "" && modelName != strings.TrimSpace(m.primaryModel) {
		if m.controller != nil {
			if err := m.controller.Handle(context.Background(), interactive.SwitchModel{Name: modelName}); err != nil {
				m.content.AppendLine("status: " + err.Error())
				m.syncViewport()
				return m, nil
			}
		}
		m.applyModelSelection(modelName, strings.TrimSpace(m.modelBaseURLs[modelName]))
	}
	m.workflowHandoff = m.workflowHandoff.close()
	m.suppressWorkflowHandoffRun = true
	m.pendingWorkflowHandoffLaunch = &workflowHandoffLaunch{next: next, target: target, modelName: modelName}
	nextModel, cmd := m.clearConversationState()
	if cleared, ok := nextModel.(Model); ok {
		cleared.suppressWorkflowHandoffRun = true
		cleared.pendingWorkflowHandoffLaunch = &workflowHandoffLaunch{next: next, target: target, modelName: modelName}
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

func (m Model) launchWorkflowHandoff(next, target string, _ string) (tea.Model, tea.Cmd) {
	return m.executeInvokeSkillAction(next, target)
}

func (s workflowHandoffModalState) modelPickerTitle() string {
	switch s.next {
	case "implement":
		return "Select model for implementation"
	case "review":
		return "Select model for review"
	default:
		return "Select model for next workflow"
	}
}
