package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type approvalState struct {
	active  bool
	tool    string
	mode    string
	preview string
}

// contextOverlayState holds the state for the /context report overlay modal.
type contextOverlayState struct {
	OverlayShell
	content      string
	scrollOffset int
	lineCount    int
}

type tickMsg struct{}

type paletteSetAccentMsg struct{ preset string }

type paletteToggleThinkingMsg struct{}

type paletteSwitchModelMsg struct{ name string }

type paletteClearMsg struct{}

type historyLoadedMsg struct{ prompts []string }

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
	external                     <-chan tea.Msg
	autoScroll                   bool
	contentTopPad                int
	skillNames                   []string
	enabledSkills                map[string]bool
	modelNames                   []string
	modelContexts                map[string]int
	onSubmit                     func(string)
	onContextInspect             func()
	onApproval                   func(bool)
	onInterrupt                  func()
	onExitRequested              func()
	onSkillToggle                func(string, bool)
	onModelSwitch                func(string) (string, bool)
	onClear                      func()
	onCompact                    func()
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
	palette                      paletteModel
	fileList                     fileListOverlay
	filePicker                   filePickerOverlay
	contextOverlay               contextOverlayState
	fileViewer                   fileViewerState
	sessionHealthCompactionCount int
	sessionHealthTurn            int
	sessionHealthState           string
	sessionHealthGuidance        string
	sessionHealthNotes           []string
	ctxInfoPromptTokens          int
	ctxInfoReservedTokens        int
	ctxInfoSafetyTokens          int
}

func newModel(cfg Config, external <-chan tea.Msg) Model {
	input := textarea.New()
	input.Prompt = "› "
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

		external:         external,
		autoScroll:       true,
		skillNames:       append([]string(nil), cfg.SkillNames...),
		enabledSkills:    enabledSkills,
		modelNames:       append([]string(nil), cfg.ModelNames...),
		modelContexts:    cloneModelContexts(cfg.ModelContexts),
		onSubmit:         cfg.OnSubmit,
		onContextInspect: cfg.OnContextInspect,
		onApproval:       cfg.OnApproval,
		onInterrupt:      cfg.OnInterrupt,
		onExitRequested:  cfg.OnExitRequested,
		onSkillToggle:    cfg.OnSkillToggle,
		onModelSwitch:    cfg.OnModelSwitch,
		onClear:          cfg.OnClear,
		onCompact:        cfg.OnCompact,
		activeTheme:      t,
		styles:           theme.BuildStyles(accentHex),
		inputHistory:     []string{},
		historyIdx:       0,
		historyDraft:     "",
		fileHistory:      []string{},
		fileHistoryIdx:   -1,
		showThinking:     cfg.ShowThinking,
		accentPreset:     cfg.AccentPreset,
	}
	m.status.model = strings.TrimSpace(cfg.Model)
	m.sidebar.model = strings.TrimSpace(cfg.Model)
	m.sidebar.contextBudget = m.contextBudgetForModel(m.sidebar.model)
	m.sidebar.provider = strings.TrimSpace(cfg.ProviderBaseURL)
	m.sidebar.maxTurns = cfg.MaxTurns
	m.sidebar.homeDir = strings.TrimSpace(cfg.HomeDir)
	m.sidebar.workingDir = strings.TrimSpace(cfg.WorkingDir)
	// Wire up git error logger to emit events through the model
	gitErrorLogger = func(err error) {
		// Errors are captured and will be emitted on next tick via lastRenderErr
		m.content.lastRenderErr = err
	}
	// Wire up render error logger
	renderErrorLogger = func(err error) {
		m.content.lastRenderErr = err
	}
	m.git.Refresh(context.Background())
	m.syncSidebar()
	m.layout()

	// Set styles on content and sidebar
	m.content.styles = m.styles
	m.content.glamourStyleSheet = m.activeTheme.GlamourStyleSheet()
	m.content.collapseState = make(map[int]bool)
	m.content.showThinking = m.showThinking
	m.sidebar.styles = m.styles
	m.status.styles = m.styles

	// Set textarea styles
	m.input.FocusedStyle.Base = m.styles.InputArea
	m.input.BlurredStyle.Base = m.styles.InputArea

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
	cmds := []tea.Cmd{m.input.Focus(), tickCmd()}
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
			PaddingTop(1).
			PaddingLeft(3).
			PaddingRight(2)
	}
	viewportView := paneStyle.Width(contentWidth).Render(viewportContent)

	if m.helpVisible {
		help := renderHelp(m.styles, max(20, contentWidth-4))
		viewportView = lipgloss.Place(contentWidth, lipgloss.Height(viewportView),
			lipgloss.Center, lipgloss.Center,
			help,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	// Horizontal divider: 1-row line of border-soft between transcript and bottom area.
	// Lives inside the main column only — sidebar's vertical divider crosses uninterrupted.
	hDivider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", contentWidth))

	inputView := m.input.View()
	if m.input.Focused() && m.content.streamingPhase == "" {
		inputView = m.styles.InputFocusBorder.Width(contentWidth - 2).Render(inputView)
	}
	statusView := m.status.view(contentWidth)

	mainComponents := []string{viewportView, hDivider}
	mainComponents = append(mainComponents, inputView, statusView)

	mainColumn := lipgloss.JoinVertical(lipgloss.Left, mainComponents...)

	var base string
	if sidebarVisible {
		// 1-cell vertical divider, full window height, floor-to-ceiling.
		vDivider := lipgloss.NewStyle().
			Background(lipgloss.Color(theme.BorderSoft)).
			Width(1).
			Height(m.height).
			Render("")
		base = lipgloss.JoinHorizontal(lipgloss.Top,
			mainColumn,
			vDivider,
			m.sidebar.View(m.width, m.height),
		)
	} else {
		base = mainColumn
	}

	if m.palette.open {
		overlay := m.palette.View()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	if m.fileList.open {
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			m.fileList.View(),
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	if m.filePicker.open {
		overlay := m.filePicker.View()
		inputHeight := 1
		if m.input.Focused() && m.content.streamingPhase == "" {
			inputHeight = 3
		}
		base = m.filePicker.PlaceBottomAnchored(base, overlay, inputHeight)
	}

	if m.contextOverlay.open {
		overlay := m.renderContextOverlay()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	if m.fileViewer.open {
		overlay := m.renderFileViewer()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	return base
}

func (m *Model) syncSidebar() {
	m.sidebar.model = strings.TrimSpace(m.status.model)
	m.sidebar.provider = strings.TrimSpace(m.sidebar.provider)
	m.sidebar.currentTurn = m.status.turn
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
		m.status.turn = payload.Turn
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

func isApprovalAccepted(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y", "yes", "a", "always":
		return true
	default:
		return false
	}
}

func approvalDecisionText(allowed bool, toolName string) string {
	state := "denied"
	if allowed {
		state = "approved"
	}
	if toolName == "" {
		return state
	}
	return fmt.Sprintf("%s %s", toolName, state)
}
