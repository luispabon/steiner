package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

// applySubAgentEvent applies an event scoped to a sub-agent. Sub-agent events
// update delegation transcript segments only. They must not overwrite the main
// agent's sidebar, status bar, or activity state.
func (m *Model) applySubAgentEvent(event output.Event) tea.Cmd {
	if event.Type == output.EventTypeDelegationWorktreeDisposal {
		if payload, ok := event.Payload.(output.DelegationWorktreeDisposalEvent); ok {
			// Unscoped legacy events name no agent type, so the wording stays
			// generic.
			worktreeLabel := "worktree"
			if event.Scope.AgentType != "" {
				worktreeLabel = event.Scope.AgentType + " worktree"
			}
			line := fmt.Sprintf("%s discarded for %s", worktreeLabel, payload.AgentID)
			switch {
			case payload.Error != "":
				line = fmt.Sprintf("%s discard failed for %s: %s", worktreeLabel, payload.AgentID, payload.Error)
			case !payload.Removed:
				line = fmt.Sprintf("no %s to discard for %s", worktreeLabel, payload.AgentID)
			}
			m.content.AppendLine("status: " + line)
			m.contentDirty = true
			m.syncDebounceSeq++
			return tea.Batch(gitRefreshCmd(m.git), syncDebounceCmd(m.syncDebounceSeq))
		}
	}
	if event.Type != output.EventTypeHistoryLoaded {
		m.content.AppendEvent(event)
	}
	var cmds []tea.Cmd
	if shouldRefreshGit(event) {
		cmds = append(cmds, gitRefreshCmd(m.git))
	}
	if event.Type != output.EventTypeAssistantChunk && event.Type != output.EventTypeThinkingChunk {
		m.contentDirty = true
		m.syncDebounceSeq++
		cmds = append(cmds, syncDebounceCmd(m.syncDebounceSeq))
	}
	return tea.Batch(cmds...)
}
