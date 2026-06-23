package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func renderHelp(styles theme.Styles, width int) string {
	// Constrain panel width
	panelWidth := width
	if panelWidth > 50 {
		panelWidth = 50
	}
	if panelWidth < 20 {
		panelWidth = 20
	}

	type binding struct {
		key  string
		desc string
	}
	type group struct {
		title    string
		bindings []binding
	}

	groups := []group{
		{
			title: "NAVIGATION",
			bindings: []binding{
				{"pgup / pgdn", "scroll"},
				{"mouse wheel", "scroll"},
			},
		},
		{
			title: "INPUT",
			bindings: []binding{
				{"enter", "submit"},
				{"shift+enter", "insert newline"},
				{"up / down", "command history"},
				{"/", "open slash overlay (commands & skills)"},
				{"/skillname", "invoke skill directly"},
				{"ctrl+a / ctrl+e", "line start / end"},
				{"ctrl+k", "delete to end"},
				{"ctrl+w", "delete word back"},
				{"ctrl+u", "delete to start"},
			},
		},
		{
			title: "SESSION",
			bindings: func() []binding {
				var bb []binding
				bb = append(bb,
					binding{"ctrl+b", "toggle sidebar"},
					binding{"ctrl+t", "inspect context"},
					binding{"ctrl+x", "toggle delegation output"},
				)
				for _, hb := range projectHelpLines() {
					bb = append(bb, binding(hb))
				}
				bb = append(bb, binding{"?", "toggle help"})
				return bb
			}(),
		},
		{
			title: "APPROVAL",
			bindings: []binding{
				{"y / yes", "approve"},
				{"n / no", "deny"},
			},
		},
	}

	var sb strings.Builder
	headingStyle := lipgloss.NewStyle().
		Foreground(styles.AccentColor).
		Bold(true)
	keyStyle := lipgloss.NewStyle().
		Foreground(styles.SidebarLabel.GetForeground())

	for i, g := range groups {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(headingStyle.Render(g.title))
		sb.WriteString("\n")
		for _, b := range g.bindings {
			line := fmt.Sprintf("  %-18s %s", keyStyle.Render(b.key), b.desc)
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}

	panel := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Border(lipgloss.NormalBorder()).
		BorderForeground(styles.Border.GetForeground()).
		Foreground(styles.ContentPane.GetForeground()).
		Width(panelWidth).
		Padding(1, 2).
		Render(sb.String())

	return theme.WithBg(panel, lipgloss.Color(theme.BgElev))
}
