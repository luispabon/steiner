package main

import (
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tui"
)

type orderLog struct {
	mu    sync.Mutex
	steps []string
}

func (o *orderLog) add(step string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.steps = append(o.steps, step)
}

func (o *orderLog) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.steps)
}

func stubTeaProgram(t *testing.T) {
	t.Helper()
	oldRun, oldQuit := runTeaProgram, quitTeaProgram
	t.Cleanup(func() { runTeaProgram, quitTeaProgram = oldRun, oldQuit })
	runTeaProgram = func(*tea.Program) (tea.Model, error) { return nil, tea.ErrProgramKilled }
	quitTeaProgram = func(*tea.Program) {}
}

func startShutdownChild(t *testing.T, sup *delegation.Supervisor, id, worktree string, execute func(context.Context) (tool.ExecutionResult, error)) {
	t.Helper()
	started := make(chan struct{})
	go func() {
		_, _ = sup.SpawnAndWait(context.Background(), delegation.ChildJob{
			AgentID:   id,
			AgentType: delegation.AgentTypeCode,
			Worktree:  delegation.CodeWorktree{Path: worktree},
			Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
				close(started)
				return execute(ctx)
			},
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not start")
	}
}

func TestRunInteractiveSessionShutsDownSupervisorBeforePruneAndClose(t *testing.T) {
	stubTeaProgram(t)
	var order orderLog
	sup := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: 1})
	startShutdownChild(t, sup, "child-1", "/wt/child-1", func(ctx context.Context) (tool.ExecutionResult, error) {
		<-ctx.Done()
		order.add("child-stopped")
		return tool.ExecutionResult{}, nil
	})
	plan := tui.NewWorktreeCleanupPlan(nil, func(context.Context) (int, error) {
		order.add("prune")
		return 1, nil
	})
	plan.Request()
	sess, err := interactive.NewSession(interactive.Dependencies{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	rt := &cliRuntime{
		events:               output.NoopSink{},
		delegationSupervisor: sup,
		worktreeCleanup:      plan,
		closeFn:              func() error { order.add("close-runtime"); return nil },
	}

	if err := runInteractiveSession(cmd, sess, nil, rt); err != nil {
		t.Fatalf("runInteractiveSession error = %v", err)
	}
	want := []string{"child-stopped", "prune", "close-runtime"}
	if got := order.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("shutdown order = %v, want %v", got, want)
	}
	if !sup.Closed() {
		t.Error("supervisor was not shut down")
	}
}

func TestPruneWorktreesOnExitReportsProtectedWorktrees(t *testing.T) {
	sup := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: 1, JoinTimeout: 20 * time.Millisecond})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	startShutdownChild(t, sup, "stuck", "/wt/stuck", func(context.Context) (tool.ExecutionResult, error) {
		<-release
		return tool.ExecutionResult{}, nil
	})
	var warnings []output.Event
	rt := &cliRuntime{
		events:               output.SinkFunc(func(e output.Event) { warnings = append(warnings, e) }),
		delegationSupervisor: sup,
	}
	plan := tui.NewWorktreeCleanupPlan(nil, func(context.Context) (int, error) { return 0, nil })
	plan.Request()
	rt.worktreeCleanup = plan

	shutdownDelegation(context.Background(), rt, delegation.CancelCauseUser)
	if got := protectedWorktrees(sup); !slices.Equal(got, []string{"/wt/stuck"}) {
		t.Fatalf("protectedWorktrees() = %v, want [/wt/stuck]", got)
	}
	if len(warnings) != 1 {
		t.Fatalf("shutdown emitted %d warnings, want 1", len(warnings))
	}

	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&stderr)
	pruneWorktreesOnExit(cmd, nil, rt)
	if want := "Kept /wt/stuck: sub-agent did not stop in time."; !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), want)
	}

	shutdownDelegation(context.Background(), rt, delegation.CancelCauseSystem)
	if len(warnings) != 1 {
		t.Fatalf("second shutdown emitted more warnings: %d, want 1", len(warnings))
	}
}

func TestProtectedWorktreesNilSupervisor(t *testing.T) {
	if got := protectedWorktrees(nil); got != nil {
		t.Fatalf("protectedWorktrees(nil) = %v, want nil", got)
	}
}

func TestCloseRuntimeShutsDownSupervisorBeforeClosingResources(t *testing.T) {
	var order orderLog
	sup := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: 1})
	startShutdownChild(t, sup, "child-1", "", func(ctx context.Context) (tool.ExecutionResult, error) {
		<-ctx.Done()
		order.add("child-stopped")
		return tool.ExecutionResult{}, nil
	})
	rt := cliRuntime{
		events:               output.NoopSink{},
		delegationSupervisor: sup,
		closeFn:              func() error { order.add("close-runtime"); return nil },
	}

	closeRuntime(&rt)

	if got, want := order.snapshot(), []string{"child-stopped", "close-runtime"}; !slices.Equal(got, want) {
		t.Fatalf("close order = %v, want %v", got, want)
	}
}
