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
	overlayWidth := p.width * 2 / 3
	if overlayWidth < 40 {
		overlayWidth = 40
	}
	if overlayWidth > p.width-4 {
		overlayWidth = p.width - 4
	}

	inputLine := p.styles.PaletteInput.Width(overlayWidth - 2).Render("> " + p.query)

	maxItems := 10
	lines := make([]string, 0, len(p.filtered))
	for i, item := range p.filtered {
		if i >= maxItems {
			break
		}
		label := item.command
		if item.name != "" {
			label += "  " + item.name
		}
		if item.desc != "" {
			label += "  — " + item.desc
		}
		if i == p.cursor {
			lines = append(lines, p.styles.PaletteItemActive.Width(overlayWidth-2).Render(label))
		} else {
			lines = append(lines, p.styles.PaletteItem.Width(overlayWidth-2).Render(label))
		}
	}

	body := lipgloss.JoinVertical(lipgloss.Left, append([]string{inputLine}, lines...)...)
	box := p.styles.PaletteOverlay.
		Width(overlayWidth).
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
