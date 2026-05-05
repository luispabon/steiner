package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type approvalState struct {
	active         bool
	tool           string
	mode           string
	preview        string
	selectedAction int
}

type tickMsg struct{}

type paletteSetAccentMsg struct{ preset string }

type paletteToggleThinkingMsg struct{}

type paletteSwitchModelMsg struct{ name string }

type paletteClearMsg struct{}

type historyLoadedMsg struct{ prompts []string }

const (
	inputRailWidth = 1
	inputPadX      = 1
	inputPadY      = 1
	inputTailFill  = 1
)

type Model struct {
	width    int
	height   int
	viewport viewport.Model
	input    textarea.Model
	content  contentBuffer
	status   statusState
	sidebar  sidebarState
	git      *gitState

	approval                     approvalState
	activity                     activityState
	external                     <-chan tea.Msg
	autoScroll                   bool
	contentTopPad                int
	skillNames                   []string
	enabledSkills                map[string]bool
	modelNames                   []string
	modelContexts                map[string]int
	modelBaseURLs                map[string]string
	controller                   interactive.Controller
	activeTheme                  theme.Theme
	styles                       theme.Styles
	inputHistory                 []string
	historyIdx                   int
	historyDraft                 string
	fileHistory                  []string
	fileHistoryIdx               int
	completionCandidates         []string
	completionIdx                int
	helpVisible                  bool
	showThinking                 bool
	compacting                   bool
	accentPreset                 string
	sidebarPosition              string
	palette                      paletteModel
	fileList                     fileListOverlay
	filePicker                   filePickerOverlay
	contextOverlay               contextOverlayState
	exitModal                    exitModalState
	sessionHealthCompactionCount int
	sessionHealthTurn            int
	sessionHealthState           string
	sessionHealthGuidance        string
	sessionHealthNotes           []string
	ctxInfoPromptTokens          int
	ctxInfoReservedTokens        int
	ctxInfoSafetyTokens          int
	interruptPending             bool
	contentDirty                 bool
	syncDebounceSeq              int
}

func newModel(cfg Config, external <-chan tea.Msg) Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "ask steiner — / for commands, @ for files"
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.SetHeight(1)
	input.MaxHeight = 10
	// Remove ctrl+b from CharacterBackward to avoid conflict with sidebar toggle
	input.KeyMap.CharacterBackward = key.NewBinding(key.WithKeys("left"))
	// Add Shift+Enter and Alt+Enter for inserting newlines
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	// Init() has a value receiver, so its Focus() call only affects a copy.
	// Focus here so the running model's textarea is focused from the start.
	input.Focus()

	enabledSkills := make(map[string]bool, len(cfg.SkillNames))
	for _, name := range cfg.SkillNames {
		enabledSkills[name] = true
	}

	// Load theme
	var t theme.Theme
	if cfg.Theme != "" {
		var err error
		t, err = theme.Get(cfg.Theme)
		if err != nil {
			fmt.Fprintf(os.Stderr, "theme not found: %s, using default\n", cfg.Theme)
			t = theme.Default()
		}
	} else {
		t = theme.Default()
	}
	if t == nil {
		t = theme.Default()
	}

	// Resolve accent hex from preset name
	accentHex := theme.AccentPresets[cfg.AccentPreset]
	if accentHex == "" {
		accentHex = theme.AccentPresets["amber"] // fallback
	}

	m := Model{
		width:    80,
		height:   24,
		viewport: viewport.New(80, 22),
		input:    input,
		sidebar:  newSidebarState(),
		git:      newGitState(cfg.WorkingDir),

		external:        external,
		autoScroll:      true,
		skillNames:      append([]string(nil), cfg.SkillNames...),
		enabledSkills:   enabledSkills,
		modelNames:      append([]string(nil), cfg.ModelNames...),
		modelContexts:   cloneModelContexts(cfg.ModelContexts),
		modelBaseURLs:   cloneModelBaseURLs(cfg.ModelBaseURLs),
		controller:      cfg.Controller,
		activeTheme:     t,
		styles:          theme.BuildStyles(accentHex),
		inputHistory:    []string{},
		historyIdx:      0,
		historyDraft:    "",
		fileHistory:     []string{},
		fileHistoryIdx:  -1,
		showThinking:    cfg.ShowThinking,
		accentPreset:    cfg.AccentPreset,
		sidebarPosition: cfg.SidebarPosition,
	}
	m.status.model = strings.TrimSpace(cfg.Model)
	m.sidebar.model = strings.TrimSpace(cfg.Model)
	m.sidebar.contextBudget = m.contextBudgetForModel(m.sidebar.model)
	m.sidebar.provider = strings.TrimSpace(cfg.ProviderBaseURL)
	m.sidebar.maxTurns = cfg.MaxTurns
	m.sidebar.homeDir = strings.TrimSpace(cfg.HomeDir)
	m.sidebar.workingDir = strings.TrimSpace(cfg.WorkingDir)
	m.git.Refresh(context.Background())
	m.syncSidebar()
	m.layout()

	// Set styles on content and sidebar
	m.content.styles = m.styles
	m.content.glamourStyleSheet = m.activeTheme.GlamourStyleSheet()
	m.content.collapseState = make(map[int]bool)
	m.content.showThinking = m.showThinking
	m.content.showInternalScaffoldInference = cfg.ShowInternalScaffoldInference
	m.sidebar.styles = m.styles
	m.status.styles = m.styles
	m.activity = newActivityState(m.styles)

	// Match the composer to user message chrome: accent rail, muted surface.
	m.applyInputStyles()
	m.input.Focus()

	// Initialize palette
	m.palette = newPalette(m.styles, buildDefaultPaletteItems())
	m.palette.width = m.width
	m.palette.height = m.height

	// Initialize file list overlay
	m.fileList = newFileListOverlay(m.styles)
	m.fileList.width = m.width
	m.fileList.height = m.height

	m.filePicker = newFilePickerOverlay(m.styles)
	m.filePicker.width = m.width
	m.filePicker.height = m.height

	return m
}

