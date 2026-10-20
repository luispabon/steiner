package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
)

// applySteerReceivedEvent appends a steer message the run accepted mid-flight.
func (m *Model) applySteerReceivedEvent(payload output.SteerReceivedEvent) {
	m.content.AppendUser(payload.Text)
	if len(payload.Images) > 0 {
		m.content.AppendImagesAttached(agentImageBlocks(payload.Images), m.sidebar.workingDir, m.sidebar.homeDir)
	}
	m.syncInputChrome()
	m.relayoutInput()
}

// applyModeChangedEvent updates the execution mode shown in the sidebar,
// status bar, and transcript.
func (m *Model) applyModeChangedEvent(payload output.ModeChangedEvent) {
	m.mode = strings.TrimSpace(payload.Mode)
	m.sidebar.execMode = m.mode
	m.status.execMode = m.mode
	m.content.AppendLine(fmt.Sprintf("status: mode → %s", m.mode))
}

// applyOrchestrationLevelChangedEvent updates the orchestration level and
// records the change in the transcript.
func (m *Model) applyOrchestrationLevelChangedEvent(payload output.OrchestrationLevelChangedEvent) {
	level := strings.TrimSpace(payload.Level)
	m.orchestrationLevel = config.OrchestrationLevel(level)
	if m.subAgentsEnabled {
		m.sidebar.orchestrationLevel = level
	}
	m.content.AppendLine(fmt.Sprintf("status: orchestration → %s", level))
}

// applySandboxStatusEvent updates the sandbox status shown in the sidebar and
// status bar, and appends its message when one is present.
func (m *Model) applySandboxStatusEvent(payload output.SandboxStatusEvent) {
	m.sidebar.sandboxStatus = strings.TrimSpace(payload.Status)
	m.status.sandboxStatus = strings.TrimSpace(payload.Status)
	if payload.Message != "" {
		m.content.AppendLine(fmt.Sprintf("status: %s", payload.Message))
	}
}
