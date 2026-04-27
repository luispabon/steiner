package tui

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
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
	ahead         int
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
	innerHeight := height - sidebarPadV*2
	if innerHeight < 0 {
		innerHeight = 0
	}
	lines := s.lines(innerWidth, innerHeight)
	body := strings.Join(lines, "\n")
	return s.styles.Sidebar.Width(sidebarWidth).Height(height).Padding(sidebarPadV, sidebarPadH).Render(body)
}

func (s sidebarState) lines(width, innerHeight int) []string {
	fgBright := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))

	// Build all static lines first so we can compute overflow budget.
	var static []string

	// Brand row: mark · name · version right-aligned; gap then divider
	static = append(static, s.brandRow(width-3))
	static = append(static, "")
	static = append(static, s.styles.FgMute.Render(strings.Repeat("─", maxInt(0, width))))

	// Model card
	static = append(static, "")
	static = append(static, cardLabel("model", s.styles))
	static = append(static, cardField("name", fgBright, fitText(safeText(s.model), width-7), s.styles))
	if q := strings.TrimSpace(s.quant); q != "" {
		static = append(static, cardField("quant", s.styles.FgDim, fitText(q, width-7), s.styles))
	}
	host := fitText(stripProviderURL(s.provider), width-7)
	if host == "" {
		host = "n/a"
	}
	static = append(static, cardField("host", s.styles.FgDim, host, s.styles))

	// Context card
	static = append(static, "")
	static = append(static, cardLabel("context", s.styles))
	static = append(static, s.tokenBarLine(width))
	static = append(static, s.tokenUsageLine(width))
	static = append(static, s.compactDotLine())

	// Repository card
	static = append(static, "")
	static = append(static, cardLabel("repository", s.styles))
	maxWD := min(25, maxInt(1, width-7))
	static = append(static, cardField("workdir", s.styles.FgDim, fitTextMiddle(s.workdirSummary(width), maxWD), s.styles))
	static = append(static, s.branchLine(width))
	if s.ahead > 0 {
		static = append(static, cardField("ahead", s.styles.FgDim, fmt.Sprintf("%d commits", s.ahead), s.styles))
	}

	// Modified files heading
	static = append(static, "")
	static = append(static, cardLabel(fmt.Sprintf("modified files · %d", len(s.modifiedFiles)), s.styles))

	// Sort files: status priority (M→A→D→U) then alphabetically
	sorted := sortedModifiedFiles(s.modifiedFiles)

	// Compute rows available for the file list
	availForFiles := innerHeight - len(static)
	if availForFiles < 0 {
		availForFiles = 0
	}

	overflow := innerHeight > 0 && len(sorted) > availForFiles
	displayCount := len(sorted)
	if overflow {
		displayCount = availForFiles - 1 // reserve 1 row for ↓ indicator
		if displayCount < 0 {
			displayCount = 0
		}
	}

	lines := static
	for i := 0; i < displayCount && i < len(sorted); i++ {
		lines = append(lines, s.modifiedFileLine(sorted[i], width))
	}
	if overflow {
		remaining := len(sorted) - displayCount
		lines = append(lines, s.styles.FgMute.Render(fmt.Sprintf("↓ %d more", remaining)))
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
	mark := s.styles.AccentBg.Render("s")
	name := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)).Render("steiner")
	ver := s.styles.FgMute.Render("0.1.4")
	leftVisible := 1 + 1 + len("steiner") // mark + space + name
	verVisible := lipgloss.Width(ver)
	pad := width - leftVisible - verVisible
	if pad < 1 {
		pad = 1
	}

	content := mark + " " + name + strings.Repeat(" ", pad) + ver
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(s.styles.AccentColor).
		Padding(0, 1, 0, 0).
		Render(content)
}

func cardLabel(label string, styles theme.Styles) string {
	return styles.CardLabel.Render(strings.ToUpper(label))
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

	var barStyle lipgloss.Style
	switch {
	case pct > 90:
		barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Removed))
	case pct > 70:
		barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn))
	default:
		barStyle = s.styles.Accent
	}

	filled := 0
	if s.contextBudget > 0 && barWidth > 0 {
		filled = (s.promptUsed * barWidth) / s.contextBudget
		if filled > barWidth {
			filled = barWidth
		}
	}

	bar := barStyle.Render(strings.Repeat("█", filled)) +
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

func (s sidebarState) branchLine(width int) string {
	branch := strings.TrimSpace(s.branch)
	if branch == "" {
		branch = "n/a"
	}
	maxBranch := maxInt(1, width-7)
	if s.dirty {
		maxBranch = maxInt(1, maxBranch-2) // reserve " ●"
	}
	branchText := fitText(branch, maxBranch)
	line := cardField("branch", lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg)), branchText, s.styles)
	if s.dirty {
		line += " " + lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn)).Render("●")
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
	path := fitTextMiddle(file.Path, pathWidth)

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
