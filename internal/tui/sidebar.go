package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	sidebarWidth    = 34
	sidebarMinWidth = 100
	sidebarPadding  = 1
)

type sidebarState struct {
	expanded      bool
	model         string
	provider      string
	homeDir       string
	promptUsed    int
	budgetUsed    int
	contextBudget int
	currentTurn   int
	maxTurns      int
	compaction    string
	branch        string
	dirty         bool
	modifiedFiles []gitModifiedFile
	workingDir    string
	styles        theme.Styles
	tickCount     int
}

func newSidebarState() sidebarState {
	return sidebarState{expanded: true}
}

func (s *sidebarState) Toggle() {
	if s == nil {
		return
	}
	s.expanded = !s.expanded
}

func (s *sidebarState) SetExpanded(expanded bool) {
	if s == nil {
		return
	}
	s.expanded = expanded
}

func (s sidebarState) Visible(width int) bool {
	return s.expanded && width >= sidebarMinWidth
}

func (s sidebarState) View(width, height int) string {
	if !s.Visible(width) {
		return ""
	}
	innerWidth := sidebarWidth - sidebarPadding*2
	lines := s.lines(innerWidth)
	body := strings.Join(lines, "\n")
	return s.styles.Sidebar.Width(sidebarWidth).Height(height).Padding(sidebarPadding, sidebarPadding).Render(body)
}

func (s sidebarState) lines(width int) []string {
	var lines []string

	// Brand row
	lines = append(lines, s.brandRow())
	lines = append(lines, s.styles.FgMute.Render(strings.Repeat("─", maxInt(0, width))))
	lines = append(lines, "")

	// Model card
	lines = append(lines, cardLabel("model", s.styles))
	lines = append(lines, cardField("name", fitText(safeText(s.model), width-7), s.styles))
	lines = append(lines, cardField("host", fitText(safeText(s.provider), width-7), s.styles))
	lines = append(lines, "")

	// Context card
	lines = append(lines, cardLabel("context", s.styles))
	lines = append(lines, s.tokenBarLine(width))
	lines = append(lines, s.tokenUsageLine(width))
	lines = append(lines, s.compactDotLine())
	lines = append(lines, "")

	// Repository card
	lines = append(lines, cardLabel("repository", s.styles))
	lines = append(lines, cardField("dir", fitText(s.workdirSummary(width), width-6), s.styles))
	lines = append(lines, s.branchLine())

	// Modified files card
	if len(s.modifiedFiles) > 0 {
		lines = append(lines, "")
		lines = append(lines, cardLabel(fmt.Sprintf("modified  %d", len(s.modifiedFiles)), s.styles))
		for _, file := range s.modifiedFiles {
			lines = append(lines, s.modifiedFileLine(file, width))
		}
	}

	return lines
}

func (s sidebarState) workdirSummary(width int) string {
	value := strings.TrimSpace(s.workingDir)
	if value == "" {
		return "n/a"
	}
	return homeRelativePath(filepath.Clean(value), strings.TrimSpace(s.homeDir))
}

func (s sidebarState) brandRow() string {
	square := s.styles.AccentSoft.Foreground(lipgloss.Color(theme.AccentAmber)).Render("▪")
	name := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(theme.Fg)).Render("steiner")
	ver := s.styles.FgMute.Render("v0.0.1")
	return square + " " + name + " " + ver
}

func cardLabel(label string, styles theme.Styles) string {
	spaced := strings.Join(strings.Split(strings.ToUpper(label), ""), " ")
	return styles.FgMute.Render(spaced)
}

func cardField(key, value string, styles theme.Styles) string {
	keyStr := styles.FgFaint.Render(key + ":")
	valStr := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Render(" " + value)
	return keyStr + valStr
}

func (s sidebarState) tokenBarLine(width int) string {
	pct := occupancyPercent(s.promptUsed, s.contextBudget)
	barWidth := maxInt(4, width-2)

	var barColor lipgloss.Color
	switch {
	case pct > 90:
		barColor = lipgloss.Color(theme.Removed)
	case pct > 70:
		barColor = lipgloss.Color(theme.Warn)
	default:
		barColor = lipgloss.Color(theme.AccentAmber)
	}

	filled := 0
	if s.contextBudget > 0 && barWidth > 0 {
		filled = (s.promptUsed * barWidth) / s.contextBudget
		if filled > barWidth {
			filled = barWidth
		}
	}

	bar := lipgloss.NewStyle().Foreground(barColor).Render(strings.Repeat("█", filled)) +
		s.styles.FgMute.Render(strings.Repeat("░", barWidth-filled))
	return bar
}

