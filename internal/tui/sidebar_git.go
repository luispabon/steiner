package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

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
		warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn)).Background(lipgloss.Color(theme.Black))
		spaceBgStyle := lipgloss.NewStyle().Background(lipgloss.Color(theme.Black))
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

	statsText := ""
	spaceBgStyle := lipgloss.NewStyle().Background(lipgloss.Color(theme.Black))
	if file.Added > 0 {
		statsText += s.styledWithBg(s.styles.Added, fmt.Sprintf("+%d", file.Added))
	}
	if file.Deleted > 0 {
		if statsText != "" {
			statsText += spaceBgStyle.Render(" ")
		}
		statsText += s.styledWithBg(s.styles.Removed, fmt.Sprintf("-%d", file.Deleted))
	}

	statsLen := 0
	if file.Added > 0 {
		statsLen += len(fmt.Sprintf("+%d", file.Added))
	}
	if file.Deleted > 0 {
		if file.Added > 0 {
			statsLen++
		}
		statsLen += len(fmt.Sprintf("-%d", file.Deleted))
	}

	pathWidth := max(1, width-3-statsLen-1)
	path := fitTextMiddle(file.Path, pathWidth)
	glyphWithBg := glyphStyle.Copy().Background(lipgloss.Color(theme.Black))
	line := glyphWithBg.Render(glyph) + spaceBgStyle.Render(" ") + s.styledWithBg(s.styles.FgDim, path)
	if statsText != "" {
		padding := max(1, width-2-lipgloss.Width(path)-statsLen)
		line += spaceBgStyle.Render(strings.Repeat(" ", padding)) + statsText
	}
	return line
}

func statusPriority(s string) int {
	switch s {
	case "M":
		return 0
	case "A":
		return 1
	case "D":
		return 2
	case "U":
		return 3
	default:
		return 4
	}
}

func sortedModifiedFiles(files []gitModifiedFile) []gitModifiedFile {
	out := append([]gitModifiedFile(nil), files...)
	slices.SortStableFunc(out, func(a, b gitModifiedFile) int {
		if pa, pb := statusPriority(a.Status), statusPriority(b.Status); pa != pb {
			return cmp.Compare(pa, pb)
		}
		return cmp.Compare(a.Path, b.Path)
	})
	return out
}
