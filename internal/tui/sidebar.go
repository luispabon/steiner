package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	sidebarWidth    = 36
	sidebarMinWidth = 100
	sidebarPadH     = 2 // horizontal padding (2 cols each side)
	sidebarPadV     = 1 // vertical padding (1 row top/bottom)
)

type sidebarState struct {
	expanded      bool
	model         string
	quant         string
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
	innerWidth := sidebarWidth - sidebarPadH*2
	lines := s.lines(innerWidth)
	body := strings.Join(lines, "\n")
	return s.styles.Sidebar.Width(sidebarWidth).Height(height).Padding(sidebarPadV, sidebarPadH).Render(body)
}

func (s sidebarState) lines(width int) []string {
	var lines []string
	fgBright := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))

	// Brand row: mark · name · version right-aligned; gap then divider
	lines = append(lines, s.brandRow(width))
	lines = append(lines, "")
	lines = append(lines, s.styles.FgMute.Render(strings.Repeat("─", maxInt(0, width))))

	// Model card
	lines = append(lines, "")
	lines = append(lines, cardLabel("model", s.styles))
	lines = append(lines, cardField("name", fgBright, fitText(safeText(s.model), width-7), s.styles))
	if q := strings.TrimSpace(s.quant); q != "" {
		lines = append(lines, cardField("quant", s.styles.FgDim, fitText(q, width-7), s.styles))
	}
	host := fitText(stripProviderURL(s.provider), width-7)
	if host == "" {
		host = "n/a"
	}
	lines = append(lines, cardField("host", s.styles.FgDim, host, s.styles))

	// Context card
	lines = append(lines, "")
	lines = append(lines, cardLabel("context", s.styles))
	lines = append(lines, s.tokenBarLine(width))
	lines = append(lines, s.tokenUsageLine(width))
	lines = append(lines, s.compactDotLine())

	// Repository card
	lines = append(lines, "")
	lines = append(lines, cardLabel("repository", s.styles))
	lines = append(lines, cardField("workdir", s.styles.FgDim, fitText(s.workdirSummary(width), width-7), s.styles))
	lines = append(lines, s.branchLine())

	// Modified files card — always shown
	lines = append(lines, "")
	lines = append(lines, cardLabel(fmt.Sprintf("modified files · %d", len(s.modifiedFiles)), s.styles))
	for _, file := range s.modifiedFiles {
		lines = append(lines, s.modifiedFileLine(file, width))
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

func (s sidebarState) brandRow(width int) string {
	mark := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.AccentAmber)).
		Foreground(lipgloss.Color(theme.Bg)).
		Render("s")
	name := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Render("steiner")
	ver := s.styles.FgMute.Render("0.1.4")
	leftVisible := 1 + 1 + len("steiner") // mark + space + name
	verVisible := lipgloss.Width(ver)
	pad := width - leftVisible - verVisible
	if pad < 1 {
		pad = 1
	}
	return mark + " " + name + strings.Repeat(" ", pad) + ver
}

func cardLabel(label string, styles theme.Styles) string {
	return styles.FgMute.Render(strings.ToUpper(label))
}

func cardField(key string, valStyle lipgloss.Style, value string, styles theme.Styles) string {
	keyStr := styles.FgFaint.Render(fmt.Sprintf("%-7s", key))
	return keyStr + valStyle.Render(value)
}

func stripProviderURL(url string) string {
	url = strings.TrimSpace(url)
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimSuffix(url, "/v1")
	url = strings.TrimSuffix(url, "/")
	return url
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
		lipgloss.NewStyle().Background(lipgloss.Color(theme.BgElev)).Render(strings.Repeat(" ", barWidth-filled))
	return bar
}

func (s sidebarState) tokenUsageLine(width int) string {
	pct := occupancyPercent(s.promptUsed, s.contextBudget)
	usageStr := sidebarPromptCount(s.promptUsed, s.contextBudget)
	pctStr := fmt.Sprintf("%d%%", pct)
	pad := width - len(usageStr) - len(pctStr)
	if pad < 1 {
		pad = 1
	}
	return s.styles.FgDim.Render(usageStr + strings.Repeat(" ", pad) + pctStr)
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
	return s.styles.FgFaint.Render("●") + s.styles.FgDim.Render(" auto @ 90%")
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
