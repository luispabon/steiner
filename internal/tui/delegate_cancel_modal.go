package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type delegateCancelScreen int

const (
	delegateCancelScreenSelector delegateCancelScreen = iota
	delegateCancelScreenConfirmTarget
	delegateCancelScreenConfirmTargetCode
	delegateCancelScreenConfirmAll
	delegateCancelScreenConfirmRun
)

type delegateCancelModalState struct {
	OverlayShell
	screen   delegateCancelScreen
	rows     []delegateActiveRow
	selected int
	target   int
}

func openDelegateCancelModal(width, height int, rows []delegateActiveRow) delegateCancelModalState {
	shell := OverlayShell{}.
		WithPreferredWidth(72).
		WithDimensions(width, height).
		WithTitle("stop delegate").
		openShell()
	return delegateCancelModalState{
		OverlayShell: shell,
		screen:       delegateCancelScreenSelector,
		rows:         append([]delegateActiveRow(nil), rows...),
	}
}

func (s delegateCancelModalState) close() delegateCancelModalState {
	s.OverlayShell = s.closeShell()
	return s
}

func (s delegateCancelModalState) moveSelection(delta int) delegateCancelModalState {
	count := s.delegateCancelSelectionCount()
	if count == 0 {
		return s
	}
	s.selected = ((s.selected+delta)%count + count) % count
	return s
}

func (s delegateCancelModalState) delegateCancelSelectionCount() int {
	if s.screen == delegateCancelScreenSelector {
		return len(s.rows) + 3
	}
	switch s.screen {
	case delegateCancelScreenConfirmTarget, delegateCancelScreenConfirmAll, delegateCancelScreenConfirmRun:
		return 2
	case delegateCancelScreenConfirmTargetCode:
		return 3
	default:
		return 0
	}
}

func (m *Model) openDelegateCancelModal() *Model {
	rows := m.content.ActiveDelegateRows()
	if len(rows) == 0 {
		return m
	}
	m.delegateCancelModal = openDelegateCancelModal(m.width, m.height, rows)
	return m
}

func (m *Model) renderDelegateCancelModal() string {
	s := m.delegateCancelModal
	s.OverlayShell = s.WithDimensions(m.width, m.height)
	contentWidth := s.InnerWidth()

	var sections []string
	switch s.screen {
	case delegateCancelScreenSelector:
		sections = append(sections,
			m.delegateCancelHeading("Stop a delegate", contentWidth),
			"",
			m.renderDelegateCancelSelector(contentWidth),
		)
	case delegateCancelScreenConfirmTarget, delegateCancelScreenConfirmTargetCode:
		sections = append(sections, m.delegateCancelHeading("Stop delegate?", contentWidth), "", m.delegateCancelTargetBody(contentWidth))
	case delegateCancelScreenConfirmAll:
		sections = append(sections, m.delegateCancelHeading("Stop all delegates?", contentWidth), "", lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Width(contentWidth).Render("Stop all active delegates?"))
	case delegateCancelScreenConfirmRun:
		sections = append(sections, m.delegateCancelHeading("Stop entire run?", contentWidth), "", lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Width(contentWidth).Render("Stop the entire active run and its delegates?"))
	}

	sections = append(sections, "", m.renderDelegateCancelButtons(contentWidth), s.Divider(), s.RenderFooter(FooterChip("↑↓/←→")+" move   "+FooterChip("enter")+" confirm   "+FooterChip("esc")+" back"))
	content := lipgloss.JoinVertical(lipgloss.Left, sections...)
	return s.RenderWithBg(m.styles.PaletteOverlay, content, theme.BgElev)
}

func (m *Model) delegateCancelHeading(text string, contentWidth int) string {
	return lipgloss.NewStyle().Foreground(m.styles.AccentColor).Bold(true).Width(contentWidth).Render(text)
}

