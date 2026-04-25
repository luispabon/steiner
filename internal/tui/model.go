package tui

import (
	"context"
	"fmt"
	"os"
	"slices"
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

type tickMsg struct{}

type paletteSetAccentMsg struct{ preset string }
type paletteToggleThinkingMsg struct{}
type paletteSwitchModelMsg struct{ name string }
type paletteClearMsg struct{}

type Model struct {
	width                int
	height               int
	viewport             viewport.Model
	input                textarea.Model
	content              contentBuffer
	status               statusState
	sidebar              sidebarState
	git                  *gitState
	keys                 keyMap
	approval             approvalState
	external             <-chan tea.Msg
	autoScroll           bool
	skillNames           []string
	enabledSkills        map[string]bool
	modelNames           []string
	modelContexts        map[string]int
	onSubmit             func(string)
	onContextInspect     func()
	onApproval           func(bool)
	onSkillToggle        func(string, bool)
	onModelSwitch        func(string)
	onClear              func()
	activeTheme          theme.Theme
	styles               theme.Styles
	inputHistory         []string
	historyIdx           int
	historyDraft         string
	completionCandidates []string
	completionIdx        int
	helpVisible          bool
	showThinking         bool
	accentPreset         string
	palette              paletteModel
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
		width:            80,
		height:           24,
		viewport:         viewport.New(80, 22),
		input:            input,
		sidebar:          newSidebarState(),
		git:              newGitState(cfg.WorkingDir),
		keys:             defaultKeyMap(),
		external:         external,
		autoScroll:       true,
		skillNames:       append([]string(nil), cfg.SkillNames...),
		enabledSkills:    enabledSkills,
		modelNames:       append([]string(nil), cfg.ModelNames...),
		modelContexts:    cloneModelContexts(cfg.ModelContexts),
		onSubmit:         cfg.OnSubmit,
		onContextInspect: cfg.OnContextInspect,
		onApproval:       cfg.OnApproval,
		onSkillToggle:    cfg.OnSkillToggle,
		onModelSwitch:    cfg.OnModelSwitch,
		onClear:          cfg.OnClear,
		activeTheme:      t,
		styles:           theme.BuildStyles(accentHex),
		inputHistory:     []string{},
		historyIdx:       0,
		historyDraft:     "",
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case paletteClearMsg:
		m.content.Clear()
		m.sidebar.promptUsed = 0
		m.sidebar.budgetUsed = 0
		if m.sidebar.contextBudget > 0 {
			m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
		} else {
			m.status.context = ""
		}
		m.syncSidebar()
		if m.onClear != nil {
			m.onClear()
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	case paletteToggleThinkingMsg:
		m.showThinking = !m.showThinking
		m.content.showThinking = m.showThinking
		m.syncViewport()
		return m, nil
	case paletteSwitchModelMsg:
		if m.onModelSwitch != nil {
			m.onModelSwitch(msg.name)
		}
		m.status.model = msg.name
		m.sidebar.contextBudget = m.contextBudgetForModel(msg.name)
		m.sidebar.promptUsed = 0
		m.sidebar.budgetUsed = 0
		if m.sidebar.contextBudget > 0 {
			m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
		} else {
			m.status.context = ""
		}
		m.syncSidebar()
		m.content.AppendLine(fmt.Sprintf("status: model switched to %s", msg.name))
		m.syncViewport()
		return m, nil
	case paletteSetAccentMsg:
		accentHex := theme.AccentPresets[msg.preset]
		if accentHex == "" {
			accentHex = theme.AccentPresets["amber"]
		}
		m.accentPreset = msg.preset
		m.styles = theme.BuildStyles(accentHex)
		m.content.styles = m.styles
		m.sidebar.styles = m.styles
		m.status.styles = m.styles
		m.input.FocusedStyle.Base = m.styles.InputArea
		m.input.BlurredStyle.Base = m.styles.InputArea
		m.palette.styles = m.styles
		m.syncViewport()
		return m, nil
	case tickMsg:
		m.content.tickCount++
		m.sidebar.tickCount = m.content.tickCount
		m.status.streaming = m.content.streamingPhase != ""
		m.status.promptUsed = m.sidebar.promptUsed
		m.status.contextBudget = m.sidebar.contextBudget
		if !m.approval.active {
			if m.content.streamingPhase != "" {
				m.input.Placeholder = "streaming… esc to interrupt"
			} else {
				m.input.Placeholder = "ask steiner — / for commands, @ for files"
			}
		}
		m.syncViewport()
		return m, tickCmd()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.palette.width = msg.Width
		m.palette.height = msg.Height
		m.layout()
		return m, nil
	case runtimeEventMsg:
		m.applyEvent(msg.Event)
		if m.external != nil {
			return m, waitForExternalMsg(m.external)
		}
		return m, nil
	case bridgeClosedMsg:
		return m, nil
	case tea.MouseMsg:
		m.handleMouse(msg)
		return m, nil
	case tea.KeyMsg:
		// If palette is open, route all keys to it first
		if m.palette.open {
			var cmd tea.Cmd
			m.palette, cmd = m.palette.Update(msg)
			return m, cmd
		}

		// Reset completion state on any non-Tab key
		if msg.Type != tea.KeyTab {
			m.completionCandidates = nil
			m.completionIdx = 0
		}

		// Handle ? for help toggle (only when textarea is empty)
		if msg.String() == "?" && strings.TrimSpace(m.input.Value()) == "" {
			m.helpVisible = !m.helpVisible
			return m, nil
		}

		// Handle Escape: interrupt streaming first (takes priority over help panel)
		if msg.Type == tea.KeyEsc && m.content.streamingPhase != "" {
			m.content.AppendLine("[interrupted]")
			m.content.streamingPhase = ""
			m.status.streaming = false
			m.input.Placeholder = "ask steiner — / for commands, @ for files"
			m.syncViewport()
			return m, nil
		}

		// Handle Escape to close help
		if msg.Type == tea.KeyEsc && m.helpVisible {
			m.helpVisible = false
			return m, nil
		}

		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyCtrlD:
			return m, tea.Quit
		case tea.KeyCtrlP:
			m.palette = m.palette.Open()
			m.palette.width = m.width
			m.palette.height = m.height
			return m, nil
		case tea.KeyCtrlB:
			m.sidebar.Toggle()
			m.layout()
			return m, nil
		case tea.KeyTab:
			current := m.input.Value()
			if !strings.HasPrefix(current, "/") {
				// no "/" prefix — pass Tab through to textarea (does nothing meaningful)
				break
			}
			// build or advance candidates
			if len(m.completionCandidates) == 0 {
				m.completionCandidates = buildCompletionCandidates(current, m.skillNames, m.modelNames)
				m.completionIdx = 0
			}
			if len(m.completionCandidates) == 0 {
				return m, nil // no matches
			}
			// set the value to current candidate
			m.input.SetValue(m.completionCandidates[m.completionIdx])
			m.completionIdx = (m.completionIdx + 1) % len(m.completionCandidates)
			return m, nil
		case tea.KeyUp:
			if m.input.Line() == 0 && len(m.inputHistory) > 0 {
				// navigate history backward
				if m.historyIdx == 0 {
					m.historyDraft = m.input.Value()
				}
				if m.historyIdx < len(m.inputHistory) {
					m.historyIdx++
					m.input.SetValue(m.inputHistory[m.historyIdx-1])
				}
				return m, nil
			}
			// cursor not on first line — pass to textarea
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		case tea.KeyDown:
			if m.historyIdx > 0 {
				m.historyIdx--
				if m.historyIdx == 0 {
					m.input.SetValue(m.historyDraft)
				} else {
					m.input.SetValue(m.inputHistory[m.historyIdx-1])
				}
				return m, nil
			}
			// pass to textarea
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		case tea.KeyPgUp:
			m.scrollUp(maxInt(1, m.viewport.Height))
			return m, nil
		case tea.KeyPgDown:
			m.scrollDown(maxInt(1, m.viewport.Height))
			return m, nil
		case tea.KeyEnter:
			return m.handleEnter()
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	contentWidth := maxInt(1, m.width)
	sidebarVisible := m.sidebar.Visible(m.width)
	if sidebarVisible {
		contentWidth = maxInt(1, m.width-sidebarWidth)
	}
	contentView := m.styles.ContentPane.Width(contentWidth).Render(m.viewport.View())
	if sidebarVisible {
		contentView = lipgloss.JoinHorizontal(lipgloss.Top, contentView, m.sidebar.View(m.width, m.viewport.Height))
	}

	// Overlay help panel if visible
	if m.helpVisible {
		help := renderHelp(m.styles, maxInt(20, contentWidth-4))
		contentView = lipgloss.Place(contentWidth, m.viewport.Height,
			lipgloss.Center, lipgloss.Center,
			help,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	inputView := m.input.View()
	statusView := m.status.view(m.width, m.keys.hints(m.approval.active))

	base := lipgloss.JoinVertical(
		lipgloss.Left,
		contentView,
		inputView,
		statusView,
	)

	if m.palette.open {
		overlay := m.palette.View()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	return base
}

func (m *Model) layout() {
	contentHeight := m.height - 5
	if contentHeight < 1 {
		contentHeight = 1
	}
	contentWidth := m.width
	if m.sidebar.Visible(m.width) {
		contentWidth = m.width - sidebarWidth
	}
	if contentWidth < 1 {
		contentWidth = 1
	}
	m.viewport.Width = maxInt(1, contentWidth-2)
	m.viewport.Height = maxInt(1, contentHeight-2)
	m.input.SetWidth(maxInt(1, contentWidth))
	m.syncViewport()
}

func (m *Model) syncViewport() {
	m.viewport.SetContent(m.content.String(m.viewport.Width))
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) applyEvent(event output.Event) {
	m.content.AppendEvent(event)

	switch payload := event.Payload.(type) {
	case output.RunStartedEvent:
		if payload.Model != "" {
			m.status.model = payload.Model
			m.sidebar.model = payload.Model
		}
		if payload.MaxTurns > 0 {
			m.sidebar.maxTurns = payload.MaxTurns
		}
		m.status.mode = "running"
	case output.RunFinishedEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
	case output.StopReasonEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
	case output.TurnStartedEvent:
		m.status.turn = payload.Turn
		m.sidebar.currentTurn = payload.Turn
		if payload.Model != "" {
			m.status.model = payload.Model
			m.sidebar.model = payload.Model
		}
	case output.ContextDiagnosticsEvent:
		m.applyContextBudget(payload)
		if payload.Kind == "compaction" {
			m.sidebar.compaction = compactionSidebarSummary(payload)
			m.status.context = appendStatusContext(m.status.context, compactionStatusFragment(payload))
		}
		if payload.Kind == "session_health" {
			m.sidebar.compaction = compactionSidebarSummary(payload)
			m.status.context = appendStatusContext(m.status.context, compactionStatusFragment(payload))
		}
	case output.ApprovalEvent:
		switch event.Type {
		case output.EventTypeApprovalRequested:
			m.approval = approvalState{
				active:  true,
				tool:    payload.Tool,
				mode:    payload.Mode,
				preview: payload.Preview,
			}
			m.status.mode = "approval"
			m.input.Reset()
			m.input.Prompt = "approve> "
			m.input.Placeholder = "approve? y/n/d"
		case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
			m.approval = approvalState{}
			m.status.mode = "running"
			m.input.Prompt = "› "
			m.input.Placeholder = "ask steiner — / for commands, @ for files"
		}
	}

	if event.Type == output.EventTypeToolCallFinished {
		m.git.Refresh(context.Background())
	}
	m.syncSidebar()
	m.syncViewport()
}

func (m *Model) syncSidebar() {
	m.sidebar.model = strings.TrimSpace(m.status.model)
	m.sidebar.provider = strings.TrimSpace(m.sidebar.provider)
	m.sidebar.currentTurn = m.status.turn
	if snap := m.git.Snapshot(); snap.ready {
		m.sidebar.branch = snap.branch
		m.sidebar.dirty = snap.dirty
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

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	if m.approval.active {
		allowed := isApprovalAccepted(value)
		if m.onApproval != nil {
			m.onApproval(allowed)
		}
		m.content.AppendLine(fmt.Sprintf("approval: %s", approvalDecisionText(allowed, m.approval.tool)))
		m.approval = approvalState{}
		m.status.mode = "running"
		m.input.Reset()
		m.input.Prompt = "› "
		m.input.Placeholder = "ask steiner — / for commands, @ for files"
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}

	action := parseInput(value, m.enabledSkills)
	if action.quit {
		return m, tea.Quit
	}
	if action.clear {
		m.content.Clear()
		m.sidebar.promptUsed = 0
		m.sidebar.budgetUsed = 0
		if m.sidebar.contextBudget > 0 {
			m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
		} else {
			m.status.context = ""
		}
		m.syncSidebar()
		if m.onClear != nil {
			m.onClear()
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.inspectContext {
		if m.onContextInspect != nil {
			m.onContextInspect()
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.listSkills {
		names := append([]string(nil), m.skillNames...)
		slices.Sort(names)
		if len(names) == 0 {
			m.content.AppendLine("status: no skills configured")
		} else {
			m.content.AppendLine("status: skills " + strings.Join(names, ", "))
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.toggleSkill != "" {
		m.enabledSkills[action.toggleSkill] = action.toggleEnable
		if m.onSkillToggle != nil {
			m.onSkillToggle(action.toggleSkill, action.toggleEnable)
		}
		state := "disabled"
		if action.toggleEnable {
			state = "enabled"
		}
		m.content.AppendLine(fmt.Sprintf("status: skill %s %s", action.toggleSkill, state))
		m.input.Reset()
		m.historyIdx = 0
		m.syncSidebar()
		m.syncViewport()
		return m, nil
	}
	if action.listModels {
		if len(m.modelNames) == 0 {
			m.content.AppendLine("status: no named models configured")
		} else {
			m.content.AppendLine("status: models " + strings.Join(m.modelNames, ", "))
		}
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.switchModel != "" {
		if m.onModelSwitch != nil {
			m.onModelSwitch(action.switchModel)
		}
		m.status.model = action.switchModel
		m.sidebar.contextBudget = m.contextBudgetForModel(action.switchModel)
		m.sidebar.promptUsed = 0
		m.sidebar.budgetUsed = 0
		if m.sidebar.contextBudget > 0 {
			m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
		} else {
			m.status.context = ""
		}
		m.syncSidebar()
		m.content.AppendLine(fmt.Sprintf("status: model switched to %s", action.switchModel))
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
		return m, nil
	}
	if action.submit != "" {
		// prepend to history (non-empty submits only)
		if value != "" {
			m.inputHistory = append([]string{value}, m.inputHistory...)
			m.historyIdx = 0
		}
		if m.onSubmit != nil {
			m.onSubmit(action.submit)
		}
		m.content.AppendLine("you> " + action.submit)
		m.input.Reset()
		m.historyIdx = 0
		m.syncViewport()
	}
	return m, nil
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
	if payload.Turn > 0 {
		m.status.turn = payload.Turn
		m.sidebar.currentTurn = payload.Turn
	}
	m.status.context = fmt.Sprintf("ctx %d/%d", promptUsed, payload.ContextTokens)
	return true
}

func (m *Model) handleMouse(msg tea.MouseMsg) {
	if msg.Action != tea.MouseActionPress {
		return
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollUp(m.viewport.MouseWheelDelta)
	case tea.MouseButtonWheelDown:
		m.scrollDown(m.viewport.MouseWheelDelta)
	case tea.MouseButtonLeft:
		m.handleLeftClick(msg.Y)
	}
}

func (m *Model) handleLeftClick(termY int) {
	// The viewport content area starts below the status bar and input area.
	// We need the content-area row. The viewport itself is positioned at
	// some Y within the terminal — approximate by using termY directly
	// adjusted for scroll offset.
	// content line = termY + m.viewport.YOffset
	// (viewport renders from its YOffset in the scrollable content)
	contentLine := termY + m.viewport.YOffset

	if contentLine < 0 || len(m.content.segmentHeights) == 0 {
		return
	}

	// Walk segmentHeights to find which segment index this line falls in
	cumulative := 0
	for i, h := range m.content.segmentHeights {
		if h == 0 {
			continue
		}
		if contentLine < cumulative+h {
			// Click landed in segment i — toggle if collapsible
			seg := &m.content.segments[i]
			switch seg.kind {
			case segmentToolCall:
				if seg.toolData != nil {
					seg.toolData.collapsed = !seg.toolData.collapsed
					m.syncViewport()
				}
			case segmentThinkingBlock:
				if seg.thinkData != nil {
					seg.thinkData.collapsed = !seg.thinkData.collapsed
					m.syncViewport()
				}
			}
			return
		}
		cumulative += h
	}
}

func (m *Model) scrollUp(lines int) {
	if lines < 1 {
		lines = 1
	}
	m.autoScroll = false
	m.viewport.ScrollUp(lines)
}

func (m *Model) scrollDown(lines int) {
	if lines < 1 {
		lines = 1
	}
	m.viewport.ScrollDown(lines)
	if m.viewport.AtBottom() {
		m.autoScroll = true
	}
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
	case "y", "yes":
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
