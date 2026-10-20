package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/quota"
)

// TestApplyEventSharedTail checks which events skip finishAppliedEvent. The
// early returns must leave syncDebounceSeq alone and return no command. An
// ordinary event must advance the debounce by one and return a command.
func TestApplyEventSharedTail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		event        output.Event
		wantSeqDelta int
		wantCmd      bool
	}{
		{name: "history loaded returns early", event: output.NewHistoryLoadedEvent([]string{"first"})},
		{name: "workflow handoff request returns early", event: output.NewWorkflowHandoffRequestedEvent("build", "build", "", "")},
		{name: "phase transition returns early", event: output.NewPhaseTransitionEvent("run-1", "plan", "build", "completed", "", "")},
		{name: "ordinary event runs shared tail", event: output.NewSandboxStatusEvent("active", ""), wantSeqDelta: 1, wantCmd: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			before := m.syncDebounceSeq
			cmd := m.applyEvent(tc.event)
			if got := m.syncDebounceSeq - before; got != tc.wantSeqDelta {
				t.Errorf("syncDebounceSeq delta = %d, want %d", got, tc.wantSeqDelta)
			}
			if (cmd != nil) != tc.wantCmd {
				t.Errorf("cmd non-nil = %t, want %t", cmd != nil, tc.wantCmd)
			}
		})
	}
}

// TestApplyEventSubAgentPath covers applySubAgentEvent. Worktree disposal
// returns its own batch. Other sub-agent events share the debounce tail, except
// assistant chunks, which skip it. No sub-agent event changes the main sidebar
// sandbox status.
func TestApplyEventSubAgentPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		event        output.Event
		wantSeqDelta int
		wantCmd      bool
		wantDirty    bool
	}{
		{
			name:         "worktree disposal returns its own batch",
			event:        output.WithAgentScope(output.NewDelegationWorktreeDisposalEvent("child-1", true, ""), "child-1"),
			wantSeqDelta: 1,
			wantCmd:      true,
			wantDirty:    true,
		},
		{
			name:         "sub-agent sandbox status shares debounce tail",
			event:        output.WithAgentScope(output.NewSandboxStatusEvent("active", ""), "child-1"),
			wantSeqDelta: 1,
			wantCmd:      true,
			wantDirty:    true,
		},
		{
			name:  "sub-agent assistant chunk skips debounce",
			event: output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "hi", output.ChunkSourceAssistant), "child-1"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			before := m.syncDebounceSeq
			cmd := m.applyEvent(tc.event)
			if got := m.syncDebounceSeq - before; got != tc.wantSeqDelta {
				t.Errorf("syncDebounceSeq delta = %d, want %d", got, tc.wantSeqDelta)
			}
			if (cmd != nil) != tc.wantCmd {
				t.Errorf("cmd non-nil = %t, want %t", cmd != nil, tc.wantCmd)
			}
			if m.contentDirty != tc.wantDirty {
				t.Errorf("contentDirty = %t, want %t", m.contentDirty, tc.wantDirty)
			}
			if m.sidebar.sandboxStatus != "" {
				t.Errorf("sub-agent event changed main sidebar sandbox status to %q", m.sidebar.sandboxStatus)
			}
		})
	}
}

// TestApplyEventRunFinishedBatchesUsageRefresh checks that RunFinished adds the
// usage refresh to its batch when a quota service is configured. The git state
// is nil so the batch carries no git refresh. Without a quota service the
// command is the bare debounce tick, which is not a BatchMsg.
func TestApplyEventRunFinishedBatchesUsageRefresh(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		quota bool
	}{
		{name: "usage refresh with quota service", quota: true},
		{name: "debounce only without quota service", quota: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m.git = nil
			if tc.quota {
				m.usageOverlay.quota = quota.NewService(quota.ServiceOptions{})
				m.usageOverlay.refreshInterval = time.Minute
			}
			cmd := m.applyEvent(output.NewRunFinishedEvent(1, "completed", "", "", nil))
			if cmd == nil {
				t.Fatal("RunFinished returned nil cmd")
			}
			batch, isBatch := cmd().(tea.BatchMsg)
			if isBatch != tc.quota {
				t.Fatalf("cmd is batch = %t, want %t", isBatch, tc.quota)
			}
			if tc.quota && len(batch) != 2 {
				t.Errorf("batch size = %d, want 2 (usage refresh and debounce tick)", len(batch))
			}
		})
	}
}
