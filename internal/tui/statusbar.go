package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type statusState struct {
	model   string
	turn    int
	context string
	mode    string
}

func (s statusState) view(width int, hints string) string {
	parts := make([]string, 0, 5)
	if s.model != "" {
		parts = append(parts, "model "+s.model)
	}
	if s.turn > 0 {
		parts = append(parts, fmt.Sprintf("turn %d", s.turn))
	}
	if s.context != "" {
		parts = append(parts, s.context)
	}
	if s.mode != "" {
		parts = append(parts, s.mode)
	}
	if hints != "" {
		parts = append(parts, hints)
	}
	text := strings.Join(parts, " | ")

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("24")).
		Padding(0, 1)
	if width > 0 {
		style = style.Width(width)
	}
	return style.Render(text)
}
