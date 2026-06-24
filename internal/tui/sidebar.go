package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

const (
	sidebarWidth    = 36
	sidebarMinWidth = 100
	sidebarPadH     = 2 // horizontal padding (2 cols each side)
	sidebarPadV     = 1 // vertical padding (1 row top/bottom)
)

type sidebarState struct {
	expanded              bool
	model                 string
	version               string
	quant                 string
	provider              string
	providerName          string
	homeDir               string
	promptUsed            int
	budgetUsed            int
	contextBudget         int
	currentTurn           int
	maxTurns              int
	compaction            compactionState
	branch                string
	dirty                 bool
	ahead                 int
	modifiedFiles         []gitModifiedFile
	workingDir            string
	activeSkill           string
	styles                theme.Styles
	tickCount             int
	perfDurationMs        int64
	perfTTFTMs            int64
	perfOutputTPS         float64
	sessionCacheHitRate   float64
	sessionCacheHitRateOK bool
	oneshotPhase          string
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
	innerHeight := height - sidebarPadV*2
	if innerHeight < 0 {
		innerHeight = 0
	}
	lines := s.lines(innerWidth, innerHeight)
	body := strings.Join(lines, "\n")
	return s.styles.Sidebar.Width(sidebarWidth).Height(height).Padding(sidebarPadV, sidebarPadH).Render(body)
}

func (s sidebarState) styledWithBg(baseStyle lipgloss.Style, text string) string {
	return baseStyle.Background(lipgloss.Color(theme.Black)).Render(text)
}

func (s sidebarState) workdirSummary() string {
	value := strings.TrimSpace(s.workingDir)
	if value == "" {
		return "n/a"
	}
	return homeRelativePath(filepath.Clean(value), strings.TrimSpace(s.homeDir))
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

func fitTextMiddle(text string, width int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if width <= 0 || len(runes) <= width {
		return text
	}
	if width <= 3 {
		return string(runes[:width])
	}
	keep := width - 1 // 1 cell for the ellipsis character
	left := keep / 2
	right := keep - left
	return string(runes[:left]) + "…" + string(runes[len(runes)-right:])
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
