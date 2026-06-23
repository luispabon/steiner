package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tui/theme"
)

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

func (s sessionPickerOverlay) withDimensions(width, height int) sessionPickerOverlay {
	s.OverlayShell = s.WithDimensions(width, height)
	return s
}

func (s sessionPickerOverlay) Open(entries []session.IndexEntry) sessionPickerOverlay {
	s.OverlayShell = s.openShell()
	s.query = ""
	s.selection = 0
	s.scrollOffset = 0
	s.allEntries = append([]session.IndexEntry(nil), entries...)
	s.candidates = append([]session.IndexEntry(nil), entries...)
	return s
}

func (s sessionPickerOverlay) Close() sessionPickerOverlay {
	s.OverlayShell = s.closeShell()
	return s
}

func (s sessionPickerOverlay) Update(msg tea.Msg) (sessionPickerOverlay, tea.Cmd) {
	if !s.IsOpen() {
		return s, nil
	}
	switch updateSearchPicker(&s.query, &s.selection, &s.scrollOffset, &s.candidates, s.allEntries, msg, func(query string, entries []session.IndexEntry) []session.IndexEntry {
		return filterSearchPickerEntries(entries, query, func(entry session.IndexEntry, loweredQuery string) bool {
			return strings.Contains(strings.ToLower(entry.Title), loweredQuery)
		})
	}) {
	case searchPickerClosed:
		return s.Close(), nil
	case searchPickerHandled, searchPickerIgnored:
		return s, nil
	default:
		return s, nil
	}
}

func (s sessionPickerOverlay) View() string {
	if !s.IsOpen() {
		return ""
	}

	innerWidth := s.InnerWidth()

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

	for i := s.scrollOffset; i < min(s.scrollOffset+maxDisplay, len(s.candidates)); i++ {
		entry := s.candidates[i]
		row := s.formatSessionRow(entry, innerWidth)
		if i == s.selection {
			lines = append(lines, s.styles.AccentBg.MaxWidth(innerWidth).Render(row))
		} else {
			lines = append(lines, lipgloss.NewStyle().MaxWidth(innerWidth).Render(row))
		}
	}

	if len(s.candidates) > s.scrollOffset+maxDisplay {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render(fmt.Sprintf("… and %d more", len(s.candidates)-(s.scrollOffset+maxDisplay))))
	}

	footerText := FooterChip("↵") + " select   " + FooterChip("f") + " fork   " + FooterChip("↑↓") + " navigate   " + FooterChip("esc") + " close"
	lines = append(lines, s.Divider(), s.RenderFooter(footerText))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return s.RenderWithBg(s.styles.PaletteOverlay, body, lipgloss.Color(theme.BgElev))
}

//nolint:dupl // same row-layout algorithm as oneshotResumePickerOverlay.formatRunRow; types differ
func (s sessionPickerOverlay) formatSessionRow(entry session.IndexEntry, maxWidth int) string {
	datetime := fmt.Sprintf("[ %s ] ", entry.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	modelStr := fmt.Sprintf(" (%s)", entry.Model)
	idSuffix := fmt.Sprintf(" [%s]", entry.ID[len(entry.ID)-8:])
	spacer := " "

	prefixWidth := lipgloss.Width(datetime)
	suffixWidth := lipgloss.Width(modelStr) + lipgloss.Width(spacer) + lipgloss.Width(idSuffix)
	titleMaxWidth := maxWidth - prefixWidth - suffixWidth
	if titleMaxWidth < 10 {
		titleMaxWidth = 10
	}

	title := entry.Title
	if len(title) > titleMaxWidth {
		title = title[:titleMaxWidth-1] + "…"
	}

	return datetime + title + modelStr + spacer + idSuffix
}

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
