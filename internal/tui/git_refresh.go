package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

const gitRefreshTimeout = 10 * time.Second

type gitRefreshDoneMsg struct{}

// gitRefreshCmd returns a command that refreshes gs, or nil when a refresh is
// already in flight; that refresh then runs once more before finishing.
func gitRefreshCmd(gs *gitState) tea.Cmd {
	if gs == nil || !gs.requestRefresh() {
		return nil
	}
	return func() tea.Msg {
		gs.refreshUntilIdle()
		return gitRefreshDoneMsg{}
	}
}

func (s *gitState) requestRefresh() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refreshing {
		s.pending = true
		return false
	}
	s.refreshing = true
	return true
}

func (s *gitState) refreshUntilIdle() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), gitRefreshTimeout)
		s.Refresh(ctx)
		cancel()

		s.mu.Lock()
		if s.pending {
			s.pending = false
			s.mu.Unlock()
			continue
		}
		s.refreshing = false
		s.mu.Unlock()
		return
	}
}

// shouldRefreshGit reports whether event can have changed the working tree.
func shouldRefreshGit(event output.Event) bool {
	switch event.Type {
	case output.EventTypeDelegationComplete,
		output.EventTypeDelegationFailed,
		output.EventTypeDelegationWorktreeDisposal,
		output.EventTypeRunFinished:
		return true
	case output.EventTypeToolCallFinished:
		if event.Scope.AgentID != "" {
			return false
		}
		payload, ok := event.Payload.(output.ToolCallFinishedEvent)
		return !ok || !isReadOnlyTool(payload.Tool)
	}
	return false
}

func isReadOnlyTool(name string) bool {
	switch name {
	case "read", "glob", "grep", "ls", "fetch_url", "display_file", "advisor", "workflow_handoff":
		return true
	}
	return strings.HasPrefix(name, "lsp_")
}
