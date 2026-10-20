package tui

import (
	"context"
	"testing"
)

// TestUpdateSidebarHoverSetsRosterHover checks handleSidebarHoverMsg. The hover
// tracks the agent under the pointer, and an empty agent ID clears it. The test
// calls updateDispatch rather than Update: Update reconciles the hover against
// the roster, which is empty here, and would clear it before the assertion.
func TestUpdateSidebarHoverSetsRosterHover(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.updateDispatch(sidebarHoverMsg{agentID: "child-1"})
	if m.sidebar.rosterHover != "child-1" {
		t.Fatalf("rosterHover = %q, want child-1", m.sidebar.rosterHover)
	}
	m.updateDispatch(sidebarHoverMsg{agentID: ""})
	if m.sidebar.rosterHover != "" {
		t.Errorf("rosterHover = %q after clear, want empty", m.sidebar.rosterHover)
	}
}

// TestUpdateGitRefreshDoneSyncsSidebar checks handleGitRefreshDoneMsg. A refresh
// stores the snapshot in gitState, and only the done message copies it into the
// sidebar.
func TestUpdateGitRefreshDoneSyncsSidebar(t *testing.T) {
	t.Parallel()
	m := newModel(Config{WorkingDir: t.TempDir()}, nil)
	m.git.detect = func(context.Context, string, func(error)) gitSnapshot {
		return gitSnapshot{branch: "main", ready: true}
	}
	m.git.Refresh(context.Background())
	if m.sidebar.branch == "main" {
		t.Fatal("sidebar synced before gitRefreshDoneMsg")
	}
	m = updateModel(t, m, gitRefreshDoneMsg{})
	if m.sidebar.branch != "main" {
		t.Errorf("sidebar branch = %q, want main", m.sidebar.branch)
	}
}
