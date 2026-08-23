package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type modelPickerOverlay struct {
	OverlayShell
	query        string
	allEntries   []ModelEntry
	candidates   []ModelEntry
	selection    int
	scrollOffset int
	currentModel string
	title        string
	mode         modelPickerMode
	styles       *theme.Styles
}

type modelPickerMode int

const (
	modelPickerModeCommand modelPickerMode = iota
	modelPickerModeWorkflowHandoff
)

func newModelPickerOverlay(styles *theme.Styles) modelPickerOverlay {
	return modelPickerOverlay{styles: styles}
}

func (m modelPickerOverlay) Open(names []string, current string) modelPickerOverlay {
	entries := make([]ModelEntry, len(names))
	for i, name := range names {
		entries[i] = ModelEntry{Ref: name, Display: name}
	}
	return m.OpenEntries(entries, current)
}

func (m modelPickerOverlay) OpenEntries(entries []ModelEntry, current string) modelPickerOverlay {
	return m.open(entries, current, "", modelPickerModeCommand)
}

func (m modelPickerOverlay) OpenForWorkflowHandoff(title string, names []string, current string) modelPickerOverlay {
	entries := make([]ModelEntry, len(names))
	for i, name := range names {
		entries[i] = ModelEntry{Ref: name, Display: name}
	}
	return m.open(entries, current, title, modelPickerModeWorkflowHandoff)
}

func (m modelPickerOverlay) open(entries []ModelEntry, current string, title string, mode modelPickerMode) modelPickerOverlay {
	m.OverlayShell = m.openShell()
	m.query = ""
	m.selection = 0
	m.scrollOffset = 0
	m.allEntries = cloneModelEntries(entries)
	m.candidates = cloneModelEntries(entries)
	m.currentModel = current
	m.title = strings.TrimSpace(title)
	m.mode = mode
	return m.withCurrentSelection(current)
}

func (m modelPickerOverlay) ReplaceEntries(entries []ModelEntry) modelPickerOverlay {
	selectedRef := m.SelectedRef()
	m.allEntries = cloneModelEntries(entries)
	m.candidates = filterModelEntries(m.allEntries, m.query)
	m.selection = 0
	m.scrollOffset = 0
	for i, entry := range m.candidates {
		if entry.Ref == selectedRef {
			m.selection = i
			break
		}
	}
	if m.selection >= len(m.candidates) {
		m.selection = max(0, len(m.candidates)-1)
	}
	scrollSearchPickerIntoView(&m.selection, &m.scrollOffset)
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
	switch updateSearchPicker(&m.query, &m.selection, &m.scrollOffset, &m.candidates, m.allEntries, msg, func(query string, entries []ModelEntry) []ModelEntry {
		return filterModelEntries(entries, query)
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

	return m.render(m.modelPickerInnerWidth())
}

func (m modelPickerOverlay) ViewAttached(maxWidth int) string {
	if !m.IsOpen() {
		return ""
	}

	return m.render(m.modelPickerAttachedInnerWidth(maxWidth))
}

func (m modelPickerOverlay) render(innerW int) string {
	const selectionPrefix = "> "
	const idlePrefix = "  "

	lines := make([]string, 0, maxDisplay+5)
	if m.mode == modelPickerModeWorkflowHandoff {
		title := m.title
		if title == "" {
			title = "Select model"
		}
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Fg)).
			Bold(true).
			Width(innerW).
			Render(title))
	}

	queryDisplay := m.query
	if queryDisplay == "" {
		if name := m.SelectedName(); name != "" {
			queryDisplay = m.styles.Accent.Render(name)
		} else {
			queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search models…")
		}
	}
	headerLine := lipgloss.NewStyle().Width(innerW).Render(m.headerPrefix() + " " + queryDisplay)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerW))

	lines = append(lines, headerLine, divider)

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	currentStyle := m.styles.Accent

	for i := m.scrollOffset; i < min(m.scrollOffset+maxDisplay, len(m.candidates)); i++ {
		entry := m.candidates[i]
		name := entry.Display
		if name == "" {
			name = entry.Ref
		}
		var row string
		if entry.Current || entry.Ref == m.currentModel {
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
	// WithBg is required: nested lipgloss renders emit ANSI resets that clear
	// cell backgrounds in transparent terminals; the box Background() does not
	// re-apply after resets inside the body.
	return theme.WithBg(m.styles.PaletteOverlay.Width(innerW+4).Padding(1, 1).Render(body), theme.BgElev)
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

func (m modelPickerOverlay) modelPickerAttachedInnerWidth(maxWidth int) int {
	inner := maxWidth - 6
	if inner > 90 {
		inner = 90
	}
	if inner < 40 {
		inner = 40
	}
	return inner
}

func (m modelPickerOverlay) SelectedName() string {
	if m.selection >= 0 && m.selection < len(m.candidates) {
		name := m.candidates[m.selection].Display
		if name == "" {
			return m.candidates[m.selection].Ref
		}
		return name
	}
	return ""
}

func (m modelPickerOverlay) SelectedRef() string {
	if m.selection >= 0 && m.selection < len(m.candidates) {
		return m.candidates[m.selection].Ref
	}
	return ""
}

func (m modelPickerOverlay) SelectedEntry() (ModelEntry, bool) {
	if m.selection >= 0 && m.selection < len(m.candidates) {
		return m.candidates[m.selection], true
	}
	return ModelEntry{}, false
}

func (m modelPickerOverlay) IsWorkflowHandoff() bool {
	return m.mode == modelPickerModeWorkflowHandoff
}

func (m modelPickerOverlay) withCurrentSelection(current string) modelPickerOverlay {
	for i, entry := range m.candidates {
		if entry.Ref != current {
			continue
		}
		m.selection = i
		if i >= maxDisplay {
			m.scrollOffset = i - maxDisplay + 1
		}
		break
	}
	return m
}

func (m modelPickerOverlay) headerPrefix() string {
	if m.mode == modelPickerModeWorkflowHandoff {
		return m.styles.Accent.Render("search")
	}
	return m.styles.Accent.Render("/model")
}

func filterModelEntries(entries []ModelEntry, query string) []ModelEntry {
	return filterSearchPickerEntries(entries, query, func(entry ModelEntry, loweredQuery string) bool {
		return strings.Contains(strings.ToLower(entry.Display), loweredQuery) ||
			strings.Contains(strings.ToLower(entry.Ref), loweredQuery)
	})
}