func buildDefaultPaletteItems() []paletteItem {
	items := []paletteItem{
		{
			command: "/clear",
			name:    "Clear conversation",
			desc:    "reset the current session",
			action: func() tea.Cmd {
				return func() tea.Msg { return paletteClearMsg{} }
			},
		},
		{
			command: "/thinking",
			name:    "Toggle thinking",
			desc:    "show or hide thinking blocks",
			action: func() tea.Cmd {
				return func() tea.Msg { return paletteToggleThinkingMsg{} }
			},
		},
	}
	for _, model := range []string{"claude-opus-4-7", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"} {
		m := model
		items = append(items, paletteItem{
			command: "/model " + m,
			name:    "Switch model",
			desc:    m,
			action: func() tea.Cmd {
				return func() tea.Msg { return paletteSwitchModelMsg{name: m} }
			},
		})
	}
	for _, preset := range []string{"amber", "rose", "magenta", "violet", "cyan", "mint", "lime"} {
		p := preset
		items = append(items, paletteItem{
			command: "/accent " + p,
			name:    "Set accent",
			desc:    p,
			action: func() tea.Cmd {
				return func() tea.Msg { return paletteSetAccentMsg{preset: p} }
			},
		})
	}
	return items
}

func (m *Model) applyModelSelection(modelName, providerBaseURL string) {
	m.status.model = modelName
	m.sidebar.model = modelName
	m.sidebar.provider = strings.TrimSpace(providerBaseURL)
	m.sidebar.contextBudget = m.contextBudgetForModel(modelName)
	m.sidebar.promptUsed = 0
	m.sidebar.budgetUsed = 0
	if m.sidebar.contextBudget > 0 {
		m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
	} else {
		m.status.context = ""
	}
	m.syncSidebar()
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus(), tickCmd(), tea.HideCursor}
	if m.external != nil {
		cmds = append(cmds, waitForExternalMsg(m.external))
	}
	return tea.Batch(cmds...)
}

