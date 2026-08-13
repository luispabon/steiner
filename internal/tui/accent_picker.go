package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// accentPickerOverlay is a search-and-select overlay for accent colour presets.
type accentPickerOverlay struct {
	OverlayShell
	query        string
	allNames     []string
	candidates   []string
	selection    int
	scrollOffset int
	current      string
	styles       *theme.Styles
}

// newAccentPickerOverlay constructs an uninitialised accent picker.
func newAccentPickerOverlay(styles *theme.Styles) accentPickerOverlay {
	return accentPickerOverlay{styles: styles}
}

// Open opens the picker, initialising entries from AccentPresets plus "random".
func (m accentPickerOverlay) Open(current string) accentPickerOverlay {
	m.OverlayShell = m.openShell()
	m.query = ""
	m.selection = 0
	m.scrollOffset = 0
	m.current = current

	names := make([]string, 0, len(theme.AccentPresets)+1)
	for name := range theme.AccentPresets {
		names = append(names, name)
	}
	slices.Sort(names)
	names = append(names, "random")

	m.allNames = names
	m.candidates = append([]string(nil), names...)

	// Position selection on the current preset.
	for i, name := range m.candidates {
		if name == current {
			m.selection = i
			if i >= maxDisplay {
				m.scrollOffset = i - maxDisplay + 1
			}
			break
		}
	}

	return m
}

// Close closes the picker.
func (m accentPickerOverlay) Close() accentPickerOverlay {
	m.OverlayShell = m.closeShell()
	return m
}

// Update handles key events for search, navigation, and dismissal.
func (m accentPickerOverlay) Update(msg tea.Msg) (accentPickerOverlay, tea.Cmd) {
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

// SelectedName returns the name of the currently highlighted entry.
func (m accentPickerOverlay) SelectedName() string {
	if m.selection >= 0 && m.selection < len(m.candidates) {
		return m.candidates[m.selection]
	}
	return ""
}

// View renders the overlay as a positioned overlay string.
func (m accentPickerOverlay) View() string {
	if !m.IsOpen() {
		return ""
	}
	return m.render(m.accentPickerInnerWidth())
}

func (m accentPickerOverlay) render(innerW int) string {
	const selectionPrefix = "> "
	const idlePrefix = "  "

	lines := make([]string, 0, maxDisplay+5)

	queryDisplay := m.query
	if queryDisplay == "" {
		if name := m.SelectedName(); name != "" {
			queryDisplay = m.styles.Accent.Render(name)
		} else {
			queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search accents…")
		}
	}
	headerLine := lipgloss.NewStyle().Width(innerW).Render(m.styles.Accent.Render("/accent") + " " + queryDisplay)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerW))
	lines = append(lines, headerLine, divider)

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	currentStyle := m.styles.Accent

	for i := m.scrollOffset; i < min(m.scrollOffset+maxDisplay, len(m.candidates)); i++ {
		name := m.candidates[i]

		swatch := m.renderSwatch(name)

		var label string
		if name == m.current {
			label = currentStyle.Render(name)
		} else {
			label = nameStyle.Render(name)
		}

		row := swatch + " " + label
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

// renderSwatch returns a coloured block character for the preset, or a neutral
// indicator for "random".
func (m accentPickerOverlay) renderSwatch(name string) string {
	if name == "random" {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgDim)).Render("~")
	}
	if hex, ok := theme.AccentPresets[name]; ok {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render("█")
	}
	return " "
}

func (m accentPickerOverlay) accentPickerInnerWidth() int {
	const maxOverlayInner = 60
	inner := m.InnerWidth()
	if inner > maxOverlayInner {
		inner = maxOverlayInner
	}
	if inner < 36 {
		inner = 36
	}
	return inner
}
