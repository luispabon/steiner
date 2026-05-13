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

	"github.com/luispabon/steiner/internal/tui/theme"
)

func newModel(cfg Config, external <-chan tea.Msg) Model {
	input := newModelInput()
	enabledSkills := make(map[string]bool, len(cfg.SkillNames))
	for _, name := range cfg.SkillNames {
		enabledSkills[name] = true
	}

	accentHex := theme.AccentPresets[cfg.AccentPreset]
	if accentHex == "" {
		accentHex = theme.AccentPresets["amber"]
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
		activeTheme:     resolveTheme(cfg.Theme),
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

	m.configureModelState(cfg, accentHex)
	return m
}

func newModelInput() textarea.Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "ask steiner — / for commands, @ for files"
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.SetHeight(1)
	input.MaxHeight = 10
	input.KeyMap.CharacterBackward = key.NewBinding(key.WithKeys("left"))
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	input.Focus()
	return input
}

func resolveTheme(name string) theme.Theme {
	if name == "" {
		return theme.Default()
	}
	t, err := theme.Get(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "theme not found: %s, using default\n", name)
		return theme.Default()
	}
	if t == nil {
		return theme.Default()
	}
	return t
}

func (m *Model) configureModelState(cfg Config, accentHex string) {
	m.status.model = strings.TrimSpace(cfg.Model)
	m.sidebar.model = strings.TrimSpace(cfg.Model)
	m.sidebar.version = cfg.Version
	m.sidebar.contextBudget = m.contextBudgetForModel(m.sidebar.model)
	m.sidebar.provider = strings.TrimSpace(cfg.ProviderBaseURL)
	m.sidebar.maxTurns = cfg.MaxTurns
	m.sidebar.homeDir = strings.TrimSpace(cfg.HomeDir)
	m.sidebar.workingDir = strings.TrimSpace(cfg.WorkingDir)
	m.git.Refresh(context.Background())
	m.syncSidebar()
	m.layout()

	m.content.styles = m.styles
	m.content.setGlamourStyleSheet(accentHex)
	m.content.collapseState = make(map[int]bool)
	m.content.showThinking = m.showThinking
	m.content.showInternalScaffoldInference = cfg.ShowInternalScaffoldInference
	m.sidebar.styles = m.styles
	m.status.styles = m.styles
	m.activity = newActivityState(m.styles)

	m.applyInputStyles()
	m.input.Focus()
	m.initializeOverlays(cfg)
}

func (m *Model) initializeOverlays(cfg Config) {
	m.palette = newPalette(m.styles, buildDefaultPaletteItems())
	m.palette.width = m.width
	m.palette.height = m.height

	m.fileList = newFileListOverlay(m.styles)
	m.fileList.width = m.width
	m.fileList.height = m.height

	m.filePicker = newFilePickerOverlay(m.styles)
	m.filePicker.width = m.width
	m.filePicker.height = m.height

	m.sessionPicker = newSessionPickerOverlay(m.styles)
	m.sessionPicker.width = m.width
	m.sessionPicker.height = m.height
	m.sessionStore = cfg.SessionStore
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
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus(), tickCmd(), tea.HideCursor}
	if m.external != nil {
		cmds = append(cmds, waitForExternalMsg(m.external))
	}
	return tea.Batch(cmds...)
}