func (s sidebarState) tokenUsageLine(width int) string {
	pct := occupancyPercent(s.promptUsed, s.contextBudget)

	var pctColor lipgloss.Color
	switch {
	case pct > 90:
		pctColor = lipgloss.Color(theme.Removed)
	case pct > 70:
		pctColor = lipgloss.Color(theme.Warn)
	default:
		pctColor = lipgloss.Color(theme.AccentAmber)
	}

	usageStr := sidebarPromptCount(s.promptUsed, s.contextBudget)
	pctStr := lipgloss.NewStyle().Foreground(pctColor).Render(fmt.Sprintf("%d%%", pct))
	return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Render(usageStr) + " " + pctStr
}

func (s sidebarState) compactDotLine() string {
	active := strings.TrimSpace(s.compaction) != "" && s.compaction != "idle"
	if active {
		// Pulsing dot
		dot := "●"
		if s.tickCount%2 == 0 {
			dot = "○"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn)).Render(dot) +
			s.styles.FgDim.Render(" compacting…")
	}
	return s.styles.FgMute.Render("● auto @ 90%")
}

func (s sidebarState) branchLine() string {
	branch := strings.TrimSpace(s.branch)
	if branch == "" {
		branch = "n/a"
	}
	result := s.styles.FgFaint.Render("branch:") + " " + lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Render(branch)
	if s.dirty {
		result += " " + lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn)).Render("●")
	}
	return result
}

func (s sidebarState) modifiedFileLine(file gitModifiedFile, width int) string {
	var glyphStyle lipgloss.Style
	var glyph string
	switch {
	case file.Added > 0 && file.Deleted == 0:
		glyph = "A"
		glyphStyle = s.styles.Added
	case file.Added == 0 && file.Deleted > 0:
		glyph = "D"
		glyphStyle = s.styles.Removed
	case file.Added > 0 && file.Deleted > 0:
		glyph = "M"
		glyphStyle = s.styles.Warn
	default:
		glyph = "M"
		glyphStyle = s.styles.FgMute
	}

	statsText := ""
	if file.Added > 0 {
		statsText += s.styles.Added.Render(fmt.Sprintf("+%d", file.Added))
	}
	if file.Deleted > 0 {
		if statsText != "" {
			statsText += " "
		}
		statsText += s.styles.Removed.Render(fmt.Sprintf("-%d", file.Deleted))
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

	pathWidth := maxInt(1, width-3-statsLen-1) // glyph(1) + space(1) + ... + space(1) + stats
	path := fitText(file.Path, pathWidth)

	line := glyphStyle.Render(glyph) + " " + s.styles.FgDim.Render(path)
	if statsText != "" {
		padding := maxInt(1, width-2-lipgloss.Width(path)-statsLen)
		line += strings.Repeat(" ", padding) + statsText
	}
	return line
}

func sidebarPromptCount(used, budget int) string {
	if budget <= 0 {
		if used <= 0 {
			return "n/a"
		}
		return fmt.Sprintf("%d used", used)
	}
	return fmt.Sprintf("%d / %d", used, budget)
}

func occupancyPercent(used, budget int) int {
	if budget <= 0 {
		return 0
	}
	percent := (used * 100) / budget
	if percent < 0 {
		return 0
	}
	return percent
}


func fitText(text string, width int) string {
	text = strings.TrimSpace(text)
	if width <= 0 || len(text) <= width {
		return text
	}
	if width <= 3 {
		return text[:width]
	}
	return text[:width-3] + "..."
}

func safeText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "n/a"
	}
	return text
}

func homeRelativePath(pathValue, homeDir string) string {
	pathValue = filepath.Clean(strings.TrimSpace(pathValue))
	homeDir = filepath.Clean(strings.TrimSpace(homeDir))
	if pathValue == "" {
		return ""
	}
	if homeDir == "" || homeDir == "." || !filepath.IsAbs(pathValue) || !filepath.IsAbs(homeDir) {
		return pathValue
	}
	if pathValue == homeDir {
		return "~"
	}
	rel, err := filepath.Rel(homeDir, pathValue)
	if err != nil {
		return pathValue
	}
	if rel == "." {
		return "~"
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return pathValue
	}
	return filepath.Join("~", rel)
}
