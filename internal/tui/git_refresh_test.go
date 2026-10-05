package tui

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestShouldRefreshGit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		event output.Event
		want  bool
	}{
		{"model call finished", output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{Turn: 1}), false},
		{"bash", output.NewToolCallFinishedEvent(1, "bash", "c", "", nil), true},
		{"mutate", output.NewToolCallFinishedEvent(1, "mutate", "c", "", nil), true},
		{"mcp tool", output.NewToolCallFinishedEvent(1, "mcp__x__y", "c", "", nil), true},
		{"unknown tool", output.NewToolCallFinishedEvent(1, "whatever", "c", "", nil), true},
		{"read", output.NewToolCallFinishedEvent(1, "read", "c", "", nil), false},
		{"glob", output.NewToolCallFinishedEvent(1, "glob", "c", "", nil), false},
		{"grep", output.NewToolCallFinishedEvent(1, "grep", "c", "", nil), false},
		{"ls", output.NewToolCallFinishedEvent(1, "ls", "c", "", nil), false},
		{"fetch_url", output.NewToolCallFinishedEvent(1, "fetch_url", "c", "", nil), false},
		{"display_file", output.NewToolCallFinishedEvent(1, "display_file", "c", "", nil), false},
		{"advisor", output.NewToolCallFinishedEvent(1, "advisor", "c", "", nil), false},
		{"workflow_handoff", output.NewToolCallFinishedEvent(1, "workflow_handoff", "c", "", nil), false},
		{"lsp tool", output.NewToolCallFinishedEvent(1, "lsp_hover", "c", "", nil), false},
		{"sub-agent bash", output.WithAgentScope(output.NewToolCallFinishedEvent(1, "bash", "c", "", nil), "a1"), false},
		{"delegation complete", output.NewDelegationCompleteEvent(output.DelegationCompleteParams{}), true},
		{"delegation complete sub-agent scope", output.WithAgentScope(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{}), "a1"), true},
		{"delegation failed", output.WithAgentScope(output.NewDelegationFailedEvent(output.DelegationFailedParams{}), "a1"), true},
		{"worktree disposal", output.WithAgentScope(output.NewDelegationWorktreeDisposalEvent("a1", true, ""), "a1"), true},
		{"run finished", output.NewRunFinishedEvent(1, "stop", "", "", nil), true},
		{"run started", output.NewRunStartedEvent("interactive", "m", "", 1, 1), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldRefreshGit(tc.event); got != tc.want {
				t.Fatalf("shouldRefreshGit = %v, want %v", got, tc.want)
			}
		})
	}
}

func gitInFlight(gs *gitState) bool {
	gs.mu.RLock()
	defer gs.mu.RUnlock()
	return gs.refreshing
}

