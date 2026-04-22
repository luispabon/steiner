package tui

import (
	"fmt"
	"strings"
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
	if width > 0 {
		return statusBarStyle.Width(width).Render(text)
	}
	return statusBarStyle.Render(text)
}
