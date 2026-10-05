package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func (s sidebarState) branchLine(width int) string {
	branch := strings.TrimSpace(s.branch)
	if branch == "" {
		branch = "n/a"
	}
	maxBranch := max(1, width-7)
	if s.dirty {
		maxBranch = max(1, maxBranch-2)
	}
	branchText := fitText(branch, maxBranch)
	line := cardField("branch", lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)), branchText, s.styles)
	if s.dirty {
		warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn)).Background(lipgloss.Color(s.styles.Palette.SidebarBG))
		spaceBgStyle := lipgloss.NewStyle().Background(lipgloss.Color(s.styles.Palette.SidebarBG))
		line += spaceBgStyle.Render(" ") + warnStyle.Render("●")
	}
	return line
}

func (s sidebarState) modifiedFileLine(file gitModifiedFile, width int) string {
	glyph := file.Status
	if glyph == "" {
		glyph = "M"
	}
	var glyphStyle lipgloss.Style
	switch glyph {
	case "A":
		glyphStyle = s.styles.Added
	case "D":
		glyphStyle = s.styles.Removed
	case "U":
		glyphStyle = s.styles.FgMute
	default:
		glyphStyle = s.styles.Warn
	}

	spaceBgStyle := lipgloss.NewStyle().Background(lipgloss.Color(s.styles.Palette.SidebarBG))

	var addedText, deletedText string
	if file.Added > 0 {
		addedText = fmt.Sprintf("+%d", file.Added)
	}
	if file.Deleted > 0 {
		deletedText = fmt.Sprintf("-%d", file.Deleted)
	}

	statsText := ""
	statsLen := 0
	if addedText != "" {
		statsText += s.styledWithBg(s.styles.Added, addedText)
		statsLen += len(addedText)
	}
	if deletedText != "" {
		if statsText != "" {
			statsText += spaceBgStyle.Render(" ")
			statsLen++
		}
		statsText += s.styledWithBg(s.styles.Removed, deletedText)
		statsLen += len(deletedText)
	}

	pathWidth := max(1, width-3-statsLen-1)
	path := fitTextMiddle(file.Path, pathWidth)
	glyphWithBg := glyphStyle.Background(lipgloss.Color(s.styles.Palette.SidebarBG))
	line := glyphWithBg.Render(glyph) + spaceBgStyle.Render(" ") + s.styledWithBg(s.styles.FgDim, path)
	if statsText != "" {
		padding := max(1, width-2-lipgloss.Width(path)-statsLen)
		line += spaceBgStyle.Render(strings.Repeat(" ", padding)) + statsText
	}
	return line
}
