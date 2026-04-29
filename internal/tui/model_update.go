package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/tui/prefs"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case paletteClearMsg:
		return m.handlePaletteClearMsg(msg)
	case paletteToggleThinkingMsg:
		return m.handlePaletteToggleThinkingMsg(msg)
	case paletteSwitchModelMsg:
		return m.handlePaletteSwitchModelMsg(msg)
	case paletteSetAccentMsg:
		return m.handlePaletteSetAccentMsg(msg)
	case tickMsg:
		return m.handleTickMsg(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg)
	case runtimeEventMsg:
		return m.handleRuntimeEventMsg(msg)
	case bridgeClosedMsg:
		return m.handleBridgeClosedMsg(msg)
	case tea.MouseMsg:
		return m.handleMouseMsg(msg)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handlePaletteClearMsg(msg paletteClearMsg) (tea.Model, tea.Cmd) {
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

func (m Model) handlePaletteToggleThinkingMsg(msg paletteToggleThinkingMsg) (tea.Model, tea.Cmd) {
	m.showThinking = !m.showThinking
	m.content.showThinking = m.showThinking
	if err := prefs.Save(prefs.Prefs{Accent: m.accentPreset, ShowThinking: m.showThinking}); err != nil {
		fmt.Fprintf(os.Stderr, "prefs save: %v\n", err)
	}
	m.syncViewport()
	return m, nil
}

func (m Model) handlePaletteSwitchModelMsg(msg paletteSwitchModelMsg) (tea.Model, tea.Cmd) {
	providerBaseURL := m.sidebar.provider
	if m.onModelSwitch != nil {
		var ok bool
		providerBaseURL, ok = m.onModelSwitch(msg.name)
		if !ok {
			m.content.AppendLine(fmt.Sprintf("status: model %s is not configured", msg.name))
			m.syncViewport()
			return m, nil
		}
	}
	m.applyModelSelection(msg.name, providerBaseURL)
	m.content.AppendLine(fmt.Sprintf("status: model switched to %s", msg.name))
	m.syncViewport()
	return m, nil
}

func (m Model) handlePaletteSetAccentMsg(msg paletteSetAccentMsg) (tea.Model, tea.Cmd) {
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
}

func (m Model) handleTickMsg(msg tickMsg) (tea.Model, tea.Cmd) {
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
	// Emit any render or git errors captured during the last cycle
	if m.content.lastRenderErr != nil {
		emitRenderError(m.content.lastRenderErr)
		m.content.lastRenderErr = nil
	}
	m.syncViewport()
	return m, tickCmd()
}

func (m Model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.palette.width = msg.Width
	m.palette.height = msg.Height
	m.fileList.width = msg.Width
	m.fileList.height = msg.Height
	m.contextOverlay.OverlayShell = m.contextOverlay.OverlayShell.WithDimensions(msg.Width, msg.Height)
	m.fileViewer.OverlayShell = m.fileViewer.OverlayShell.WithDimensions(msg.Width, msg.Height)
	m.layout()
	return m, nil
}

func (m Model) handleRuntimeEventMsg(msg runtimeEventMsg) (tea.Model, tea.Cmd) {
	m.applyEvent(msg.Event)
	if m.external != nil {
		return m, waitForExternalMsg(m.external)
	}
	return m, nil
}

func (m Model) handleBridgeClosedMsg(msg bridgeClosedMsg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (m Model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.handleMouse(msg)
	return m, nil
}

func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If palette is open, route all keys to it first
	if m.palette.open {
		var cmd tea.Cmd
		m.palette, cmd = m.palette.Update(msg)
		return m, cmd
	}

	// If file list is open, route all keys to it
	if m.fileList.open {
		var cmd tea.Cmd
		m.fileList, cmd = m.fileList.Update(msg)
		return m, cmd
	}

	// If context overlay is open, handle scroll and close.
	if m.contextOverlay.open {
		return m.handleContextOverlayKey(msg)
	}

	// If file viewer is open, handle scroll and close.
	if m.fileViewer.open {
		return m.handleFileViewerKey(msg)
	}

	// If file picker is open, route keys to it
	if m.filePicker.open {
		return m.handleFilePickerKey(msg)
	}

	// Reset completion state on any non-Tab key
	if msg.Type != tea.KeyTab {
		m.completionCandidates = nil
		m.completionIdx = 0
	}

	activeConversation := m.content.streamingPhase != "" || m.approval.active || m.status.mode == "running" || m.status.mode == "approval"

	// Interrupt the active conversation before any other key routing.
	if activeConversation && (msg.Type == tea.KeyEsc || msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyCtrlD) {
		return m.executeInterruptAction()
	}

	// While a run is active, only approval interaction may pass through.
	if activeConversation && !m.approval.active {
		return m, nil
	}

	// Handle ? for help toggle (only when textarea is empty and no run is active)
	if !activeConversation && msg.String() == "?" && strings.TrimSpace(m.input.Value()) == "" {
		m.helpVisible = !m.helpVisible
		return m, nil
	}

	// Handle Escape to close help
	if msg.Type == tea.KeyEsc && m.helpVisible {
		m.helpVisible = false
		return m, nil
	}

	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyCtrlD:
		if m.onExitRequested != nil {
			m.onExitRequested()
			return m, nil
		}
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
		return m.handleTabKey(msg)
	case tea.KeyUp:
		return m.handleKeyUp(msg)
	case tea.KeyDown:
		return m.handleKeyDown(msg)
	case tea.KeyPgUp:
		m.scrollUp(max(1, m.viewport.Height))
		return m, nil
	case tea.KeyPgDown:
		m.scrollDown(max(1, m.viewport.Height))
		return m, nil
	case tea.KeyEnter:
		if !m.approval.active && key.Matches(msg, m.input.KeyMap.InsertNewline) {
			break
		}
		return m.handleEnter()
	}

	// @ triggers the file picker
	if msg.Type == tea.KeyRunes {
		for _, r := range msg.Runes {
			if r == '@' {
				root := m.sidebar.workingDir
				if root == "" {
					root = "."
				}
				m.filePicker = m.filePicker.Open(root)
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleContextOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.contextOverlay = m.contextOverlay.closeContextOverlay()
		return m, nil
	case tea.KeyUp:
		m.contextOverlay = m.contextOverlay.scrollUp(1)
		return m, nil
	case tea.KeyDown:
		m.contextOverlay = m.contextOverlay.scrollDown(1)
		return m, nil
	case tea.KeyPgUp:
		m.contextOverlay = m.contextOverlay.scrollUp(contextOverlayMaxLines)
		return m, nil
	case tea.KeyPgDown:
		m.contextOverlay = m.contextOverlay.scrollDown(contextOverlayMaxLines)
		return m, nil
	}
	return m, nil
}

func (m Model) handleFileViewerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.fileViewer = m.fileViewer.closeFileViewer()
		return m, nil
	case tea.KeyUp:
		m.fileViewer = m.fileViewer.scrollUp(1)
		return m, nil
	case tea.KeyDown:
		m.fileViewer = m.fileViewer.scrollDown(1)
		return m, nil
	case tea.KeyPgUp:
		m.fileViewer = m.fileViewer.scrollUp(fileViewerMaxLines)
		return m, nil
	case tea.KeyPgDown:
		m.fileViewer = m.fileViewer.scrollDown(fileViewerMaxLines)
		return m, nil
	}
	return m, nil
}

func (m Model) handleFilePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.filePicker = m.filePicker.Close()
		return m, nil
	case tea.KeyEnter:
		if m.filePicker.selection >= 0 && len(m.filePicker.candidates) > 0 {
			selected := m.filePicker.candidates[m.filePicker.selection]
			m.filePicker = m.filePicker.Close()
			m.input.InsertString(selected + " ")
		}
		return m, nil
	default:
		var cmd tea.Cmd
		m.filePicker, cmd = m.filePicker.Update(msg)
		return m, cmd
	}
}

func (m Model) handleTabKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	current := m.input.Value()
	if !strings.HasPrefix(current, "/") {
		// no "/" prefix — pass Tab through to textarea (does nothing meaningful)
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
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
}

func (m Model) handleKeyUp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
}

func (m Model) handleKeyDown(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
}
