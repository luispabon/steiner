package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// orchestrationPickerRow is one selectable row in the orchestration picker.
type orchestrationPickerRow struct {
	level config.OrchestrationLevel
	desc  string
}

// orchestrationPickerRows is the fixed, ordered list of orchestration levels
// the picker offers.
var orchestrationPickerRows = []orchestrationPickerRow{
	{level: config.OrchestrationLevelStandard, desc: "steered to delegate"},
	{level: config.OrchestrationLevelLow, desc: "delegation at model's discretion"},
}

// orchestrationPickerOverlay is a fixed two-item list overlay for
// /orchestration: no fuzzy search, just up/down over the two levels.
type orchestrationPickerOverlay struct {
	OverlayShell
	selection int
	current   config.OrchestrationLevel
	styles    *theme.Styles
}

// Open opens the picker, positioning the cursor on current's row.
func (m orchestrationPickerOverlay) Open(current config.OrchestrationLevel) orchestrationPickerOverlay {
	m.OverlayShell = m.openShell()
	m.current = current
	m.selection = 0
	for i, row := range orchestrationPickerRows {
		if row.level == current {
			m.selection = i
			break
		}
	}
	return m
}

// Close closes the picker.
func (m orchestrationPickerOverlay) Close() orchestrationPickerOverlay {
	m.OverlayShell = m.closeShell()
	return m
}

// moveSelection moves the cursor by delta rows, wrapping.
func (m orchestrationPickerOverlay) moveSelection(delta int) orchestrationPickerOverlay {
	count := len(orchestrationPickerRows)
	m.selection = ((m.selection+delta)%count + count) % count
	return m
}

// Selected returns the orchestration level at the current cursor row.
func (m orchestrationPickerOverlay) Selected() config.OrchestrationLevel {
	if m.selection < 0 || m.selection >= len(orchestrationPickerRows) {
		return ""
	}
	return orchestrationPickerRows[m.selection].level
}

// View renders the overlay as a positioned overlay string.
func (m orchestrationPickerOverlay) View() string {
	if !m.IsOpen() {
		return ""
	}
	return m.render(m.orchestrationPickerInnerWidth())
}

func (m orchestrationPickerOverlay) render(innerW int) string {
	const selectionPrefix = "▸ "
	const idlePrefix = "  "
	const nameColumns = 9

	lines := make([]string, 0, len(orchestrationPickerRows)+4)

	headerLine := lipgloss.NewStyle().Width(innerW).Render(m.styles.Accent.Render("/orchestration"))
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerW))
	lines = append(lines, headerLine, divider)

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute))

	for i, row := range orchestrationPickerRows {
		radio := m.styles.FgFaint.Render("○")
		if row.level == m.current {
			radio = m.styles.Accent.Render("●")
		}
		name := nameStyle.Render(fmt.Sprintf("%-*s", nameColumns, string(row.level)))
		text := radio + " " + name + " " + descStyle.Render(row.desc)

		if i == m.selection {
			lines = append(lines, m.styles.PaletteItemActive.
				Width(innerW).
				MaxWidth(innerW).
				Render(m.styles.Accent.Render(selectionPrefix)+text))
		} else {
			lines = append(lines, lipgloss.NewStyle().
				Width(innerW).
				MaxWidth(innerW).
				Render(idlePrefix+text))
		}
	}

	footerText := FooterChip("↑↓") + " move   " + FooterChip("enter") + " select   " + FooterChip("esc") + " close"
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Width(innerW).Render(footerText)
	lines = append(lines, divider, footer)

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	// WithBg is required: nested lipgloss renders emit ANSI resets that clear
	// cell backgrounds in transparent terminals; the box Background() does not
	// re-apply after resets inside the body.
	return theme.WithBg(m.styles.PaletteOverlay.Width(innerW+4).Padding(1, 1).Render(body), theme.BgElev)
}

func (m orchestrationPickerOverlay) orchestrationPickerInnerWidth() int {
	const maxOverlayInner = 60
	inner := m.InnerWidth()
	if inner > maxOverlayInner {
		inner = maxOverlayInner
	}
	if inner < 40 {
		inner = 40
	}
	return inner
}
