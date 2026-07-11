package tui

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// resolveAccentPreset resolves an accent preset name to its hex color value.
// If preset is "random", it returns a random preset from AccentPresets.
// Otherwise, it returns the hex value for the named preset, or "" if not found.
// randIntn is used for random selection and allows deterministic testing.
func resolveAccentPreset(preset string, randIntn func(int) int) string {
	if preset == "random" {
		// Collect all preset keys in sorted order for deterministic randomness
		keys := make([]string, 0, len(theme.AccentPresets))
		for k := range theme.AccentPresets {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		if len(keys) == 0 {
			return ""
		}
		idx := randIntn(len(keys))
		return theme.AccentPresets[keys[idx]]
	}
	return theme.AccentPresets[preset]
}

func newModel(cfg Config, external <-chan tea.Msg) Model {
	input := newModelInput()
	enabledSkills := make(map[string]bool, len(cfg.SkillNames))
	for _, name := range cfg.SkillNames {
		enabledSkills[name] = false
	}

	accentHex := resolveAccentPreset(cfg.AccentPreset, rand.Intn)
	if accentHex == "" {
		accentHex = theme.AccentPresets["amber"]
	}

	m := Model{
		width:    80,
		height:   24,
		viewport: viewport.New(viewport.WithWidth(80), viewport.WithHeight(22)),
		input:    input,
		sidebar:  newSidebarState(),
		git:      newGitState(cfg.WorkingDir),

		external:                   external,
		autoScroll:                 true,
		skillNames:                 append([]string(nil), cfg.SkillNames...),
		skillDescriptions:          cloneStringMap(cfg.SkillDescriptions),
		enabledSkills:              enabledSkills,
		modelNames:                 append([]string(nil), cfg.ModelNames...),
		modelContexts:              cloneModelContexts(cfg.ModelContexts),
		modelBaseURLs:              cloneModelBaseURLs(cfg.ModelBaseURLs),
		modelProviderNames:         cloneModelProviderNames(cfg.ModelProviderNames),
		modelReasoningCapabilities: cloneModelReasoningCapabilities(cfg.ModelReasoningCapabilities),
		modelReasoningEfforts:      cloneStringMap(cfg.ModelReasoningEfforts),
		reasoningLabels:            newReasoningLabels(cfg.ModelReasoningEfforts, cfg.ModelReasoningCapabilities),
		controller:                 cfg.Controller,
		recorder:                   cfg.Recorder,
		activeTheme:                resolveTheme(cfg.Theme),
		styles:                     theme.BuildStyles(accentHex),
		inputHistory:               []string{},
		historyIdx:                 0,
		historyDraft:               "",
		fileHistory:                []string{},
		fileHistoryIdx:             -1,
		showThinking:               cfg.ShowThinking,
		accentPreset:               cfg.AccentPreset,
		sidebarPosition:            cfg.SidebarPosition,
		mousePressX:                -1,
		mousePressY:                -1,
		screenLines:                new([]string),
		oneshotRunnerFactory:       cfg.OneshotRunnerFactory,
		imageStore:                 cfg.ImageStore,
		visionCapabilities:         cfg.VisionCapabilities,
		sessionResetCleanup:        cfg.SessionResetCleanup,
		resolveReasoningFunc:       cfg.ResolveReasoningFunc,
	}

	m.notifier = cfg.Notifier
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
	input.MaxHeight = 30
	input.KeyMap.CharacterBackward = key.NewBinding(key.WithKeys("left"))
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	input.Focus()
	return input
}

type overlayKeyHandler interface {
	matches(Model) bool
	handle(*Model, tea.KeyPressMsg) tea.Cmd
}

type overlayKeyHandlerFunc struct {
	match func(Model) bool
	apply func(*Model, tea.KeyPressMsg) tea.Cmd
}

func (h overlayKeyHandlerFunc) matches(m Model) bool {
	return h.match(m)
}

func (h overlayKeyHandlerFunc) handle(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	return h.apply(m, msg)
}

var overlayKeyHandlers = []overlayKeyHandler{
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.modelPicker.IsOpen() && m.modelPicker.IsWorkflowHandoff() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleModelPickerKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.workflowHandoff.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleWorkflowHandoffModalKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.exitModal.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleExitModalKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.slashOverlay.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleSlashOverlayKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.fileList.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			var cmd tea.Cmd
			m.fileList, cmd = m.fileList.Update(msg)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.contextOverlay.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next := m.handleContextOverlayKey(msg)
			*m = next.(Model)
			return nil
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.filePicker.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleFilePickerKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.sessionPicker.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleSessionPickerKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.oneshotResumePicker.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleOneshotResumePickerKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.planPicker.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handlePlanPickerKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.accentPicker.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleAccentPickerKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.modelPicker.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleModelPickerKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
	overlayKeyHandlerFunc{
		match: func(m Model) bool { return m.reasoningPicker.IsOpen() },
		apply: func(m *Model, msg tea.KeyPressMsg) tea.Cmd {
			next, cmd := m.handleReasoningPickerKey(msg)
			*m = next.(Model)
			return cmd
		},
	},
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
	m.primaryModel = strings.TrimSpace(cfg.Model)
	m.currentModelAlias = strings.TrimSpace(cfg.CurrentModelAlias)
	m.status.model = m.primaryModel
	m.sidebar.model = m.primaryModel
	m.sidebar.version = cfg.Version
	m.sidebar.contextBudget = m.contextBudgetForModel(m.sidebar.model)
	m.sidebar.reasoning = m.reasoningLabels[m.currentModelAlias]
	m.sidebar.provider = strings.TrimSpace(cfg.ProviderBaseURL)
	m.sidebar.providerName = strings.TrimSpace(cfg.ProviderName)
	m.sidebar.maxTurns = cfg.MaxTurns
	m.sidebar.homeDir = strings.TrimSpace(cfg.HomeDir)
	m.sidebar.workingDir = strings.TrimSpace(cfg.WorkingDir)
	m.git.Refresh(context.Background())
	m.syncSidebar()
	m.layout()

	m.content.styles = m.styles
	m.content.skillNames = m.skillNames
	m.content.setGlamourStyleSheet(accentHex)
	m.content.collapseState = make(map[int]bool)
	m.content.showThinking = m.showThinking
	m.sidebar.styles = m.styles
	m.status.styles = m.styles
	m.activity = newActivityState(m.styles)

	m.applyInputStyles()
	m.input.Focus()
	m.initializeOverlays(cfg)
	if m.notifier != nil {
		if ok, reason := m.notifier.Availability(); !ok {
			m.content.AppendUser(fmt.Sprintf("desktop notifications: %s", reason))
		}
	}
}

func (m *Model) initializeOverlays(cfg Config) {

	m.fileList = newFileListOverlay(m.styles)
	m.filePicker = newFilePickerOverlay(m.styles)
	m.filePicker.width = m.width
	m.filePicker.height = m.height

	m.sessionPicker = newSessionPickerOverlay(m.styles)
	m.sessionPicker.width = m.width
	m.sessionPicker.height = m.height
	m.sessionStore = cfg.SessionStore

	m.oneshotResumePicker = newOneshotResumePickerOverlay(m.styles)
	m.oneshotResumePicker.width = m.width
	m.oneshotResumePicker.height = m.height

	m.modelPicker = newModelPickerOverlay(m.styles)
	m.modelPicker.width = m.width
	m.modelPicker.height = m.height

	m.reasoningPicker = newReasoningPickerOverlay(m.styles)
	m.reasoningPicker.width = m.width
	m.reasoningPicker.height = m.height

	m.planPicker = newPlanPickerOverlay(m.styles)
	m.planPicker.width = m.width
	m.planPicker.height = m.height

	m.accentPicker = newAccentPickerOverlay(m.styles)
	m.accentPicker.width = m.width
	m.accentPicker.height = m.height

	m.slashOverlay = newSlashOverlay(m.styles)
	m.slashOverlay.width = m.width
	m.slashOverlay.height = m.height

	m.workflowHandoff = workflowHandoffModalState{}
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus(), tickCmd()}
	if m.external != nil {
		cmds = append(cmds, waitForExternalMsg(m.external))
	}
	if m.resolveReasoningFunc != nil {
		resolve := m.resolveReasoningFunc
		cmds = append(cmds, func() tea.Msg {
			caps, efforts := resolve()
			return modelReasoningResolvedMsg{capabilities: caps, efforts: efforts}
		})
	}
	return tea.Batch(cmds...)
}