func (m Model) View() string {
	contentWidth := max(1, m.width)
	sidebarVisible := m.sidebar.Visible(m.width)
	if sidebarVisible {
		contentWidth = max(1, m.width-sidebarWidth-1) // 1-cell vertical divider
	}

	viewportInner := m.viewport.View()
	scrollbar := m.renderScrollbar()
	var viewportContent string
	if scrollbar != "" {
		vpLines := strings.Split(viewportInner, "\n")
		scLines := strings.Split(scrollbar, "\n")
		merged := make([]string, 0, len(vpLines)+1)
		merged = append(merged, "") // match ContentPane PaddingTop(1)
		for i := 0; i < len(vpLines) && i < len(scLines); i++ {
			merged = append(merged, vpLines[i]+scLines[i])
		}
		viewportContent = strings.Join(merged, "\n")
	} else {
		viewportContent = viewportInner
	}

	paneStyle := m.styles.ContentPane
	if scrollbar != "" {
		paneStyle = lipgloss.NewStyle().
			Background(lipgloss.Color(theme.BgElev)).
			PaddingLeft(3).
			PaddingRight(2)
	}
	viewportView := paneStyle.Width(contentWidth).Render(viewportContent)

	if m.helpVisible {
		help := renderHelp(m.styles, max(20, contentWidth-4))
		viewportView = composeCenteredOverlay(viewportView, help, contentWidth, lipgloss.Height(viewportView))
	}

	// Horizontal divider: 1-row line of border-soft between transcript and bottom area.
	// Lives inside the main column only — sidebar's vertical divider crosses uninterrupted.
	hDivider := lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", contentWidth))

	inputView := m.renderInputView(contentWidth)
	activityView := m.renderActivityRow(contentWidth)
	statusView := m.status.view(contentWidth)

	mainComponents := []string{viewportView, hDivider}
	if tray := m.renderApprovalTray(contentWidth); tray != "" {
		mainComponents = append(mainComponents, tray)
	}
	mainComponents = append(mainComponents, activityView, inputView, statusView)

	mainColumn := lipgloss.JoinVertical(lipgloss.Left, mainComponents...)
	mainColumn = lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev)).
		Width(contentWidth).
		Height(m.height).
		Render(mainColumn)

	var base string
	if sidebarVisible {
		// 1-cell vertical divider, full window height, floor-to-ceiling.
		vDivider := lipgloss.NewStyle().
			Background(lipgloss.Color(theme.BorderSoft)).
			Width(1).
			Height(m.height).
			Render("")
		if m.sidebarPosition == "right" {
			base = lipgloss.JoinHorizontal(lipgloss.Top,
				mainColumn,
				vDivider,
				m.sidebar.View(m.width, m.height),
			)
		} else {
			base = lipgloss.JoinHorizontal(lipgloss.Top,
				m.sidebar.View(m.width, m.height),
				vDivider,
				mainColumn,
			)
		}
	} else {
		base = mainColumn
	}

	if m.palette.open {
		return composeCenteredOverlay(base, m.palette.View(), m.width, m.height)
	}

	if m.fileList.open {
		return composeCenteredOverlay(base, m.fileList.View(), m.width, m.height)
	}

	if m.filePicker.open {
		overlay := m.filePicker.View()
		base = m.filePicker.PlaceBottomAnchored(base, overlay, m.inputChromeHeight(contentWidth)+m.activityRowHeight(contentWidth))
	}

	if m.contextOverlay.open {
		overlay := m.renderContextOverlay()
		return composeCenteredOverlay(base, overlay, m.width, m.height)
	}

	if m.exitModal.open {
		return composeCenteredOverlay(base, m.renderExitModal(), m.width, m.height)
	}

	return base
}

func (m *Model) syncSidebar() {
	m.sidebar.model = strings.TrimSpace(m.status.model)
	m.sidebar.provider = strings.TrimSpace(m.sidebar.provider)
	if snap := m.git.Snapshot(); snap.ready {
		m.sidebar.branch = snap.branch
		m.sidebar.dirty = snap.dirty
		m.sidebar.ahead = snap.ahead
		m.sidebar.modifiedFiles = append([]gitModifiedFile(nil), snap.modifiedFiles...)
	}
	m.sidebar.workingDir = strings.TrimSpace(m.sidebar.workingDir)
}

func appendStatusContext(base, fragment string) string {
	base = strings.TrimSpace(base)
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return base
	}
	if base == "" {
		return fragment
	}
	return base + " | " + fragment
}

