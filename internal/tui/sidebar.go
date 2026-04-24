package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	sidebarWidth      = 40
	sidebarMinWidth   = 100
	sidebarToggleHint = "ctrl+b toggle sidebar"
	sidebarSkillLimit = 3
	sidebarPadding    = 1
)

type sidebarState struct {
	expanded      bool
	model         string
	provider      string
	promptUsed    int
	budgetUsed    int
	contextBudget int
	currentTurn   int
	maxTurns      int
	compaction    string
	branch        string
	dirty         bool
	workingDir    string
	activeSkills  []string
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
	lines = append(lines, sidebarSubField("Prompt", s.promptSummary(), width, s.styles))
	lines = append(lines, sidebarSubField("Budget", s.budgetSummary(), width, s.styles))
	lines = append(lines, sidebarSubField("Compaction", s.compactionSummary(), width, s.styles))
	lines = append(lines, sidebarSubField("Turn", s.turnSummary(), width, s.styles))
	lines = append(lines, "")

	lines = append(lines, sidebarSection("Repository", s.styles))
	lines = append(lines, sidebarSubField("Workdir", s.workdirSummary(width), width, s.styles))
	lines = append(lines, sidebarSubField("Branch", s.gitSummary(), width, s.styles))
	lines = append(lines, "")

	lines = append(lines, sidebarSection("Skills", s.styles))
	skills := s.skillsSummary(width)
	if skills == "" || skills == "n/a" || skills == "none" {
		lines = append(lines, "  "+s.styles.SidebarValue.Render("None"))
	} else {
		lines = append(lines, sidebarSubField("", skills, width, s.styles))
	}

	return lines
}

func (s sidebarState) promptSummary() string {
	return occupancySummary(s.promptUsed, s.contextBudget)
}

func (s sidebarState) budgetSummary() string {
	return occupancySummary(s.budgetUsed, s.contextBudget)
}

func occupancySummary(used, budget int) string {
	if budget <= 0 {
		if used <= 0 {
			return "n/a"
		}
		return fmt.Sprintf("%d used", used)
	}

	percent := 0
	if budget > 0 {
		percent = (used * 100) / budget
	}
	if percent < 0 {
		percent = 0
	}
	return fmt.Sprintf("%d/%d %d%%", used, budget, percent)
}

func (s sidebarState) contextSummary() string {
	if s.contextBudget <= 0 {
		if s.promptUsed <= 0 {
			return "n/a"
		}
		return fmt.Sprintf("%d used", s.promptUsed)
	}

	percent := 0
	if s.contextBudget > 0 {
		percent = (s.promptUsed * 100) / s.contextBudget
	}
	if percent < 0 {
		percent = 0
	}
	return fmt.Sprintf("%d/%d %d%%", s.promptUsed, s.contextBudget, percent)
}

func (s sidebarState) turnSummary() string {
	if s.maxTurns <= 0 {
		if s.currentTurn <= 0 {
			return "n/a"
		}
		return fmt.Sprintf("%d", s.currentTurn)
	}
	if s.currentTurn <= 0 {
		return fmt.Sprintf("0/%d", s.maxTurns)
	}
	return fmt.Sprintf("%d/%d", s.currentTurn, s.maxTurns)
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
	return filepath.Clean(value)
}

func (s sidebarState) skillsSummary(width int) string {
	if len(s.activeSkills) == 0 {
		return "none"
	}
	skills := append([]string(nil), s.activeSkills...)
	sort.Strings(skills)
	if len(skills) > sidebarSkillLimit {
		skills = append(skills[:sidebarSkillLimit], fmt.Sprintf("+%d", len(s.activeSkills)-sidebarSkillLimit))
	}
	return strings.Join(skills, ", ")
}

func sidebarSection(title string, styles theme.Styles) string {
	return styles.SidebarSection.Render(title)
}

func sidebarSubField(label, value string, width int, styles theme.Styles) string {
	const prefix = "  * "
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
