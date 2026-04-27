package tui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/tui/prefs"
	"github.com/luispabon/steiner/internal/tui/theme"
)

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
		if err := prefs.Save(prefs.Prefs{Accent: m.accentPreset, ShowThinking: m.showThinking}); err != nil {
			fmt.Fprintf(os.Stderr, "prefs save: %v\n", err)
		}
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
		if err := prefs.Save(prefs.Prefs{Accent: m.accentPreset, ShowThinking: m.showThinking}); err != nil {
			fmt.Fprintf(os.Stderr, "prefs save: %v\n", err)
		}
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

		// Block all non-Esc input while streaming
		if m.content.streamingPhase != "" && msg.Type != tea.KeyEsc {
			return m, nil
		}

		// Handle Escape: interrupt streaming first (takes priority over help panel)
		if msg.Type == tea.KeyEsc && m.content.streamingPhase != "" {
			m.content.AppendInterrupted()
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
			if m.fileHistoryIdx >= 0 && m.fileHistoryIdx < len(m.fileHistory)-1 {
				m.fileHistoryIdx++
				m.input.SetValue(m.fileHistory[m.fileHistoryIdx])
				return m, nil
			}
			if len(m.fileHistory) > 0 && m.fileHistoryIdx < 0 {
				m.historyDraft = m.input.Value()
				m.fileHistoryIdx = 0
				m.input.SetValue(m.fileHistory[0])
				return m, nil
			}
			m.fileHistoryIdx = -1
			m.historyIdx = -1
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		case tea.KeyDown:
			if m.fileHistoryIdx > 0 {
				m.fileHistoryIdx--
				m.input.SetValue(m.fileHistory[m.fileHistoryIdx])
				return m, nil
			}
			if m.fileHistoryIdx == 0 {
				m.input.SetValue(m.historyDraft)
				m.fileHistoryIdx = -1
				return m, nil
			}
			m.fileHistoryIdx = -1
			m.historyIdx = -1
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
