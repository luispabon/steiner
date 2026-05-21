package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const slashOverlayMaxDisplay = 10

// slashOverlayItem represents a single command or skill in the slash overlay.
type slashOverlayItem struct {
	command string // e.g. "/clear", "/skillname"
	name    string // e.g. "Clear conversation", "Awesome Skill"
	desc    string // e.g. "reset the current session", "does cool things"
	source  string // "" for built-in commands, "project"/"user"/"global" for skills
	isSkill bool
}

// slashOverlay is a bottom-anchored overlay that appears when the user types "/" and
// provides quick access to built-in commands and available skills.
type slashOverlay struct {
	OverlayShell
	query        string
	allItems     []slashOverlayItem
	candidates   []slashOverlayItem
	selection    int
	scrollOffset int
	styles       theme.Styles
}

func newSlashOverlay(styles theme.Styles) slashOverlay {
	return slashOverlay{styles: styles}
}

// Open initializes the slash overlay with items and opens it.
// items should be built-in commands + skills.
func (s slashOverlay) Open(items []slashOverlayItem) slashOverlay {
	s.OverlayShell = s.openShell()
	s.query = ""
	s.selection = 0
	s.scrollOffset = 0
	s.allItems = append([]slashOverlayItem(nil), items...)
	s.candidates = append([]slashOverlayItem(nil), s.allItems...)
	return s
}

// Close closes the slash overlay.
func (s slashOverlay) Close() slashOverlay {
	s.OverlayShell = s.closeShell()
	return s
}

func (s *slashOverlay) syncQuery(query string) {
	s.query = query
	s.filterCandidates()
}

// Update handles key input for the overlay.
func (s slashOverlay) Update(msg tea.Msg) (slashOverlay, tea.Cmd) {
	if !s.IsOpen() {
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
		// Selection will be handled by the parent
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
			s.filterCandidates()
		}
		return s, nil
	case tea.KeyRunes:
		s.query += keyMsg.String()
		s.filterCandidates()
		return s, nil
	default:
		return s, nil
	}
}

// filterCandidates updates the list of candidates based on the current query.
// Matches command, name, and desc fields.
func (s *slashOverlay) filterCandidates() {
	s.scrollOffset = 0
	s.selection = 0

	q := strings.ToLower(strings.TrimSpace(s.query))
	q = strings.TrimPrefix(q, "/")
	if q == "" {
		s.candidates = append([]slashOverlayItem(nil), s.allItems...)
		return
	}

	s.candidates = nil
	for _, item := range s.allItems {
		if strings.Contains(strings.ToLower(item.command), q) ||
			strings.Contains(strings.ToLower(item.name), q) ||
			strings.Contains(strings.ToLower(item.desc), q) {
			s.candidates = append(s.candidates, item)
		}
	}
}

// scrollIntoView adjusts the scroll offset to ensure the selection is visible.
func (s *slashOverlay) scrollIntoView() {
	if s.selection >= s.scrollOffset+slashOverlayMaxDisplay {
		s.scrollOffset = s.selection - slashOverlayMaxDisplay + 1
	}
	if s.selection < s.scrollOffset {
		s.scrollOffset = s.selection
	}
	if s.scrollOffset < 0 {
		s.scrollOffset = 0
	}
}

// SelectedItem returns the currently selected item, or nil if none.
func (s slashOverlay) SelectedItem() *slashOverlayItem {
	if s.selection >= 0 && s.selection < len(s.candidates) {
		return &s.candidates[s.selection]
	}
	return nil
}

// slashOverlayInnerWidth computes the inner content width for the overlay,
// capped at 90 to keep it compact above the prompt box.
func (s slashOverlay) slashOverlayInnerWidth() int {
	const maxOverlayInner = 90
	inner := s.InnerWidth()
	if inner > maxOverlayInner {
		inner = maxOverlayInner
	}
	if inner < 40 {
		inner = 40
	}
	return inner
}

// View renders the overlay.
func (s slashOverlay) View() string {
	if !s.IsOpen() {
		return ""
	}

	innerW := s.slashOverlayInnerWidth()
	const selectionPrefix = "> "
	const idlePrefix = "  "
	contentW := max(0, innerW-lipgloss.Width(selectionPrefix)-5)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerW))

	// Header showing the slash prefix and query
	prefix := s.styles.Accent.Render("/")
	queryDisplay := strings.TrimPrefix(s.query, "/")
	if queryDisplay == "" {
		queryDisplay = lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.FgMute)).
			Render("commands & skills…")
	}
	headerLine := lipgloss.NewStyle().Width(innerW).Render(prefix + " " + queryDisplay)

	lines := []string{headerLine, divider}

	// Render candidate items
	cmdStyle := s.styles.Accent
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute))
	sourceStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgFaint)).Italic(true)

	for i := s.scrollOffset; i < min(s.scrollOffset+slashOverlayMaxDisplay, len(s.candidates)); i++ {
		item := s.candidates[i]

		var row string
		var plainRow string
		if item.isSkill {
			plainParts := []string{item.command}
			parts := []string{cmdStyle.Render(item.command)}
			descWidth := contentW - lipgloss.Width(item.command)
			if item.desc != "" && descWidth > 2 {
				desc := truncateOverlayText(item.desc, descWidth-2)
				plainParts = append(plainParts, desc)
				parts = append(parts, descStyle.Render(desc))
			}
			plainRow = strings.Join(plainParts, "  ")
			row = strings.Join(parts, "  ")
		} else {
			parts := []string{cmdStyle.Render(item.command)}
			if item.name != "" {
				parts = append(parts, nameStyle.Render(item.name))
			}
			if item.desc != "" {
				parts = append(parts, "|", descStyle.Render(item.desc))
			}
			if item.source != "" {
				parts = append(parts, sourceStyle.Render("["+item.source+"]"))
			}
			row = strings.Join(parts, "  ")
		}

		if i == s.selection {
			if item.isSkill {
				lines = append(lines, s.renderSkillRow(s.styles.Accent.Render(selectionPrefix), row, selectionPrefix+plainRow, innerW))
			} else {
				lines = append(lines, lipgloss.NewStyle().
					Width(innerW).
					MaxWidth(innerW).
					Render(s.styles.Accent.Render(selectionPrefix)+row))
			}
		} else {
			if item.isSkill {
				lines = append(lines, s.renderSkillRow(idlePrefix, row, idlePrefix+plainRow, innerW))
			} else {
				lines = append(lines, lipgloss.NewStyle().
					Width(innerW).
					MaxWidth(innerW).
					Render(idlePrefix+row))
			}
		}
	}

	if len(s.candidates) > s.scrollOffset+slashOverlayMaxDisplay {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.FgMute)).
			Render(fmt.Sprintf("... and %d more", len(s.candidates)-(s.scrollOffset+slashOverlayMaxDisplay))))
	}

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	rendered := s.styles.PaletteOverlay.Width(innerW+2).Padding(1, 1).Render(body)
	return theme.WithBg(rendered, lipgloss.Color(theme.BgElev))
}

func truncateOverlayText(text string, width int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if width <= 0 || text == "" {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(text)
	for len(runes) > 0 {
		candidate := string(runes) + "…"
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

func (s slashOverlay) renderSkillRow(prefixRendered, rowRendered, plain string, width int) string {
	pad := max(0, width-lipgloss.Width(plain))
	return prefixRendered + rowRendered + strings.Repeat(" ", pad)
}
