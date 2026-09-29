package main

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui"
)

type recordingCanceller struct {
	mu    sync.Mutex
	calls []string
}

func (c *recordingCanceller) CancelAgent(id string, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, "agent:"+id)
	return nil
}

func (c *recordingCanceller) CancelAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, "all")
	return nil
}

func TestPhaseControlRoutesToDriverAndSupervisor(t *testing.T) {
	h := &phaseHarness{bg: &phaseBackground{}}
	canceller := &recordingCanceller{}
	var ctl oneshot.PhaseControl
	registered := make(chan struct{})
	released := make(chan struct{})
	prompts := make(chan string, 4)
	var runs atomic.Int32
	host := h.host(func(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		prompts <- in.Conversation[len(in.Conversation)-1].Content
		if runs.Add(1) == 1 {
			<-ctx.Done()
		}
		return agent.DriverRunOutput{Conversation: assistantReply("done", in.Conversation)}, nil
	})
	host.canceller = canceller
	in := h.input()
	in.RegisterControl = func(pc oneshot.PhaseControl) func() {
		ctl = pc
		close(registered)
		return func() { close(released) }
	}

	done := make(chan error, 1)
	go func() {
		_, err := runPhaseOnDriver(context.Background(), in, host)
		done <- err
	}()
	<-registered
	if got := <-prompts; got != "do the phase" {
		t.Fatalf("first prompt = %q", got)
	}
	if err := ctl.CancelAgent("a1", true); err != nil {
		t.Fatalf("CancelAgent: %v", err)
	}
	if err := ctl.CancelAll(); err != nil {
		t.Fatalf("CancelAll: %v", err)
	}
	if want := []string{"agent:a1", "all"}; !slices.Equal(canceller.calls, want) {
		t.Fatalf("canceller calls = %v, want %v", canceller.calls, want)
	}

	ctl.StopTurn()
	ctl.Submit("next", nil)
	select {
	case got := <-prompts:
		if got != "next" {
			t.Fatalf("second prompt = %q, want the submitted text", got)
		}
	case <-time.After(phaseDriverTestTimeout):
		t.Fatal("submitted prompt never reached the phase driver")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("phase error = %v", err)
		}
	case <-time.After(phaseDriverTestTimeout):
		t.Fatal("phase did not finish")
	}
	select {
	case <-released:
	default:
		t.Fatal("control was not released when the phase ended")
	}
	if slices.Index(h.snapshotOrder(), "save") < 0 {
		t.Fatalf("order = %v, want a final save before release", h.snapshotOrder())
	}
}

func TestPhaseControlWithoutSupervisorRefusesCancellation(t *testing.T) {
	ctl := phaseControl{}
	if err := ctl.CancelAll(); err == nil {
		t.Fatal("CancelAll error = nil")
	}
	if err := ctl.CancelAgent("a", false); err == nil {
		t.Fatal("CancelAgent error = nil")
	}
}

func TestQuitJoinsTUIOneshotAndShutsPhaseDownBeforePrune(t *testing.T) {
	stubTeaProgram(t)
	var order orderLog
	h := &phaseHarness{bg: &phaseBackground{}}
	started := make(chan struct{})
	host := h.host(func(ctx context.Context, in agent.DriverRunInput) (agent.DriverRunOutput, error) {
		close(started)
		<-ctx.Done()
		return agent.DriverRunOutput{Conversation: in.Conversation}, ctx.Err()
	})
	host.shutdown = func(context.Context, delegation.CancelCause) { order.add("phase-shutdown") }
	in := h.input()
	in.Session.Save = func(context.Context, agent.DriverSnapshot) error {
		order.add("phase-save")
		return nil
	}

	sess, err := interactive.NewSession(interactive.Dependencies{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sess.RunBackground(func(ctx context.Context) {
		_, runErr := runPhaseOnDriver(ctx, in, host)
		if !errors.Is(runErr, context.Canceled) {
			t.Errorf("phase error = %v, want context.Canceled", runErr)
		}
		order.add("orchestrator-exit")
	})
	<-started

	plan := tui.NewWorktreeCleanupPlan(nil, func(context.Context) (int, error) {
		order.add("prune")
		return 1, nil
	})
	plan.Request()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	rt := &cliRuntime{
		events:          output.NoopSink{},
		worktreeCleanup: plan,
		closeFn:         func() error { order.add("close-runtime"); return nil },
	}
	if err := runInteractiveSession(cmd, sess, nil, rt); err != nil {
		t.Fatalf("runInteractiveSession error = %v", err)
	}

	got := order.snapshot()
	shutdown, exit, prune := slices.Index(got, "phase-shutdown"), slices.Index(got, "orchestrator-exit"), slices.Index(got, "prune")
	if shutdown < 0 || exit < 0 || prune < 0 || shutdown > exit || exit > prune {
		t.Fatalf("order = %v, want phase-shutdown, orchestrator-exit, then prune", got)
	}
}
