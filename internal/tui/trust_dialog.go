package tui

import (
	"context"
	"io"
	"math/rand"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tui/prefs"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// TrustChoice is the user's answer in the trust dialog.
type TrustChoice int

const (
	// TrustDeny is the zero value: deny and exit.
	TrustDeny TrustChoice = iota
	// TrustSession trusts the project for this run only.
	TrustSession
	// TrustAlways trusts the project permanently.
	TrustAlways
)

// trustChoiceCycle is the visual left-to-right order the trust dialog cycles
// through with arrow keys or tab, independent of TrustChoice's int values.
var trustChoiceCycle = []TrustChoice{TrustAlways, TrustSession, TrustDeny}

// DialogIO holds the terminal streams a standalone dialog uses.
type DialogIO struct {
	In  io.Reader
	Out io.Writer
}

// dialogStyles loads user-level TUI preferences and builds styles for a
// standalone dialog. It never reads steiner project or global config, and it
// never fails loudly: any error loading prefs, resolving the accent, or
// resolving the palette falls back to defaults.
func dialogStyles() theme.Styles {
	p, err := prefs.Load()
	if err != nil {
		p = prefs.DefaultPrefs()
	}
	accentHex := resolveAccentPreset(p.Accent, rand.Intn)
	if accentHex == "" {
		accentHex = theme.AccentPresets["amber"]
	}
	palette, err := theme.ResolvePalette(p.SidebarBG, p.ContentBG)
	if err != nil {
		palette = theme.DefaultPalette()
	}
	return theme.BuildStyles(accentHex, palette)
}

// trustDialogModel is the bubbletea model backing RunTrustDialog.
type trustDialogModel struct {
	insp     config.ProjectInspection
	styles   theme.Styles
	selected TrustChoice
	done     bool
	width    int
	height   int
	scroll   int
}

func newTrustDialogModel(insp config.ProjectInspection) *trustDialogModel {
	return &trustDialogModel{
		insp:     insp,
		styles:   dialogStyles(),
		selected: TrustDeny,
		width:    80,
		height:   24,
	}
}

// Init implements tea.Model.
func (m *trustDialogModel) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m *trustDialogModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *trustDialogModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrl(msg, 'c') {
		return m.decide(TrustDeny)
	}
	if msg.Text != "" {
		if model, cmd, handled := m.handleTextKey(msg); handled {
			return model, cmd
		}
	}
	return m.handleCodeKey(msg)
}

func (m *trustDialogModel) handleTextKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "a":
		model, cmd := m.decide(TrustAlways)
		return model, cmd, true
	case "s":
		model, cmd := m.decide(TrustSession)
		return model, cmd, true
	case "d":
		model, cmd := m.decide(TrustDeny)
		return model, cmd, true
	case "j":
		m.adjustScroll(1)
		return m, nil, true
	case "k":
		m.adjustScroll(-1)
		return m, nil, true
	}
	return m, nil, false
}

func (m *trustDialogModel) handleCodeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEsc:
		return m.decide(TrustDeny)
	case tea.KeyEnter:
		return m.decide(m.selected)
	case tea.KeyLeft, tea.KeyUp:
		m.cycleSelected(-1)
	case tea.KeyRight, tea.KeyDown:
		m.cycleSelected(1)
	case tea.KeyTab:
		if msg.Mod&tea.ModShift != 0 {
			m.cycleSelected(-1)
		} else {
			m.cycleSelected(1)
		}
	case tea.KeyPgUp:
		m.adjustScroll(-m.changeListHeight())
	case tea.KeyPgDown:
		m.adjustScroll(m.changeListHeight())
	}
	return m, nil
}

func (m *trustDialogModel) decide(choice TrustChoice) (tea.Model, tea.Cmd) {
	m.selected = choice
	m.done = true
	return m, tea.Quit
}

func (m *trustDialogModel) cycleSelected(delta int) {
	idx := 0
	for i, c := range trustChoiceCycle {
		if c == m.selected {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(trustChoiceCycle)) % len(trustChoiceCycle)
	m.selected = trustChoiceCycle[idx]
}

func (m *trustDialogModel) adjustScroll(delta int) {
	m.scroll += delta
	if m.scroll < 0 {
		m.scroll = 0
	}
	maxScroll := len(m.insp.Changes) - m.changeListHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
}

// changeListHeight is the number of change lines visible at once, reserving
// room for the header lines and the always-visible button row.
func (m *trustDialogModel) changeListHeight() int {
	h := m.height - m.chromeLines()
	if h < 1 {
		h = 1
	}
	return h
}

// chromeLines is the number of non-change lines the changes view renders:
// title, blank, header, blank, permanence line, blank, buttons, plus a
// scroll-indicator line (always reserved so scrolling can't change the
// chrome count) and, when any change is security-relevant, the blank and
// note line calling that out.
func (m *trustDialogModel) chromeLines() int {
	const base = 7
	chrome := base
	if len(m.insp.Changes) > 0 {
		chrome++
	}
	if m.hasSecurityChange() {
		chrome += 2
	}
	return chrome
}

// noticeDialogModel is the bubbletea model backing RunNoticeDialog.
type noticeDialogModel struct {
	title   string
	message string
	styles  theme.Styles
	done    bool
	width   int
	height  int
}

func newNoticeDialogModel(title, message string) *noticeDialogModel {
	return &noticeDialogModel{
		title:   title,
		message: message,
		styles:  dialogStyles(),
		width:   80,
		height:  24,
	}
}

// Init implements tea.Model.
func (m *noticeDialogModel) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m *noticeDialogModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyPressMsg:
		if isCtrl(msg, 'c') {
			m.done = true
			return m, tea.Quit
		}
		if msg.Text == "q" {
			m.done = true
			return m, tea.Quit
		}
		switch msg.Code {
		case tea.KeyEnter, tea.KeyEsc:
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// RunTrustDialog shows the trust prompt for insp and returns the choice. Any
// program error returns TrustDeny with the error. The program ending without
// the user reaching a decision (m.done unset, e.g. on EOF or a cancelled
// context) also returns TrustDeny: a lingering cursor position must never be
// read as consent.
func RunTrustDialog(ctx context.Context, dio DialogIO, insp config.ProjectInspection) (TrustChoice, error) {
	m := newTrustDialogModel(insp)
	p := tea.NewProgram(m, tea.WithInput(dio.In), tea.WithOutput(dio.Out), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		return TrustDeny, err
	}
	return trustDialogResult(m), nil
}

// trustDialogResult reports the user's trust decision, or TrustDeny when the
// program ended before m.done was set (no explicit decision was made).
func trustDialogResult(m *trustDialogModel) TrustChoice {
	if !m.done {
		return TrustDeny
	}
	return m.selected
}

// RunNoticeDialog shows message until the user dismisses it.
func RunNoticeDialog(ctx context.Context, dio DialogIO, title, message string) error {
	m := newNoticeDialogModel(title, message)
	p := tea.NewProgram(m, tea.WithInput(dio.In), tea.WithOutput(dio.Out), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}
