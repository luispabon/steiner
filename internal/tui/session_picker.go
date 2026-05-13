package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// sessionPickerOverlay is a reusable session selection overlay following
// the filePickerOverlay pattern with searchable list, keyboard navigation,
// and session selection.
type sessionPickerOverlay struct {
	OverlayShell
	query        string
	allEntries   []session.IndexEntry
	candidates   []session.IndexEntry
	selection    int
	scrollOffset int
	styles       theme.Styles
}

func newSessionPickerOverlay(styles theme.Styles) sessionPickerOverlay {
	return sessionPickerOverlay{styles: styles}
}

// withDimensions returns a copy of the overlay with updated terminal dimensions.
func (s sessionPickerOverlay) withDimensions(width, height int) sessionPickerOverlay {
	s.OverlayShell = s.WithDimensions(width, height)
	return s
}

// Open loads sessions from the store and opens the overlay.
func (s sessionPickerOverlay) Open(entries []session.IndexEntry) sessionPickerOverlay {
	s.OverlayShell = s.openShell()
	s.query = ""
	s.selection = 0
	s.scrollOffset = 0
	s.allEntries = append([]session.IndexEntry(nil), entries...)
	s.candidates = append([]session.IndexEntry(nil), entries...)
	return s
}

// Close closes the overlay and resets state.
func (s sessionPickerOverlay) Close() sessionPickerOverlay {
	s.OverlayShell = s.closeShell()
	return s
}

// Update handles keyboard input: Esc to close, Enter to confirm,
// arrow keys to navigate, typing to filter.
func (s sessionPickerOverlay) Update(msg tea.Msg) (sessionPickerOverlay, tea.Cmd) {
	if !s.open {
		return s, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	switch keyMsg.Type {
	case tea.KeyEsc:
		return s.Close(), nil
	case tea.KeyEnter:
		return s, nil
	case tea.KeyUp:
		if s.selection > 0 {
			s.selection--
		}
		s.scrollIntoView()
		return s, nil
	case tea.KeyDown:
		if s.selection < len(s.candidates)-1 {
			s.selection++
		}
		s.scrollIntoView()
		return s, nil
	case tea.KeyBackspace:
		if len(s.query) > 0 {
			s.query = s.query[:len(s.query)-1]
			s.filter()
		}
		return s, nil
	case tea.KeyRunes:
		s.query += keyMsg.String()
		s.filter()
		s.selection = 0
		return s, nil
	}
	return s, nil
}

// filter updates the candidates list based on the current query,
// matching case-insensitively against session titles.
func (s *sessionPickerOverlay) filter() {
	s.scrollOffset = 0
	s.selection = 0
	q := strings.ToLower(s.query)
	if q == "" {
		s.candidates = append([]session.IndexEntry(nil), s.allEntries...)
		return
	}
	var result []session.IndexEntry
	for _, entry := range s.allEntries {
		if strings.Contains(strings.ToLower(entry.Title), q) {
			result = append(result, entry)
		}
	}
	s.candidates = result
}

// View renders the session picker overlay showing title, model, updated_at,
// and session ID suffix for each entry.
func (s sessionPickerOverlay) View() string {
	if !s.open {
		return ""
	}

	innerWidth := s.InnerWidth()

	// Header: search box showing query or default text
	prefix := s.styles.Accent.Render("▶")
	queryDisplay := s.query
	if queryDisplay == "" {
		if len(s.candidates) > 0 {
			queryDisplay = s.styles.Accent.Render(s.candidates[s.selection].Title)
		} else {
			queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search sessions…")
		}
	}
	headerLine := lipgloss.NewStyle().Width(innerWidth).Render(prefix + " " + queryDisplay)
	divider := s.Divider()

	lines := []string{headerLine, divider}

	// Render up to maxDisplay entries
	for i := s.scrollOffset; i < min(s.scrollOffset+maxDisplay, len(s.candidates)); i++ {
		entry := s.candidates[i]
		row := s.formatSessionRow(entry, innerWidth)
		if i == s.selection {
			lines = append(lines, s.styles.AccentBg.MaxWidth(innerWidth).Render(row))
		} else {
			lines = append(lines, lipgloss.NewStyle().MaxWidth(innerWidth).Render(row))
		}
	}

	// Show "more" indicator if there are hidden entries
	if len(s.candidates) > s.scrollOffset+maxDisplay {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render(fmt.Sprintf("… and %d more", len(s.candidates)-(s.scrollOffset+maxDisplay))))
	}

	footerText := FooterChip("↵") + " select   " + FooterChip("↑↓") + " navigate   " + FooterChip("esc") + " close"
	lines = append(lines, s.Divider(), s.RenderFooter(footerText))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return theme.WithBg(s.Render(overlayStyles{box: s.styles.PaletteOverlay}, body), lipgloss.Color(theme.BgElev))
}

// formatSessionRow formats a single session entry as: title (model) updated_at [id suffix]
// Truncates title to fit within available width.
func (s sessionPickerOverlay) formatSessionRow(entry session.IndexEntry, maxWidth int) string {
	// Calculate space available for title after model, time, and ID suffix
	// Format: "title (model) 2h ago [abcd1234]"
	modelStr := fmt.Sprintf(" (%s)", entry.Model)
	relTime := relativeTime(entry.UpdatedAt)
	idSuffix := fmt.Sprintf(" [%s]", entry.ID[len(entry.ID)-8:])
	spacer := " "

	// Account for spacing and suffix parts
	suffixWidth := lipgloss.Width(modelStr) + lipgloss.Width(spacer) + lipgloss.Width(relTime) + lipgloss.Width(spacer) + lipgloss.Width(idSuffix)
	titleMaxWidth := maxWidth - suffixWidth
	if titleMaxWidth < 10 {
		titleMaxWidth = 10
	}

	title := entry.Title
	if len(title) > titleMaxWidth {
		title = title[:titleMaxWidth-1] + "…"
	}

	return title + modelStr + spacer + relTime + spacer + idSuffix
}

// relativeTime returns a human-readable relative time string like "2h ago" or "5m ago".
func relativeTime(t time.Time) string {
	elapsed := time.Since(t)
	if elapsed < time.Minute {
		return "now"
	}
	if elapsed < time.Hour {
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	}
	if elapsed < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	}
	if elapsed < 30*24*time.Hour {
		return fmt.Sprintf("%dd ago", int(elapsed.Hours()/24))
	}
	return "old"
}

// scrollIntoView adjusts scrollOffset so the current selection is visible.
func (s *sessionPickerOverlay) scrollIntoView() {
	if s.selection >= s.scrollOffset+maxDisplay {
		s.scrollOffset = s.selection - maxDisplay + 1
	}
	if s.selection < s.scrollOffset {
		s.scrollOffset = s.selection
	}
}
