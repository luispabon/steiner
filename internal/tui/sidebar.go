package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	sidebarWidth      = 40
	sidebarMinWidth   = 100
	sidebarToggleHint = "ctrl+b toggle sidebar"
	sidebarPadding    = 1
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
	lines := make([]string, 0, 20)

	lines = append(lines, sidebarSection("Model", s.styles))
	lines = append(lines, sidebarSubField("Endpoint", safeText(s.provider), width, s.styles))
	lines = append(lines, sidebarSubField("Name", safeText(s.model), width, s.styles))
	lines = append(lines, "")

	lines = append(lines, sidebarSection("Context", s.styles))
	lines = append(lines, sidebarSubField("Compaction", s.compactionSummary(), width, s.styles))
	lines = append(lines, "")
	lines = append(lines, s.promptMeterLines(width)...)
	lines = append(lines, "")

	lines = append(lines, sidebarSection("Repository", s.styles))
	lines = append(lines, sidebarSubField("Workdir", s.workdirSummary(width), width, s.styles))
	lines = append(lines, sidebarSubField("Branch", s.gitSummary(), width, s.styles))
	if len(s.modifiedFiles) > 0 {
		lines = append(lines, "")
		lines = append(lines, s.styles.SidebarLabel.Render("Modified files:"))
		for _, file := range s.modifiedFiles {
			lines = append(lines, sidebarModifiedFileLine(file, width, s.styles))
		}
	}

	return lines
}

func (s sidebarState) promptSummary() string {
	return sidebarPromptCount(s.promptUsed, s.contextBudget)
}

func (s sidebarState) promptMeterLines(width int) []string {
	barWidth := maxInt(8, width-9)
	return []string{
		centerSidebarText(sidebarPromptMeter(s.promptUsed, s.contextBudget, barWidth), width, s.styles),
		centerSidebarText(sidebarPromptCount(s.promptUsed, s.contextBudget), width, s.styles),
	}
}

func (s sidebarState) compactionSummary() string {
	value := strings.TrimSpace(s.compaction)
	if value == "" {
		return "idle"
	}
	return value
}

func (s sidebarState) gitSummary() string {
	branch := strings.TrimSpace(s.branch)
	if branch == "" {
		branch = "n/a"
	}
	if s.dirty {
		return branch + "*"
	}
	return branch
}

func (s sidebarState) workdirSummary(width int) string {
	value := strings.TrimSpace(s.workingDir)
	if value == "" {
		return "n/a"
	}
	return homeRelativePath(filepath.Clean(value), strings.TrimSpace(s.homeDir))
}

func sidebarSection(title string, styles theme.Styles) string {
	return styles.SidebarSection.Render(title)
}

func sidebarSubField(label, value string, width int, styles theme.Styles) string {
	const prefix = ""
	value = strings.TrimSpace(value)
	if value == "" {
		value = "n/a"
	}
	if label == "" {
		maxVal := maxInt(1, width-len(prefix))
		chunks := wrapChunks(value, maxVal)
		cont := strings.Repeat(" ", len(prefix))
		var sb strings.Builder
		sb.WriteString(prefix + styles.SidebarValue.Render(chunks[0]))
		for _, c := range chunks[1:] {
			sb.WriteString("\n" + styles.SidebarValue.Render(cont+c))
		}
		return sb.String()
	}
	labelColon := label + ":"
	// +1 for the leading space before value
	maxVal := maxInt(1, width-len(prefix)-len(labelColon)-1)
	chunks := wrapChunks(value, maxVal)
	cont := strings.Repeat(" ", len(prefix)+len(labelColon)+1)
	var sb strings.Builder
	sb.WriteString(prefix + styles.SidebarLabel.Render(labelColon) + styles.SidebarValue.Render(" "+chunks[0]))
	for _, c := range chunks[1:] {
		sb.WriteString("\n" + styles.SidebarValue.Render(cont+c))
	}
	return sb.String()
}

func sidebarPromptMeter(used, budget, width int) string {
	percent := occupancyPercent(used, budget)
	if width < 8 {
		width = 8
	}
	label := fmt.Sprintf("%d%%", percent)
	barWidth := maxInt(1, width-len(label)-2)
	filled := 0
	if budget > 0 {
		filled = (used * barWidth) / budget
		if filled > barWidth {
			filled = barWidth
		}
	}
	if filled < 0 {
		filled = 0
	}
	bar := "[" + strings.Repeat("=", filled) + strings.Repeat(".", barWidth-filled) + "]"
	return bar + " " + label
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

func centerSidebarText(text string, width int, styles theme.Styles) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	padding := 0
	if width > len(text) {
		padding = (width - len(text)) / 2
	}
	return styles.SidebarValue.Render(strings.Repeat(" ", padding) + text)
}

func sidebarModifiedFileLine(file gitModifiedFile, width int, styles theme.Styles) string {
	path := fitText(strings.TrimSpace(file.Path), width)
	statsText, statsView := sidebarModifiedFileStats(file, styles)
	if statsText == "" {
		return styles.SidebarValue.Render(path)
	}
	leftWidth := maxInt(1, width-len(statsText)-1)
	path = fitText(path, leftWidth)
	spaces := maxInt(1, width-lipgloss.Width(path)-len(statsText))
	return styles.SidebarValue.Render(path) + styles.SidebarValue.Render(strings.Repeat(" ", spaces)) + statsView
}

func sidebarModifiedFileStats(file gitModifiedFile, styles theme.Styles) (string, string) {
	partsText := make([]string, 0, 2)
	partsView := make([]string, 0, 2)
	if file.Added > 0 {
		value := fmt.Sprintf("+%d", file.Added)
		partsText = append(partsText, value)
		partsView = append(partsView, styles.SuccessStyle.Render(value))
	}
	if file.Deleted > 0 {
		value := fmt.Sprintf("-%d", file.Deleted)
		partsText = append(partsText, value)
		partsView = append(partsView, styles.ErrorStyle.Render(value))
	}
	return strings.Join(partsText, " "), strings.Join(partsView, styles.SidebarValue.Render(" "))
}

func wrapChunks(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var chunks []string
	for len(text) > width {
		chunks = append(chunks, text[:width])
		text = text[width:]
	}
	if len(text) > 0 || len(chunks) == 0 {
		chunks = append(chunks, text)
	}
	return chunks
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
