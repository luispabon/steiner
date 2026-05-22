package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type modelPickerOverlay struct {
	OverlayShell
	query        string
	allNames     []string
	candidates   []string
	selection    int
	scrollOffset int
	currentModel string
	styles       theme.Styles
}

func newModelPickerOverlay(styles theme.Styles) modelPickerOverlay {
	return modelPickerOverlay{styles: styles}
}

func (m modelPickerOverlay) Open(names []string, current string) modelPickerOverlay {
	m.OverlayShell = m.openShell()
	m.query = ""
	m.selection = 0
	m.scrollOffset = 0
	m.allNames = append([]string(nil), names...)
	m.candidates = append([]string(nil), names...)
	m.currentModel = current
	return m
}

func (m modelPickerOverlay) Close() modelPickerOverlay {
	m.OverlayShell = m.closeShell()
	return m
}

func (m modelPickerOverlay) Update(msg tea.Msg) (modelPickerOverlay, tea.Cmd) {
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

func (m modelPickerOverlay) View() string {
	if !m.IsOpen() {
		return ""
	}

	innerW := m.modelPickerInnerWidth()
	const selectionPrefix = "> "
	const idlePrefix = "  "

	prefix := m.styles.Accent.Render("/model")
	queryDisplay := m.query
	if queryDisplay == "" {
		if len(m.candidates) > 0 {
			queryDisplay = m.styles.Accent.Render(m.candidates[m.selection])
		} else {
			queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search models…")
		}
	}
	headerLine := lipgloss.NewStyle().Width(innerW).Render(prefix + " " + queryDisplay)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerW))

	lines := []string{headerLine, divider}

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	currentStyle := m.styles.Accent

	for i := m.scrollOffset; i < min(m.scrollOffset+maxDisplay, len(m.candidates)); i++ {
		name := m.candidates[i]
		var row string
		if name == m.currentModel {
			row = currentStyle.Render(name)
		} else {
			row = nameStyle.Render(name)
		}
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

func (m modelPickerOverlay) modelPickerInnerWidth() int {
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

func (m modelPickerOverlay) SelectedName() string {
	if m.selection >= 0 && m.selection < len(m.candidates) {
		return m.candidates[m.selection]
	}
	return ""
}
