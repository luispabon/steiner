package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// profilePickerOverlay is a search-and-select overlay for /profile, listing
// configured profile names with fuzzy filtering.
type profilePickerOverlay struct {
	OverlayShell
	query          string
	allNames       []string
	candidates     []string
	matchIndexes   [][]int
	selection      int
	scrollOffset   int
	currentProfile string
	styles         *theme.Styles
}

// newProfilePickerOverlay constructs an uninitialised profile picker.
func newProfilePickerOverlay(styles *theme.Styles) profilePickerOverlay {
	return profilePickerOverlay{styles: styles}
}

// Open opens the picker, initialising entries from names and positioning the
// selection on current if present.
func (m profilePickerOverlay) Open(names []string, current string) profilePickerOverlay {
	m.OverlayShell = m.openShell()
	m.query = ""
	m.selection = 0
	m.scrollOffset = 0
	m.currentProfile = current
	m.allNames = append([]string(nil), names...)
	m.candidates = append([]string(nil), names...)
	m.matchIndexes = nil

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
func (m profilePickerOverlay) Close() profilePickerOverlay {
	m.OverlayShell = m.closeShell()
	return m
}

// Update handles key events for search, navigation, and dismissal.
func (m profilePickerOverlay) Update(msg tea.Msg) (profilePickerOverlay, tea.Cmd) {
	if !m.IsOpen() {
		return m, nil
	}
	switch updateSearchPicker(&m.query, &m.selection, &m.scrollOffset, &m.candidates, m.allNames, msg, func(query string, entries []string) []string {
		results, indexes := fuzzyMatchProfileNames(entries, query)
		m.matchIndexes = indexes
		return results
	}) {
	case searchPickerClosed:
		return m.Close(), nil
	case searchPickerHandled, searchPickerIgnored:
		return m, nil
	default:
		return m, nil
	}
}

// SelectedName returns the currently highlighted profile name, if any.
func (m profilePickerOverlay) SelectedName() (string, bool) {
	if m.selection >= 0 && m.selection < len(m.candidates) {
		return m.candidates[m.selection], true
	}
	return "", false
}

// View renders the overlay as a positioned overlay string.
func (m profilePickerOverlay) View() string {
	if !m.IsOpen() {
		return ""
	}
	return m.render(m.profilePickerInnerWidth())
}

func (m profilePickerOverlay) render(innerW int) string {
	const selectionPrefix = "> "
	const idlePrefix = "  "

	lines := make([]string, 0, maxDisplay+5)

	queryDisplay := m.query
	if queryDisplay == "" {
		if name, ok := m.SelectedName(); ok {
			queryDisplay = m.styles.Accent.Render(name)
		} else {
			queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search profiles…")
		}
	}
	headerLine := lipgloss.NewStyle().Width(innerW).Render(m.styles.Accent.Render("/profile") + " " + queryDisplay)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerW))
	lines = append(lines, headerLine, divider)

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	currentStyle := m.styles.Accent

	for i := m.scrollOffset; i < min(m.scrollOffset+maxDisplay, len(m.candidates)); i++ {
		name := m.candidates[i]
		matchedIndexes := []int(nil)
		if i < len(m.matchIndexes) {
			matchedIndexes = m.matchIndexes[i]
		}
		baseStyle := nameStyle
		if name == m.currentProfile {
			baseStyle = currentStyle
		}
		row := renderMatchedText(name, matchedIndexes, baseStyle, m.styles.AccentColor)
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

func (m profilePickerOverlay) profilePickerInnerWidth() int {
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

func fuzzyMatchProfileNames(names []string, query string) ([]string, [][]int) {
	q := strings.TrimSpace(query)
	if q == "" {
		return append([]string(nil), names...), make([][]int, len(names))
	}

	type scoredMatch struct {
		name           string
		matchedIndexes []int
		score          int
	}

	lowerQ := strings.ToLower(q)
	scored := make([]scoredMatch, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}

		matches := fuzzy.Find(q, []string{name})
		if len(matches) == 0 {
			continue
		}
		match := matches[0]
		scored = append(scored, scoredMatch{
			name:           name,
			matchedIndexes: append([]int(nil), match.MatchedIndexes...),
			score:          scoreStringMatch(name, lowerQ, match.MatchedIndexes),
		})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	results := make([]string, 0, len(scored))
	indexes := make([][]int, 0, len(scored))
	for _, match := range scored {
		results = append(results, match.name)
		indexes = append(indexes, match.matchedIndexes)
	}
	return results, indexes
}
