package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type statusState struct {
	model         string
	turn          int
	context       string
	mode          string
	styles        theme.Styles
	streaming     bool
	promptUsed    int
	contextBudget int
}

func (s statusState) view(width int, hints string) string {
	sep := s.styles.FgMute.Render(" │ ")

	var parts []string

	// Segment 1: model
	if s.model != "" {
		label := s.styles.FgMute.Render("model ")
		val := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.AccentAmber)).Render(s.model)
		parts = append(parts, label+val)
	}

	// Segment 2: turn
	if s.turn > 0 {
		label := s.styles.FgMute.Render("turn ")
		val := s.styles.FgDim.Render(fmt.Sprintf("%d", s.turn))
		parts = append(parts, label+val)
	}

	// Segment 3: ctx
	if s.contextBudget > 0 || s.promptUsed > 0 {
		pct := 0
		if s.contextBudget > 0 {
			pct = (s.promptUsed * 100) / s.contextBudget
		}
		var ctxColor lipgloss.Color
		switch {
		case pct > 90:
			ctxColor = lipgloss.Color(theme.Removed)
		case pct > 70:
			ctxColor = lipgloss.Color(theme.Warn)
		default:
			ctxColor = lipgloss.Color(theme.AccentAmber)
		}
		ctxStr := fmt.Sprintf("%d/%d · %d%%", s.promptUsed, s.contextBudget, pct)
		label := s.styles.FgMute.Render("ctx ")
		val := lipgloss.NewStyle().Foreground(ctxColor).Render(ctxStr)
		parts = append(parts, label+val)
	}

	// Segment 4: send/interrupt (state-dependent)
	if s.streaming {
		chip := s.styles.KeyChip.Render("esc")
		parts = append(parts, chip+" interrupt")
	} else {
		chip := s.styles.KeyChip.Render("⏎")
		parts = append(parts, chip+" send")
	}

	// Segment 5: newline
	parts = append(parts, s.styles.KeyChip.Render("⇧⏎")+" newline")

	// Segment 6: commands
	parts = append(parts, s.styles.KeyChip.Render("^P")+" commands")

	// Segment 7: sidebar
	parts = append(parts, s.styles.KeyChip.Render("^B")+" sidebar")

	// Segment 8: switch model
	parts = append(parts, s.styles.KeyChip.Render("/model")+" switch")

	// Segment 9: help
	parts = append(parts, s.styles.KeyChip.Render("?")+" help")

	text := strings.Join(parts, sep)
	if width > 0 {
		return s.styles.StatusBar.Width(width).Render(text)
	}
	return s.styles.StatusBar.Render(text)
}
