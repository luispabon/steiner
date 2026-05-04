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

	scratchpadIntent    string
	scratchpadDecisions string
	scratchpadOpen      string
	scratchpadNext      string
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
	return theme.WithBg(
		s.styles.Sidebar.Width(sidebarWidth).Height(height).Padding(sidebarPadV, sidebarPadH).Render(body),
		lipgloss.Color(theme.Black),
	)
}

func (s sidebarState) styledWithBg(baseStyle lipgloss.Style, text string) string {
	return baseStyle.Copy().Background(lipgloss.Color(theme.Black)).Render(text)
}

func (s sidebarState) lines(width, innerHeight int) []string {
	fgBright := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))

	// Build all static lines first so we can compute overflow budget.
	var static []string

	// Brand: 3-line block logo on the left; "steiner 0.1.4" on the third line
	static = append(static, s.brandSection(width-3)...)
	static = append(static, lipgloss.NewStyle().Background(lipgloss.Color(theme.Black)).Render(""))
	static = append(static, s.styledWithBg(s.styles.FgMute, strings.Repeat("─", max(0, width))))

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

	// Scratchpad card — always shown; placeholder when empty
	static = append(static, "")
	static = append(static, cardLabel("scratchpad", s.styles))
	anyField := s.scratchpadIntent != "" || s.scratchpadNext != "" || s.scratchpadOpen != "" || s.scratchpadDecisions != ""
	if !anyField {
		static = append(static, s.styledWithBg(s.styles.FgMute, "no active task"))
	} else {
		if s.scratchpadIntent != "" {
			static = append(static, cardFieldWrapped("Intent", s.styles.FgDim, s.scratchpadIntent, width, s.styles)...)
		}
		if s.scratchpadDecisions != "" {
			static = append(static, cardFieldWrapped("Decided", s.styles.FgDim, s.scratchpadDecisions, width, s.styles)...)
		}
		if s.scratchpadOpen != "" {
			static = append(static, cardFieldWrapped("Open", s.styles.FgDim, s.scratchpadOpen, width, s.styles)...)
		}
		if s.scratchpadNext != "" {
			static = append(static, cardFieldWrapped("Next", s.styles.FgDim, s.scratchpadNext, width, s.styles)...)
		}
	}

	// Repository card
	static = append(static, "")
	static = append(static, cardLabel("repository", s.styles))
	maxWD := min(25, max(1, width-7))
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
		lines = append(lines, s.styledWithBg(s.styles.FgMute, fmt.Sprintf("↓ %d more", remaining)))
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

// brandSection renders a 3-line block-art logo plus version string on the
// third line. width is the available inner width.
func (s sidebarState) brandSection(width int) []string {
	// Three lines of Unicode block-art logo.
	logo := []string{
		"▄▖▗   ▘",
		"▚ ▜▘█▌▌▛▌█▌▛▘",
		"▄▌▐▖▙▖▌▌▌▙▖▌",
	}
	bg := lipgloss.NewStyle().Background(lipgloss.Color(theme.Black))
	accentFg := s.styles.Accent.Copy().Background(lipgloss.Color(theme.Black))
	out := make([]string, 0, 3)
	for i, line := range logo {
		if i < 2 {
			out = append(out, accentFg.Render(line))
		} else {
			out = append(out, bg.Render(line))
		}
	}

	// Append version on the third line after the logo characters.
	ver := s.styles.FgMute.Copy().Background(lipgloss.Color(theme.Black)).Render("0.1.4")
	third := out[2] + bg.Render(" ") + ver
	thirdWidth := lipgloss.Width(third)
	if pad := width - thirdWidth; pad > 0 {
		third += bg.Render(strings.Repeat(" ", pad))
	}
	out[2] = third

	return out
}

func cardLabel(label string, styles theme.Styles) string {
	return styles.CardLabel.Copy().Background(lipgloss.Color(theme.Black)).Render(strings.ToUpper(label))
}

func cardField(key string, valStyle lipgloss.Style, value string, styles theme.Styles) string {
	keyStyle := styles.FgFaint.Copy().Background(lipgloss.Color(theme.Black))
	valStyleWithBg := valStyle.Copy().Background(lipgloss.Color(theme.Black))
	keyStr := keyStyle.Render(fmt.Sprintf("%-7s", key))
	return keyStr + valStyleWithBg.Render(value)
}

