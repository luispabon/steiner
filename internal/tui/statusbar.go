package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type statusState struct {
	model          string
	turn           int
	context        string
	mode           string
	styles         theme.Styles
	streaming      bool
	approvalActive bool
	promptUsed     int
	contextBudget  int
}

func (s statusState) view(width int) string {
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(" │ ")

	var parts []string

	// Segment 1: model
	if s.model != "" {
		label := s.styles.FgMute.Render("model ")
		val := lipgloss.NewStyle().Foreground(s.styles.AccentColor).Render(s.model)
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
			ctxColor = s.styles.AccentColor
		}
		ctxStr := fmt.Sprintf("%d/%d · %d%%", s.promptUsed, s.contextBudget, pct)
		label := s.styles.FgMute.Render("ctx ")
		val := lipgloss.NewStyle().Foreground(ctxColor).Render(ctxStr)
		parts = append(parts, label+val)
	}

	// Segment 4: mode
	if s.mode != "" {
		label := s.styles.FgMute.Render("mode ")
		val := s.styles.FgDim.Render(s.mode)
		parts = append(parts, label+val)
	}

	// Segment 5: action hints (state-dependent)
	if s.approvalActive {
		parts = append(parts, s.styles.KeyChip.Render("tab")+" choice")
		parts = append(parts, s.styles.KeyChip.Render("⏎")+" confirm")
		parts = append(parts, s.styles.KeyChip.Render("esc")+" deny")
	} else if s.streaming {
		chip := s.styles.KeyChip.Render("esc")
		label := s.styles.Accent.Render("interrupt")
		parts = append(parts, chip+" "+label)
	} else {
		chip := s.styles.KeyChip.Render("⏎")
		parts = append(parts, chip+" send")
	}

	// Segment 6: newline
	parts = append(parts, s.styles.KeyChip.Render("⇧⏎")+" newline")

	// Segment 7: commands
	parts = append(parts, s.styles.KeyChip.Render("^P")+" commands")

	// Segment 8: sidebar
	parts = append(parts, s.styles.KeyChip.Render("^B")+" sidebar")

	// Segment 9: switch model
	parts = append(parts, s.styles.KeyChip.Render("/model")+" switch")

	// Segment 10: help
	parts = append(parts, s.styles.KeyChip.Render("?")+" help")

	text := strings.Join(parts, sep)
	if width > 0 && lipgloss.Width(text) > width {
		for len(parts) > 1 {
			parts = parts[:len(parts)-1]
			text = strings.Join(parts, sep)
			if lipgloss.Width(text) <= width {
				break
			}
		}
	}
	if width > 0 {
		return s.styles.StatusBar.Width(width).Render(text)
	}
	return s.styles.StatusBar.Render(text)
}
