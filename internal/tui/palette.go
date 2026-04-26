package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type paletteAction func() tea.Cmd

type paletteItem struct {
	command string
	name    string
	desc    string
	action  paletteAction
}

type paletteModel struct {
	open     bool
	query    string
	items    []paletteItem
	filtered []paletteItem
	cursor   int
	styles   theme.Styles
	width    int
	height   int
}

func newPalette(styles theme.Styles, items []paletteItem) paletteModel {
	p := paletteModel{
		styles: styles,
		items:  items,
	}
	p.filtered = append([]paletteItem(nil), items...)
	return p
}

func (p paletteModel) Open() paletteModel {
	p.open = true
	p.query = ""
	p.cursor = 0
	p.filtered = append([]paletteItem(nil), p.items...)
	return p
}

func (p paletteModel) Close() paletteModel {
	p.open = false
	return p
}

func (p paletteModel) Update(msg tea.Msg) (paletteModel, tea.Cmd) {
	if !p.open {
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch keyMsg.Type {
	case tea.KeyEsc, tea.KeyCtrlP:
		return p.Close(), nil
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
		return p, nil
	case tea.KeyDown:
		if p.cursor < len(p.filtered)-1 {
			p.cursor++
		}
		return p, nil
	case tea.KeyEnter:
		if p.cursor >= 0 && p.cursor < len(p.filtered) {
			item := p.filtered[p.cursor]
			p = p.Close()
			if item.action != nil {
				return p, item.action()
			}
		}
		return p, nil
	case tea.KeyBackspace:
		if len(p.query) > 0 {
			p.query = p.query[:len(p.query)-1]
			p.filter()
		}
		return p, nil
	case tea.KeyRunes:
		p.query += keyMsg.String()
		p.filter()
		return p, nil
	}
	return p, nil
}

func (p paletteModel) View() string {
	// Total visual width of the modal (including border)
	overlayWidth := 60
	if overlayWidth > p.width-4 {
		overlayWidth = p.width - 4
	}
	if overlayWidth < 40 {
		overlayWidth = 40
	}
	// Inner content area: subtract 1-cell border + 1-cell padding each side
	innerWidth := overlayWidth - 4

	// Input row: ⌘ prefix + query or placeholder
	prefix := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("⌘ ")
	queryDisplay := p.query
	if queryDisplay == "" {
		queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("run a command…")
	} else {
		queryDisplay = p.styles.PaletteInput.Render(queryDisplay)
	}
	inputLine := lipgloss.NewStyle().Width(innerWidth).Render(prefix + queryDisplay)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerWidth))

	maxItems := 10
	lines := []string{inputLine, divider}
	accentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.AccentAmber))
	fgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute))
	for i, item := range p.filtered {
		if i >= maxItems {
			break
		}
		left := accentStyle.Render(item.command)
		if item.name != "" {
			left += "  " + fgStyle.Render(item.name)
		}
		right := descStyle.Render(item.desc)
		leftW := lipgloss.Width(left)
		rightW := lipgloss.Width(right)
		gap := innerWidth - leftW - rightW
		if gap < 1 {
			gap = 1
		}
		row := left + strings.Repeat(" ", gap) + right
		if i == p.cursor {
			lines = append(lines, p.styles.PaletteItemActive.Width(innerWidth).Render(row))
		} else {
			lines = append(lines, lipgloss.NewStyle().Width(innerWidth).Render(row))
		}
	}

	// Footer
	footerDivider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerWidth))
	chip := func(k string) string {
		return lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev2)).Foreground(lipgloss.Color(theme.FgFaint)).Padding(0, 1).Render(k)
	}
	footerText := chip("↵") + " run   " + chip("↑↓") + " navigate   " + chip("esc") + " close"
	footerLine := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Width(innerWidth).Render(footerText)
	lines = append(lines, footerDivider, footerLine)

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	box := p.styles.PaletteOverlay.
		Width(innerWidth).
		Padding(1, 1).
		Render(body)

	return lipgloss.Place(
		p.width, p.height,
		lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
	)
}

func (p *paletteModel) filter() {
	q := strings.ToLower(p.query)
	if q == "" {
		p.filtered = append([]paletteItem(nil), p.items...)
		if p.cursor >= len(p.filtered) {
			p.cursor = 0
		}
		return
	}
	result := p.filtered[:0]
	for _, item := range p.items {
		if strings.Contains(strings.ToLower(item.command), q) ||
			strings.Contains(strings.ToLower(item.name), q) ||
			strings.Contains(strings.ToLower(item.desc), q) {
			result = append(result, item)
		}
	}
	p.filtered = result
	if p.cursor >= len(p.filtered) {
		p.cursor = 0
	}
}