func compactionSidebarSummary(payload output.ContextDiagnosticsEvent) string {
	parts := make([]string, 0, 4)
	if severity := strings.TrimSpace(payload.Severity); severity != "" {
		if severity == "compacting" {
			return "compacting…"
		}
		parts = append(parts, severity)
	}
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("#%d", payload.CompactionCount))
	}
	if state := strings.TrimSpace(payload.SessionState); state != "" {
		parts = append(parts, state)
	}
	if guidance := strings.TrimSpace(payload.RestartGuidance); guidance != "" {
		parts = append(parts, guidance)
	}
	if len(parts) == 0 {
		switch {
		case strings.TrimSpace(payload.SummaryTitle) != "":
			return payload.SummaryTitle
		case payload.CompactedTurns > 0 || payload.CompactedMessages > 0:
			return fmt.Sprintf("compacted %d/%d", payload.CompactedTurns, payload.CompactedMessages)
		default:
			return "compacting"
		}
	}
	return strings.Join(parts, " ")
}

func compactionStatusFragment(payload output.ContextDiagnosticsEvent) string {
	parts := make([]string, 0, 3)
	if severity := strings.TrimSpace(payload.Severity); severity != "" {
		parts = append(parts, severity)
	}
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("compaction #%d", payload.CompactionCount))
	}
	if payload.Severity == "critical" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart now")
	} else if payload.Severity == "warning" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart soon")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func cloneModelBaseURLs(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneModelContexts(src map[string]int) map[string]int {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (m *Model) contextBudgetForModel(name string) int {
	if m == nil || len(m.modelContexts) == 0 {
		return 0
	}
	return m.modelContexts[strings.TrimSpace(name)]
}

func (m *Model) applyContextBudget(payload output.ContextDiagnosticsEvent) bool {
	if m == nil || payload.ContextTokens <= 0 {
		return false
	}
	promptUsed := payload.PromptTokens
	if promptUsed < 0 {
		promptUsed = 0
	}
	budgetUsed := payload.TotalTokens
	if budgetUsed < 0 {
		budgetUsed = 0
	}
	m.sidebar.promptUsed = promptUsed
	m.sidebar.budgetUsed = budgetUsed
	m.sidebar.contextBudget = payload.ContextTokens
	m.ctxInfoPromptTokens = payload.PromptTokens
	m.ctxInfoReservedTokens = payload.ReservedTokens
	m.ctxInfoSafetyTokens = payload.SafetyMarginTokens
	if payload.Turn > 0 {
		m.sidebar.currentTurn = payload.Turn
	}
	m.status.context = fmt.Sprintf("ctx %d/%d", promptUsed, payload.ContextTokens)
	return true
}

func waitForExternalMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return bridgeClosedMsg{}
		}
		msg, ok := <-ch
		if !ok {
			return bridgeClosedMsg{}
		}
		return msg
	}
}

func (m *Model) syncInputChrome() {
	m.input.Prompt = ""
	switch {
	case m.approval.active:
		m.input.Placeholder = "approval pending above — use arrows, tab, enter, or esc"
	case m.activity.busy():
		m.input.Placeholder = "working… esc to interrupt"
	default:
		m.input.Placeholder = "ask steiner — / for commands, @ for files"
	}
	m.status.approvalActive = m.approval.active
	m.status.streaming = m.activity.busy() && !m.approval.active
}

func (m Model) renderActivityRow(contentWidth int) string {
	return m.activity.view(contentWidth, m.styles)
}