// cardFieldWrapped renders a card field with up to 2 word-wrapped lines.
// width is the full inner width; the value column is width-7.
func cardFieldWrapped(key string, valStyle lipgloss.Style, value string, width int, styles theme.Styles) []string {
	valWidth := max(1, width-7)
	wrapped := wrapWords(strings.TrimSpace(value), valWidth, 2)
	if len(wrapped) == 0 {
		return nil
	}
	out := make([]string, 0, len(wrapped))
	keyStyle := styles.FgFaint.Copy().Background(lipgloss.Color(theme.Black))
	valStyleWithBg := valStyle.Copy().Background(lipgloss.Color(theme.Black))
	keyStr := keyStyle.Render(fmt.Sprintf("%-7s", key))
	bgStyle := lipgloss.NewStyle().Background(lipgloss.Color(theme.Black))
	indent := bgStyle.Render(strings.Repeat(" ", 7))
	for i, line := range wrapped {
		if i == 0 {
			out = append(out, keyStr+valStyleWithBg.Render(line))
		} else {
			out = append(out, indent+valStyleWithBg.Render(line))
		}
	}
	return out
}

// wrapWords splits text into at most maxLines lines of at most width runes each,
// breaking at word boundaries. The last line is truncated with "…" if needed.
func wrapWords(text string, width, maxLines int) []string {
	if width <= 0 || maxLines <= 0 || text == "" {
		return nil
	}
	words := strings.Fields(text)
	var lines []string
	current := ""
	for _, word := range words {
		if len(lines) >= maxLines {
			break
		}
		switch {
		case current == "":
			current = word
		case len(current)+1+len(word) <= width:
			current += " " + word
		default:
			lines = append(lines, current)
			current = word
		}
	}
	if current != "" && len(lines) < maxLines {
		lines = append(lines, current)
	}
	// Truncate the last line if it exceeds width.
	if n := len(lines); n > 0 {
		last := lines[n-1]
		runes := []rune(last)
		if len(runes) > width {
			if width > 1 {
				lines[n-1] = string(runes[:width-1]) + "…"
			} else {
				lines[n-1] = string(runes[:width])
			}
		}
	}
	return lines
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
	barWidth := max(4, width-2)

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

	barWithBg := barStyle.Copy().Background(lipgloss.Color(theme.Black))
	emptyWithBg := lipgloss.NewStyle().Background(lipgloss.Color(theme.Black))
	bar := barWithBg.Render(strings.Repeat("█", filled)) +
		emptyWithBg.Render(strings.Repeat(" ", barWidth-filled))
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
	return s.styledWithBg(s.styles.FgDim, usageStr+strings.Repeat(" ", pad)+pctStr)
}

func (s sidebarState) compactDotLine() string {
	active := strings.TrimSpace(s.compaction) != "" && s.compaction != "idle"
	if active {
		// Pulsing dot
		dot := "●"
		if s.tickCount%2 == 0 {
			dot = "○"
		}
		dotStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warn)).Background(lipgloss.Color(theme.Black))
		return dotStyle.Render(dot) +
			s.styledWithBg(s.styles.FgDim, " compacting…")
	}
	return s.styledWithBg(s.styles.FgFaint, "●") + s.styledWithBg(s.styles.FgDim, " auto @ 90%")
}

func (s sidebarState) branchLine(width int) string {
	branch := strings.TrimSpace(s.branch)
	if branch == "" {
		branch = "n/a"
	}
	maxBranch := max(1, width-7)
	if s.dirty {
		maxBranch = max(1, maxBranch-2) // reserve " ●"
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

	pathWidth := max(1, width-3-statsLen-1) // glyph(1) + space(1) + ... + space(1) + stats
	path := fitTextMiddle(file.Path, pathWidth)

	glyphWithBg := glyphStyle.Copy().Background(lipgloss.Color(theme.Black))
	line := glyphWithBg.Render(glyph) + spaceBgStyle.Render(" ") + s.styledWithBg(s.styles.FgDim, path)
	if statsText != "" {
		padding := max(1, width-2-lipgloss.Width(path)-statsLen)
		line += spaceBgStyle.Render(strings.Repeat(" ", padding)) + statsText
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
