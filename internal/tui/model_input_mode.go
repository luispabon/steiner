package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
)

// toggledMode returns the execution mode opposite the model's current mode,
// defaulting to plan when the current mode is unset or unrecognized.
func (m Model) toggledMode() string {
	if m.mode == string(config.ExecutionModePlan) {
		return string(config.ExecutionModeBuild)
	}
	return string(config.ExecutionModePlan)
}

// executeToggleModeAction switches between plan and build mode when Shift+Tab
// is pressed, without clearing the user's in-progress prompt text.
func (m Model) executeToggleModeAction() tea.Model {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchMode{Mode: config.ExecutionMode(m.toggledMode())}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.syncViewport()
	return m
}

// executeSetModeAction dispatches a SwitchMode action for the given mode
// ("plan" or "build") to the session controller.
func (m Model) executeSetModeAction(mode string) (tea.Model, tea.Cmd) {
	if m.controller != nil {
		if err := m.controller.Handle(context.Background(), interactive.SwitchMode{Mode: config.ExecutionMode(mode)}); err != nil {
			m.content.AppendLine(fmt.Sprintf("status: %v", err))
		}
	}
	m.input.Reset()
	m.historyIdx = 0
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}

// executeInvalidModeAction reports an unrecognized /mode argument.
func (m Model) executeInvalidModeAction(arg string) (tea.Model, tea.Cmd) {
	m.content.AppendLine(fmt.Sprintf("status: mode %q is not valid (use plan: restricted edits, plan artifacts only; or build: normal workspace editing)", arg))
	m.input.Reset()
	m.historyIdx = 0
	m.relayoutInput()
	m.syncViewport()
	return m, nil
}