func TestModelGitRefreshTriggers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		event output.Event
		want  bool
	}{
		{"main bash", output.NewToolCallFinishedEvent(1, "bash", "c", "", nil), true},
		{"main read", output.NewToolCallFinishedEvent(1, "read", "c", "", nil), false},
		{"model call finished", output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{Turn: 1}), false},
		{"sub-agent bash", output.WithAgentScope(output.NewToolCallFinishedEvent(1, "bash", "c", "", nil), "a1"), false},
		{"delegation complete", output.NewDelegationCompleteEvent(output.DelegationCompleteParams{}), true},
		{"scoped delegation complete", output.WithAgentScope(output.NewDelegationCompleteEvent(output.DelegationCompleteParams{}), "a1"), true},
		{"scoped worktree disposal", output.WithAgentScope(output.NewDelegationWorktreeDisposalEvent("a1", true, ""), "a1"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{WorkingDir: t.TempDir()}, nil)
			m = updateModel(t, m, runtimeEventMsg{Event: tc.event})
			if got := gitInFlight(m.git); got != tc.want {
				t.Fatalf("refresh in flight = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGitRefreshCmdCoalescesBurst(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	gs := newGitState(t.TempDir())
	gs.detect = func(ctx context.Context, _ string, _ func(error)) gitSnapshot {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("detect ctx has no deadline")
		}
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return gitSnapshot{}
	}

	cmd := gitRefreshCmd(gs)
	if cmd == nil {
		t.Fatal("first gitRefreshCmd = nil, want cmd")
	}
	done := make(chan any, 1)
	go func() { done <- cmd() }()
	<-started

	for i := 0; i < 10; i++ {
		if extra := gitRefreshCmd(gs); extra != nil {
			t.Fatalf("gitRefreshCmd #%d while in flight = non-nil, want nil", i)
		}
	}
	close(release)
	if msg := <-done; msg != (gitRefreshDoneMsg{}) {
		t.Fatalf("msg = %#v, want gitRefreshDoneMsg", msg)
	}
	if got, want := calls.Load(), int32(2); got != want {
		t.Fatalf("detections = %d, want %d", got, want)
	}
	if gitInFlight(gs) {
		t.Fatal("still in flight after cmd finished")
	}
	if gitRefreshCmd(gs) == nil {
		t.Fatal("gitRefreshCmd after idle = nil, want cmd")
	}
}

func TestGitRefreshCmdNilState(t *testing.T) {
	t.Parallel()
	if gitRefreshCmd(nil) != nil {
		t.Fatal("gitRefreshCmd(nil) != nil")
	}
}

func TestSyncSidebarClearsStaleSnapshot(t *testing.T) {
	t.Parallel()
	m := newModel(Config{WorkingDir: t.TempDir()}, nil)
	ready := true
	m.git.detect = func(context.Context, string, func(error)) gitSnapshot {
		if !ready {
			return gitSnapshot{}
		}
		return gitSnapshot{branch: "main", dirty: true, ahead: 2, ready: true,
			modifiedFiles: []gitModifiedFile{{Status: "M", Path: "a.go"}}}
	}
	m.git.Refresh(context.Background())
	m.syncSidebar()
	if m.sidebar.branch != "main" || !m.sidebar.dirty || m.sidebar.ahead != 2 || len(m.sidebar.modifiedFiles) != 1 {
		t.Fatalf("sidebar not populated: %+v", m.sidebar)
	}

	ready = false
	m.git.Refresh(context.Background())
	m.syncSidebar()
	if m.sidebar.branch != "" || m.sidebar.dirty || m.sidebar.ahead != 0 || m.sidebar.modifiedFiles != nil {
		t.Fatalf("sidebar kept stale data: branch=%q dirty=%v ahead=%d files=%v",
			m.sidebar.branch, m.sidebar.dirty, m.sidebar.ahead, m.sidebar.modifiedFiles)
	}
}

func TestGitErrorSurfacedOncePerFailureRun(t *testing.T) {
	t.Parallel()
	gs := newGitState(t.TempDir())
	var next error
	gs.detect = func(_ context.Context, _ string, logError func(error)) gitSnapshot {
		if next != nil {
			logError(next)
		}
		return gitSnapshot{}
	}
	errA, errB := errors.New("boom a"), errors.New("boom b")

	steps := []struct {
		name string
		err  error
		want error
	}{
		{"first failure surfaces", errA, errA},
		{"repeat suppressed", errA, nil},
		{"repeat suppressed again", errA, nil},
		{"clean refresh", nil, nil},
		{"recurrence surfaces", errA, errA},
		{"different message surfaces", errB, errB},
		{"different message repeat suppressed", errB, nil},
	}
	for _, st := range steps {
		next = st.err
		gs.Refresh(context.Background())
		if got := gs.takeError(); !errors.Is(got, st.want) {
			t.Fatalf("%s: takeError = %v, want %v", st.name, got, st.want)
		}
	}
}

func TestModelConstructionDoesNotRunGitUntilInit(t *testing.T) {
	t.Parallel()
	m := newModel(Config{WorkingDir: t.TempDir()}, nil)
	if gitInFlight(m.git) {
		t.Fatal("refresh in flight after construction")
	}
	var calls atomic.Int32
	m.git.detect = func(context.Context, string, func(error)) gitSnapshot {
		calls.Add(1)
		return gitSnapshot{}
	}
	if m.Init() == nil {
		t.Fatal("Init() = nil")
	}
	if !gitInFlight(m.git) {
		t.Fatal("Init did not request a git refresh")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("detections before Init cmd runs = %d, want 0", got)
	}
}
