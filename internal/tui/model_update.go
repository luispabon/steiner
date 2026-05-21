package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/tui/prefs"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type syncDebounceFiredMsg struct{ seq int }

func syncDebounceCmd(seq int) tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(_ time.Time) tea.Msg {
		return syncDebounceFiredMsg{seq: seq}
	})
}

// Update implements tea.Model.
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
	case gitRefreshDoneMsg:
		m.syncSidebar()
		return m, nil
	case syncDebounceFiredMsg:
		if msg.seq == m.syncDebounceSeq && m.contentDirty {
			m.syncViewport()
			m.contentDirty = false
		}
		return m, nil
	case tea.MouseMsg:
		return m.handleMouseMsg(msg)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handlePaletteClearMsg(_ paletteClearMsg) (tea.Model, tea.Cmd) {
	m.content.Clear()
	m.sidebar.promptUsed = 0
	m.sidebar.budgetUsed = 0
	m.sidebar.scratchpadIntent = ""
	m.sidebar.scratchpadDecisions = ""
	m.sidebar.scratchpadOpen = ""
	m.sidebar.scratchpadNext = ""
	if m.sidebar.contextBudget > 0 {
		m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
	} else {
		m.status.context = ""
	}
	m.syncSidebar()
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.ClearConversation{}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.input.Reset()
	m.historyIdx = 0
	m.syncViewport()
	return m, nil
}

func (m Model) handlePaletteToggleThinkingMsg(_ paletteToggleThinkingMsg) (tea.Model, tea.Cmd) {
	m.showThinking = !m.showThinking
	m.content.showThinking = m.showThinking
	if err := prefs.Save(prefs.Prefs{Accent: m.accentPreset, ShowThinking: m.showThinking}); err != nil {
		fmt.Fprintf(os.Stderr, "prefs save: %v\n", err)
	}
	for i := range m.content.segments {
		if m.content.segments[i].kind == segmentThinkingBlock {
			m.content.segments[i].renderDirty = true
		}
	}
	m.syncViewport()
	return m, nil
}

func (m Model) handlePaletteSwitchModelMsg(msg paletteSwitchModelMsg) (tea.Model, tea.Cmd) {
	providerBaseURL := m.sidebar.provider
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchModel{Name: msg.name}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: model %s is not configured", msg.name))
			m.syncViewport()
			return m, nil
		}
	}
	if baseURL, ok := m.modelBaseURLs[msg.name]; ok {
		providerBaseURL = baseURL
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
	m.content.setGlamourStyleSheet(accentHex)
	m.sidebar.styles = m.styles
	m.status.styles = m.styles
	m.activity = m.activity.withStyles(m.styles)
	m.applyInputStyles()
	m.palette.styles = m.styles
	m.slashOverlay.styles = m.styles
	m.fileList.styles = m.styles
	m.filePicker.styles = m.styles
	m.sessionPicker.styles = m.styles
	if err := prefs.Save(prefs.Prefs{Accent: m.accentPreset, ShowThinking: m.showThinking}); err != nil {
		fmt.Fprintf(os.Stderr, "prefs save: %v\n", err)
	}
	for i := range m.content.segments {
		m.content.segments[i].renderDirty = true
	}
	m.syncViewport()
	return m, nil
}

func (m Model) handleTickMsg(_ tickMsg) (tea.Model, tea.Cmd) {
	m.content.tickCount++
	m.sidebar.tickCount = m.content.tickCount
	m.activity = m.activity.advance()
	m.status.promptUsed = m.sidebar.promptUsed
	m.status.contextBudget = m.sidebar.contextBudget
	m.syncInputChrome()
	if m.git != nil {
		_ = m.git.takeError()
	}
	// Clear any render error captured during the last cycle.
	if m.content.lastRenderErr != nil {
		m.content.lastRenderErr = nil
	}
	// Advance delegation spinners when delegations are in flight.
	if m.content.HasActiveDelegations() {
		m.content.AdvanceDelegationSpinners()
	}
	if m.contentDirty || m.content.streaming || m.compacting || m.content.HasActiveDelegations() {
		m.syncViewport()
		m.contentDirty = false
	}
	return m, tickCmd()
}

func (m Model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.palette.width = msg.Width
	m.palette.height = msg.Height
	m.fileList.width = msg.Width
	m.fileList.height = msg.Height
	m.sessionPicker = m.sessionPicker.withDimensions(msg.Width, msg.Height)
	if m.contextOverlay.open {
		m.contextOverlay.OverlayShell = m.contextOverlay.WithDimensions(msg.Width, msg.Height)
		m.contextOverlay = m.contextOverlay.reflow()
	}
	if m.scratchpadOverlay.IsOpen() {
		m.scratchpadOverlay.OverlayShell = m.scratchpadOverlay.WithDimensions(msg.Width, msg.Height)
		m.scratchpadOverlay = m.scratchpadOverlay.reflow()
	}
	m.exitModal.OverlayShell = m.exitModal.WithDimensions(msg.Width, msg.Height)

	// Use content area width for bottom-anchored overlays so they don't
	// overflow into the sidebar area.
	contentW := msg.Width
	if m.sidebar.Visible(msg.Width) {
		contentW = msg.Width - sidebarWidth - 1
	}
	m.filePicker.OverlayShell = m.filePicker.WithDimensions(contentW, msg.Height)
	m.slashOverlay.OverlayShell = m.slashOverlay.WithDimensions(contentW, msg.Height)
	m.layout()
	return m, nil
}

func (m Model) handleRuntimeEventMsg(msg runtimeEventMsg) (tea.Model, tea.Cmd) {
	eventCmd := m.applyEvent(msg.Event)
	var cmds []tea.Cmd
	if eventCmd != nil {
		cmds = append(cmds, eventCmd)
	}
	if m.external != nil {
		cmds = append(cmds, waitForExternalMsg(m.external))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleBridgeClosedMsg(_ bridgeClosedMsg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (m Model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	m.handleMouse(msg)
	return m, nil
}
