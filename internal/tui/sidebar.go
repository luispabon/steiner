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
	reasoning             string
	version               string
	updateAvailable       bool
	latestVersion         string
	profile               string
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
	styles                *theme.Styles
	tickCount             int
	perfDurationMs        int64
	perfTTFTMs            int64
	perfOutputTPS         float64
	sessionCacheHitRate   float64
	sessionCacheHitRateOK bool
	sessionActive         bool
	sessionElapsedSec     int64
	oneshotPhase          string
	sandboxStatus         string
	orchestrationLevel    string
	execMode              string // execution mode: "plan" or "build"
	mcpConnected          int
	mcpTotal              int
	mcpConnecting         bool
	mcpFailed             bool
	lspServers            []LSPServerStatus
	lspActive             int // N: servers with >=1 ready session
	lspTotalKnown         int // M: servers with >=1 active (starting/ready/failed) session this poll
	lspStarting           bool
	lspFailed             bool
	lspSingleName         string // comma-joined names of active servers, unfitted
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
	// WithBg(Black) is required: the sidebar uses Black as its background colour.
	// Nested renders emit ANSI resets that would create transparent gaps in
	// transparent terminals without the explicit per-cell background pass.
	return theme.WithBg(
		s.styles.Sidebar.Width(sidebarWidth).Height(height).Padding(sidebarPadV, sidebarPadH).Render(body),
		s.styles.Palette.SidebarBG,
	)
}

func (s sidebarState) styledWithBg(baseStyle lipgloss.Style, text string) string {
	return baseStyle.Background(lipgloss.Color(s.styles.Palette.SidebarBG)).Render(text)
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
	textWidth := lipgloss.Width(text)
	if width > 0 && textWidth <= width {
		return text
	}
	if width <= 0 {
		return text
	}
	if width <= 1 {
		return "…"
	}

	runes := []rune(text)
	ellipsisWidth := lipgloss.Width("…")
	availableWidth := max(0, width-ellipsisWidth)
	leftBudget := availableWidth / 2
	rightBudget := availableWidth - leftBudget

	// Consume a contiguous prefix and suffix, each stopping at the first
	// rune that would overflow its budget (never skipping a rune that
	// doesn't fit and continuing past it, which would drop characters
	// from the middle of the kept segments instead of just the ellipsis
	// region).
	leftEnd := 0
	leftWidth := 0
	for leftEnd < len(runes) {
		runeWidth := lipgloss.Width(string(runes[leftEnd]))
		if leftWidth+runeWidth > leftBudget {
			break
		}
		leftWidth += runeWidth
		leftEnd++
	}

	rightStart := len(runes)
	rightWidth := 0
	for rightStart > leftEnd {
		runeWidth := lipgloss.Width(string(runes[rightStart-1]))
		if rightWidth+runeWidth > rightBudget {
			break
		}
		rightWidth += runeWidth
		rightStart--
	}

	return string(runes[:leftEnd]) + "…" + string(runes[rightStart:])
}

func fitText(text string, width int) string {
	text = strings.TrimSpace(text)
	if width > 0 && len(text) <= width {
		return text
	}
	runes := []rune(text)
	if width <= 0 || len(runes) <= width {
		return text
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
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