func (m *Model) applyInputStyles() {
	base := m.styles.UserBg
	cursorLine := m.styles.UserBg
	placeholder := m.styles.UserBg.Foreground(lipgloss.Color(theme.FgDim))
	text := m.styles.UserBg.Foreground(lipgloss.Color(theme.Fg))
	endOfBuffer := m.styles.UserBg.Foreground(lipgloss.Color(theme.UserSoft))

	m.input.FocusedStyle.Base = base
	m.input.FocusedStyle.CursorLine = cursorLine
	m.input.FocusedStyle.Placeholder = placeholder
	m.input.FocusedStyle.Prompt = base
	m.input.FocusedStyle.Text = text
	m.input.FocusedStyle.EndOfBuffer = endOfBuffer

	m.input.BlurredStyle.Base = base
	m.input.BlurredStyle.CursorLine = cursorLine
	m.input.BlurredStyle.Placeholder = placeholder
	m.input.BlurredStyle.Prompt = base
	m.input.BlurredStyle.Text = text
	m.input.BlurredStyle.EndOfBuffer = endOfBuffer

	m.input.Cursor.TextStyle = text
	m.input.Cursor.Style = text
	_ = m.input.Cursor.SetMode(cursor.CursorHide)
	if m.input.Focused() {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
}

func (m Model) renderInputView(contentWidth int) string {
	bar := m.styles.UserBar.Render("┃")
	bodyWidth := max(1, contentWidth-inputRailWidth)
	innerWidth := m.inputInnerWidth(contentWidth)
	lines, isPlaceholder := m.renderInputLines(innerWidth)

	var sb strings.Builder
	paddingLine := bar + m.styles.UserBg.Width(bodyWidth).Render("")
	for range inputPadY {
		sb.WriteString(paddingLine + "\n")
	}
	for _, line := range lines {
		if isPlaceholder {
			content := m.styles.UserBg.
				Foreground(lipgloss.Color(theme.FgDim)).
				Width(bodyWidth).
				Render(strings.Repeat(" ", inputPadX) + line + strings.Repeat(" ", inputPadX))
			sb.WriteString(bar + content + "\n")
			continue
		}
		lineStyle := lipgloss.NewStyle().Width(innerWidth)
		content := m.styles.UserBg.Width(bodyWidth).Render(strings.Repeat(" ", inputPadX) + lineStyle.Render(line) + strings.Repeat(" ", inputPadX))
		sb.WriteString(bar + content + "\n")
	}
	for range inputPadY {
		sb.WriteString(paddingLine + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m Model) inputChromeHeight(contentWidth int) int {
	return lipgloss.Height(m.renderInputView(contentWidth))
}

func (m Model) activityRowHeight(contentWidth int) int {
	return 1
}

func (m Model) inputInnerWidth(contentWidth int) int {
	return max(1, contentWidth-inputRailWidth-(inputPadX*2)-inputTailFill)
}

func (m Model) renderInputLines(innerWidth int) ([]string, bool) {
	if m.input.Value() != "" {
		return m.renderTypedInputLines(innerWidth), false
	}
	return renderPlaceholderLines(m.input.Placeholder, innerWidth), true
}

func (m Model) renderTypedInputLines(width int) []string {
	if width < 1 {
		width = 1
	}

	valueLines := strings.Split(m.input.Value(), "\n")
	if len(valueLines) == 0 {
		valueLines = []string{""}
	}

	cursorLine := m.input.Line()
	if cursorLine < 0 {
		cursorLine = 0
	}
	if cursorLine >= len(valueLines) {
		cursorLine = len(valueLines) - 1
	}
	lineInfo := m.input.LineInfo()

	lines := make([]string, 0, len(valueLines))
	for i, valueLine := range valueLines {
		wrapped := wrapComposerLine(valueLine, width)
		if i == cursorLine {
			absPos := lineInfo.ColumnOffset
			if absPos < 0 {
				absPos = 0
			}
			row := 0
			col := absPos
			for r, seg := range wrapped {
				segLen := len([]rune(seg))
				if col < segLen || (col == segLen && r == len(wrapped)-1) {
					row = r
					break
				}
				col -= segLen
			}
			wrapped[row] = insertComposerCursor(wrapped[row], col)
		}
		lines = append(lines, wrapped...)
	}
	return lines
}

func wrapComposerLine(line string, width int) []string {
	if width < 1 {
		width = 1
	}
	wrapped := ansi.Hardwrap(line, width, true)
	wrapped = strings.TrimRight(wrapped, "\n")
	if wrapped == "" {
		return []string{""}
	}
	return strings.Split(wrapped, "\n")
}

func insertComposerCursor(line string, col int) string {
	runes := []rune(line)
	col = max(0, min(col, len(runes)))
	if col == len(runes) {
		return string(append(runes, '█'))
	}
	out := make([]rune, 0, len(runes)+1)
	out = append(out, runes[:col]...)
	out = append(out, '█')
	out = append(out, runes[col:]...)
	return string(out)
}

func renderPlaceholderLines(placeholder string, width int) []string {
	if width < 1 {
		width = 1
	}
	wrapped := ansi.Hardwrap(ansi.Wordwrap(placeholder, width, ""), width, true)
	wrapped = strings.TrimRight(wrapped, "\n")
	lines := strings.Split(wrapped, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	cursorWidth := max(0, width-1)
	firstLine := lines[0]
	if cursorWidth == 0 {
		firstLine = ""
	}
	lines[0] = "█" + lipgloss.NewStyle().Width(cursorWidth).Render(firstLine)
	return lines
}
