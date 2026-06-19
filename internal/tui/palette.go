package tui

import (
	"fmt"
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
	OverlayShell
	query        string
	allItems     []paletteItem
	filtered     []paletteItem
	cursor       int
	scrollOffset int
	styles       theme.Styles
}

func newPalette(styles theme.Styles, items []paletteItem) paletteModel {
	p := paletteModel{
		OverlayShell: OverlayShell{}.WithPreferredWidth(60),
		styles:       styles,
		allItems:     items,
	}
	p.filtered = append([]paletteItem(nil), items...)
	return p
}

func (p paletteModel) Open() paletteModel {
	p.OverlayShell = p.openShell()
	p.query = ""
	p.cursor = 0
	p.scrollOffset = 0
	p.filtered = append([]paletteItem(nil), p.allItems...)
	return p
}

func (p paletteModel) Close() paletteModel {
	p.OverlayShell = p.closeShell()
	return p
}

func (p paletteModel) Update(msg tea.Msg) (paletteModel, tea.Cmd) {
	if !p.IsOpen() {
		return p, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}

	// Enter dispatches the selected action and closes. The other pickers handle
	// Enter in their handle*Key functions in the parent model; the palette runs
	// action dispatch inline so the parent's overlay dispatch stays trivial.
	if keyMsg.Type == tea.KeyEnter {
		if p.cursor >= 0 && p.cursor < len(p.filtered) {
			item := p.filtered[p.cursor]
			p = p.Close()
			if item.action != nil {
				return p, item.action()
			}
		}
		return p, nil
	}

	// Ctrl+P closes the palette (updateSearchPicker only handles Esc).
	if keyMsg.Type == tea.KeyCtrlP {
		return p.Close(), nil
	}

	filterFn := func(query string, items []paletteItem) []paletteItem {
		return filterSearchPickerEntries(items, query, func(item paletteItem, loweredQuery string) bool {
			return strings.Contains(strings.ToLower(item.command), loweredQuery) ||
				strings.Contains(strings.ToLower(item.name), loweredQuery) ||
				strings.Contains(strings.ToLower(item.desc), loweredQuery)
		})
	}
	result := updateSearchPicker(&p.query, &p.cursor, &p.scrollOffset, &p.filtered, p.allItems, msg, filterFn)
	switch result {
	case searchPickerClosed:
		return p.Close(), nil
	case searchPickerHandled, searchPickerIgnored:
		return p, nil
	default:
		return p, nil
	}
}

func (p paletteModel) View() string {
	innerWidth := p.InnerWidth()

	// Input row: ⌘ prefix + query or placeholder
	prefix := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("⌘ ")
	queryDisplay := p.query
	if queryDisplay == "" {
		queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("run a command…")
	} else {
		queryDisplay = p.styles.PaletteInput.Render(queryDisplay)
	}
	inputLine := lipgloss.NewStyle().Width(innerWidth).Render(prefix + queryDisplay)
	divider := p.Divider()

	lines := []string{inputLine, divider}
	accentStyle := p.styles.Accent
	fgStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute))
	for i := p.scrollOffset; i < min(p.scrollOffset+maxDisplay, len(p.filtered)); i++ {
		item := p.filtered[i]
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

	if len(p.filtered) > p.scrollOffset+maxDisplay {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render(fmt.Sprintf("… and %d more", len(p.filtered)-(p.scrollOffset+maxDisplay))))
	}

	// Footer
	footerDivider := p.Divider()
	footerText := FooterChip("↵") + " run   " + FooterChip("↑↓") + " navigate   " + FooterChip("esc") + " close"
	footerLine := p.RenderFooter(footerText)
	lines = append(lines, footerDivider, footerLine)

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return p.RenderWithBg(p.styles.PaletteOverlay, body, lipgloss.Color(theme.BgElev))
}