func (m *Model) renderDelegateCancelSelector(contentWidth int) string {
	lines := make([]string, 0, len(m.delegateCancelModal.rows)+3)
	for i, row := range m.delegateCancelModal.rows {
		line := m.renderDelegateCancelRow(row, contentWidth)
		if i == m.delegateCancelModal.selected {
			line = m.styles.PaletteItemActive.Width(contentWidth).Render(line)
		}
		lines = append(lines, line)
	}
	labels := []string{"Stop all delegates", "Stop entire run", "Dismiss"}
	for i, label := range labels {
		index := len(m.delegateCancelModal.rows) + i
		line := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Width(contentWidth).Render(label)
		if index == m.delegateCancelModal.selected {
			line = m.styles.PaletteItemActive.Width(contentWidth).Render(line)
		}
		lines = append(lines, line)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m *Model) renderDelegateCancelRow(row delegateActiveRow, contentWidth int) string {
	typeLabel := strings.TrimSpace(row.agentType)
	if typeLabel == "" {
		typeLabel = "delegate"
	}
	typeStyle := styleByKey(m.styles.DelegateTagStyles, strings.ToLower(typeLabel), m.styles.ToolTagDefault).Bold(true)
	styledType := typeStyle.Render(typeLabel)
	prefixWidth := lipgloss.Width(styledType) + 3 + lipgloss.Width(row.agentID) + 3
	preview := truncateOverlayText(row.taskPreview, max(1, contentWidth-prefixWidth))
	return styledType + " · " + row.agentID + " · " + preview
}

func (m *Model) delegateCancelTargetBody(contentWidth int) string {
	if m.delegateCancelModal.target < 0 || m.delegateCancelModal.target >= len(m.delegateCancelModal.rows) {
		return ""
	}
	row := m.delegateCancelModal.rows[m.delegateCancelModal.target]
	text := "Stop delegate " + row.agentID + "?"
	if m.delegateCancelModal.screen == delegateCancelScreenConfirmTargetCode {
		text += " Choose whether to keep or discard its worktree."
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Width(contentWidth).Render(text)
}

func (m *Model) renderDelegateCancelButtons(contentWidth int) string {
	labels := m.delegateCancelButtonLabels()
	buttons := make([]string, len(labels))
	for i, label := range labels {
		buttons[i] = m.renderExitModalButton(label, m.delegateCancelModal.selected == i)
	}
	return lipgloss.NewStyle().Width(contentWidth).Render(lipgloss.JoinVertical(lipgloss.Left, buttons...))
}

func (m *Model) delegateCancelButtonLabels() []string {
	switch m.delegateCancelModal.screen {
	case delegateCancelScreenSelector:
		return nil
	case delegateCancelScreenConfirmTarget:
		return []string{"Stop", "Keep working"}
	case delegateCancelScreenConfirmTargetCode:
		return []string{"Stop and keep worktree", "Stop and discard worktree", "Keep working"}
	case delegateCancelScreenConfirmAll:
		return []string{"Stop all", "Keep working"}
	case delegateCancelScreenConfirmRun:
		return []string{"Stop run", "Keep working"}
	default:
		return nil
	}
}

func (m *Model) refreshDelegateCancelSelector() {
	rows := m.content.ActiveDelegateRows()
	if len(rows) == 0 {
		m.delegateCancelModal = m.delegateCancelModal.close()
		return
	}
	m.delegateCancelModal.rows = rows
	m.delegateCancelModal.screen = delegateCancelScreenSelector
	m.delegateCancelModal.selected = 0
	m.delegateCancelModal.target = 0
}

func (m *Model) executeDelegateCancelAction(action interactive.Action) *Model {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), action); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
			m.syncViewport()
		}
	}
	m.delegateCancelModal = m.delegateCancelModal.close()
	return m
}

func (m *Model) handleDelegateCancelModalKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		m.delegateCancelModal = m.delegateCancelModal.moveSelection(-1)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		m.delegateCancelModal = m.delegateCancelModal.moveSelection(1)
	case tea.KeyEsc:
		if m.delegateCancelModal.screen == delegateCancelScreenSelector {
			m.delegateCancelModal = m.delegateCancelModal.close()
		} else {
			m.refreshDelegateCancelSelector()
		}
	case tea.KeyEnter:
		return m.confirmDelegateCancelModal()
	}
	return nil
}

func (m *Model) confirmDelegateCancelModal() tea.Cmd {
	if m.delegateCancelModal.screen == delegateCancelScreenSelector {
		m.confirmDelegateCancelSelector()
		return nil
	}
	m.confirmDelegateCancelAction()
	return nil
}

func (m *Model) confirmDelegateCancelSelector() {
	s := m.delegateCancelModal
	switch {
	case s.selected < len(s.rows):
		s.target = s.selected
		s.selected = 0
		if s.rows[s.target].isCode {
			s.screen = delegateCancelScreenConfirmTargetCode
		} else {
			s.screen = delegateCancelScreenConfirmTarget
		}
	case s.selected == len(s.rows):
		s.screen = delegateCancelScreenConfirmAll
		s.selected = 0
	case s.selected == len(s.rows)+1:
		s.screen = delegateCancelScreenConfirmRun
		s.selected = 0
	default:
		m.delegateCancelModal = s.close()
		return
	}
	m.delegateCancelModal = s
}

func (m *Model) confirmDelegateCancelAction() {
	s := m.delegateCancelModal
	targetStillActive := func() bool {
		if s.target < 0 || s.target >= len(s.rows) {
			return false
		}
		agentID := s.rows[s.target].agentID
		for _, row := range m.content.ActiveDelegateRows() {
			if row.agentID == agentID {
				return true
			}
		}
		return false
	}
	switch s.screen {
	case delegateCancelScreenConfirmTarget:
		if s.selected == 0 && targetStillActive() {
			m.delegateCancelModal = s
			m = m.executeDelegateCancelAction(interactive.CancelDelegate{AgentID: s.rows[s.target].agentID, Discard: false})
			m.syncViewport()
			return
		}
		m.refreshDelegateCancelSelector()
	case delegateCancelScreenConfirmTargetCode:
		if s.selected < 2 && targetStillActive() {
			m.delegateCancelModal = s
			m = m.executeDelegateCancelAction(interactive.CancelDelegate{AgentID: s.rows[s.target].agentID, Discard: s.selected == 1})
			m.syncViewport()
			return
		}
		m.refreshDelegateCancelSelector()
	case delegateCancelScreenConfirmAll:
		if s.selected == 0 {
			m.delegateCancelModal = s
			m = m.executeDelegateCancelAction(interactive.CancelAllDelegates{})
			m.syncViewport()
			return
		}
		m.refreshDelegateCancelSelector()
	case delegateCancelScreenConfirmRun:
		if s.selected == 0 {
			m.executeInterruptAction()
			m.delegateCancelModal = m.delegateCancelModal.close()
			return
		}
		m.refreshDelegateCancelSelector()
	}
}
