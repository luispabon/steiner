package tui

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type planPickerOverlay struct {
	OverlayShell
	query          string
	allNames       []string
	candidates     []string
	selection      int
	scrollOffset   int
	triggerCommand string
	styles         theme.Styles
}

func newPlanPickerOverlay(styles theme.Styles) planPickerOverlay {
	return planPickerOverlay{styles: styles}
}

func (m planPickerOverlay) Open(triggerCommand string) planPickerOverlay {
	m.OverlayShell = m.openShell()
	m.query = ""
	m.selection = 0
	m.scrollOffset = 0
	m.triggerCommand = triggerCommand

	entries, err := os.ReadDir(".steiner/plans/")
	if err != nil {
		m.allNames = nil
		m.candidates = nil
		return m
	}

	m.allNames = make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			m.allNames = append(m.allNames, ".steiner/plans/"+entry.Name())
		}
	}
	m.candidates = append([]string(nil), m.allNames...)
	return m
}

func (m planPickerOverlay) Close() planPickerOverlay {
	m.OverlayShell = m.closeShell()
	return m
}

func (m planPickerOverlay) Update(msg tea.Msg) (planPickerOverlay, tea.Cmd) {
	if !m.IsOpen() {
		return m, nil
	}
	switch updateSearchPicker(&m.query, &m.selection, &m.scrollOffset, &m.candidates, m.allNames, msg, func(query string, entries []string) []string {
		return filterSearchPickerEntries(entries, query, func(entry string, loweredQuery string) bool {
			return strings.Contains(strings.ToLower(entry), loweredQuery)
		})
	}) {
	case searchPickerClosed:
		return m.Close(), nil
	case searchPickerHandled, searchPickerIgnored:
		return m, nil
	default:
		return m, nil
	}
}

func (m planPickerOverlay) View() string {
	if !m.IsOpen() {
		return ""
	}

	innerW := m.planPickerInnerWidth()
	const selectionPrefix = "> "
	const idlePrefix = "  "

	prefix := m.styles.Accent.Render(m.triggerCommand)
	queryDisplay := m.query
	if queryDisplay == "" {
		if len(m.candidates) > 0 {
			queryDisplay = m.styles.Accent.Render(m.candidates[m.selection])
		} else {
			queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search plans…")
		}
	}
	headerLine := lipgloss.NewStyle().Width(innerW).Render(prefix + " " + queryDisplay)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerW))

	lines := []string{headerLine, divider}

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))

	for i := m.scrollOffset; i < min(m.scrollOffset+maxDisplay, len(m.candidates)); i++ {
		name := m.candidates[i]
		row := nameStyle.Render(name)
		if i == m.selection {
			lines = append(lines, m.styles.PaletteItemActive.
				Width(innerW).
				MaxWidth(innerW).
				Render(m.styles.Accent.Render(selectionPrefix)+row))
		} else {
			lines = append(lines, lipgloss.NewStyle().
				Width(innerW).
				MaxWidth(innerW).
				Render(idlePrefix+row))
		}
	}

	if len(m.candidates) > m.scrollOffset+maxDisplay {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.FgMute)).
			Render(fmt.Sprintf("... and %d more", len(m.candidates)-(m.scrollOffset+maxDisplay))))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	rendered := m.styles.PaletteOverlay.Width(innerW+2).Padding(1, 1).Render(body)
	return theme.WithBg(rendered, lipgloss.Color(theme.BgElev))
}

func (m planPickerOverlay) planPickerInnerWidth() int {
	const maxOverlayInner = 90
	inner := m.InnerWidth()
	if inner > maxOverlayInner {
		inner = maxOverlayInner
	}
	if inner < 40 {
		inner = 40
	}
	return inner
}

func (m planPickerOverlay) SelectedName() string {
	if m.selection >= 0 && m.selection < len(m.candidates) {
		return m.candidates[m.selection]
	}
	return ""
}
