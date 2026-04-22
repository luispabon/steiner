package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	sidebarWidth       = 31
	sidebarMinWidth    = 92
	sidebarToggleHint  = "ctrl+b toggle sidebar"
	sidebarSkillLimit  = 3
	sidebarPathPadding = 12
)

type sidebarState struct {
	expanded      bool
	model         string
	provider      string
	contextUsed   int
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

func (s sidebarState) View(width int) string {
	if !s.Visible(width) {
		return ""
	}

	lines := s.lines(sidebarWidth)
	body := strings.Join(lines, "\n")
	return s.styles.Sidebar.Width(sidebarWidth).Render(body)
}

func (s sidebarState) lines(width int) []string {
	lines := make([]string, 0, 8)
	lines = append(lines, sidebarField("model", safeText(s.model), width, s.styles))
	lines = append(lines, sidebarField("provider", safeText(s.provider), width, s.styles))
	lines = append(lines, sidebarField("context", s.contextSummary(), width, s.styles))
	lines = append(lines, sidebarField("turn", s.turnSummary(), width, s.styles))
	lines = append(lines, sidebarField("compact", s.compactionSummary(), width, s.styles))
	lines = append(lines, sidebarField("git", s.gitSummary(), width, s.styles))
	lines = append(lines, sidebarField("workdir", s.workdirSummary(width), width, s.styles))
	lines = append(lines, sidebarField("skills", s.skillsSummary(width), width, s.styles))
	return lines
}

func (s sidebarState) contextSummary() string {
	if s.contextBudget <= 0 {
		if s.contextUsed <= 0 {
			return "n/a"
		}
		return fmt.Sprintf("%d used", s.contextUsed)
	}

	percent := 0
	if s.contextBudget > 0 {
		percent = (s.contextUsed * 100) / s.contextBudget
	}
	if percent < 0 {
		percent = 0
	}
	return fmt.Sprintf("%d/%d %d%%", s.contextUsed, s.contextBudget, percent)
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
	value = filepath.Clean(value)
	return fitText(value, maxInt(1, width-sidebarPathPadding))
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
	return fitText(strings.Join(skills, ", "), maxInt(1, width-sidebarPathPadding))
}

func sidebarField(label, value string, width int, styles theme.Styles) string {
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	if label == "" {
		label = "item"
	}
	if value == "" {
		value = "n/a"
	}

	maxValueWidth := maxInt(1, width-len(label)-2)
	value = fitText(value, maxValueWidth)
	return styles.SidebarLabel.Render(label) + ": " + styles.SidebarValue.Render(value)
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
