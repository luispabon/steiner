package delegation

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

func TestShutdownWaitsBlockedQueuedFinalizerWithoutBlocking(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	s.joinTimeout = 20 * time.Millisecond
	running := newAsyncChild("running", "")
	queued := newAsyncChild("queued", "")
	callbackEntered, callbackRelease := make(chan struct{}), make(chan struct{})
	queued.job.OnCancelledBeforeStart = func() tool.ExecutionResult {
		close(callbackEntered)
		<-callbackRelease
		return tool.ExecutionResult{Value: Result{AgentID: "queued", Status: StatusCancelled}}
	}
	spawnAsync(context.Background(), t, s, running)
	waitClosed(t, running.started, "running child")
	spawnAsync(context.Background(), t, s, queued)
	shutdown := make(chan struct{})
	go func() { s.Shutdown(context.Background(), CancelCauseSystem); close(shutdown) }()
	waitClosed(t, callbackEntered, "queued cancellation finalizer")
	waitClosed(t, shutdown, "bounded shutdown")
	first := recv(t, sink.ch, "first timeout completion")
	second := recv(t, sink.ch, "second timeout completion")
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("completion batches = %+v, %+v", first, second)
	}
	close(callbackRelease)
	close(running.release)
	waitFinished(t, s, "queued")
	waitFinished(t, s, "running")
	sink.none(t)
	if got := jobFor(s, "queued").result.Value; got != nil {
		t.Fatalf("late callback replaced timeout waiter result: %v", got)
	}
}

func TestCancelAgentFinalizerShutdownSettlement(t *testing.T) {
	for _, mode := range []string{"async", "blocking"} {
		for _, releaseBeforeDeadline := range []bool{true, false} {
			name := mode + "/release-before-deadline"
			if !releaseBeforeDeadline {
				name = mode + "/release-after-deadline"
			}
			t.Run(name, func(t *testing.T) {
				s, sink := newAsyncSupervisor(1, nil)
				s.joinTimeout = 80 * time.Millisecond
				hold := newAsyncChild("hold", "")
				spawnAsync(context.Background(), t, s, hold)
				waitClosed(t, hold.started, "slot holder")
				scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
				cancelled := newAsyncChild("cancelled", "group")
				cancelled.job.GroupScope = scope
				entered, release, callbackDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
				cancelled.job.OnCancelledBeforeStart = func() tool.ExecutionResult {
					close(entered)
					<-release
					close(callbackDone)
					return tool.ExecutionResult{Value: Result{AgentID: "cancelled", Status: StatusCancelled}}
				}
				var waiter <-chan spawnResult
				ctx := agent.WithToolBatchID(context.Background(), "batch")
				if mode == "blocking" {
					waiter = spawn(ctx, s, cancelled.job)
				} else {
					spawnAsync(ctx, t, s, cancelled)
				}
				waitOutstanding(t, s, 2)
				// The cancelled child queues behind hold and has published acceptance.
				outcome := make(chan CancelOutcome, 1)
				go func() { outcome <- s.CancelAgent("cancelled", false, CancelCauseUser) }()
				if got := recv(t, outcome, "CancelAgent outcome"); got != CancelAccepted {
					t.Fatalf("CancelAgent = %v", got)
				}
				waitClosed(t, entered, "CancelAgent finalizer")
				if releaseBeforeDeadline {
					close(release)
					if mode == "blocking" {
						got := recv(t, waiter, "blocking waiter before shutdown")
						if got.result.Value == nil || got.err != nil {
							t.Fatalf("waiter = %+v", got)
						}
					}
				}
				shutdown := make(chan struct{})
				go func() { s.Shutdown(context.Background(), CancelCauseSystem); close(shutdown) }()
				if !releaseBeforeDeadline {
					waitClosed(t, shutdown, "shutdown deadline")
					if mode == "blocking" {
						got := recv(t, waiter, "blocking waiter timeout")
						if !errors.Is(got.err, ErrSupervisorClosed) {
							t.Fatalf("waiter err = %v", got.err)
						}
					}
					if mode == "async" {
						batch := recv(t, sink.ch, "running timeout completion")
						if len(batch) != 1 || batch[0].AgentID != "hold" {
							t.Fatalf("running completion = %+v", batch)
						}
						batch = recv(t, sink.ch, "async timeout completion")
						if len(batch) != 1 || batch[0].AgentID != "cancelled" {
							t.Fatalf("completion = %+v", batch)
						}
						s.MarkDelivered([]string{"call-cancelled"})
					}
					if got := s.SnapshotGroupLedger(scope).Names; len(got) != 1 {
						t.Fatalf("scope pruned while callback blocked: %v", got)
					}
					close(release)
				} else {
					waitClosed(t, shutdown, "shutdown settled before deadline")
				}
				if mode == "async" && releaseBeforeDeadline {
					batch := recv(t, sink.ch, "running completion")
					if len(batch) != 1 || batch[0].AgentID != "hold" {
						t.Fatalf("running completion = %+v", batch)
					}
					batch = recv(t, sink.ch, "async completion")
					if len(batch) != 1 || batch[0].AgentID != "cancelled" {
						t.Fatalf("completion = %+v", batch)
					}
					s.MarkDelivered([]string{"call-cancelled"})
				}
				waitClosed(t, callbackDone, "cancellation callback return")
				if mode == "async" && releaseBeforeDeadline {
					if s.IsPending("cancelled") {
						t.Fatal("acknowledged async job remains pending")
					}
				}
				if mode == "blocking" && releaseBeforeDeadline {
					select {
					case extra := <-waiter:
						t.Fatalf("duplicate waiter outcome: %+v", extra)
					default:
					}
				}
				if mode == "async" && !releaseBeforeDeadline {
					select {
					case extra := <-sink.ch:
						t.Fatalf("duplicate terminal completion: %+v", extra)
					default:
					}
				}
				if got := s.SnapshotGroupLedger(scope).Names; len(got) != 1 {
					t.Fatalf("scope ledger changed before release: %v", got)
				}
				s.ReleaseGroupScope(scope)
				if got := s.SnapshotGroupLedger(scope).Names; len(got) != 0 {
					t.Fatalf("scope not pruned after settlement: %v", got)
				}
				close(hold.release)
			})
		}
	}
}

func TestSupervisorShutdownCleanJoin(t *testing.T) {
	t.Parallel()

	s, controller := newTestSupervisor(1, 0)
	a, b := newFakeChild("a", true), newFakeChild("b", false)
	resA := spawn(context.Background(), s, a.job)
	waitClosed(t, a.started, "a start")
	resB := spawn(context.Background(), s, b.job)
	waitOutstanding(t, s, 2)
	stateA := jobFor(s, "a")

	report := s.Shutdown(context.Background(), CancelCauseSystem)
	if len(report.Unjoined) != 0 {
		t.Fatalf("unjoined = %+v, want none", report.Unjoined)
	}
	if !s.Closed() {
		t.Fatal("Closed() = false after Shutdown")
	}
	if got := recv(t, resA, "a result"); got.result.Value != "cancelled:a" {
		t.Fatalf("a result = %+v, want the child's own cancelled result", got)
	}
	if got := recv(t, resB, "b result"); got.result.Value != "not-started:b" {
		t.Fatalf("b result = %+v, want OnCancelledBeforeStart result", got)
	}
	if cause := causeOf(s, stateA); cause != CancelCauseSystem {
		t.Fatalf("cause = %v, want CancelCauseSystem", cause)
	}
	if ids := controller.ActiveAgentIDs(); len(ids) != 0 {
		t.Fatalf("active after shutdown = %v, want none", ids)
	}
	if paths := s.ProtectedWorktrees(); len(paths) != 0 {
		t.Fatalf("protected = %v, want none", paths)
	}
}

func TestSupervisorShutdownReportsUnjoinedChild(t *testing.T) {
	t.Parallel()

	s, _ := newTestSupervisor(1, 50*time.Millisecond)
	stuck := newFakeChild("stuck", false)
	res := spawn(context.Background(), s, stuck.job)
	waitClosed(t, stuck.started, "start")

	report := s.Shutdown(context.Background(), CancelCauseSystem)
	want := []UnjoinedChild{{AgentID: "stuck", AgentType: AgentTypeCode, ParentCallID: "call-stuck", WorktreePath: "/wt/stuck"}}
	if !reflect.DeepEqual(report.Unjoined, want) {
		t.Fatalf("unjoined = %+v, want %+v", report.Unjoined, want)
	}
	if paths := s.ProtectedWorktrees(); !reflect.DeepEqual(paths, []string{"/wt/stuck"}) {
		t.Fatalf("protected = %v, want [/wt/stuck]", paths)
	}
	if !s.Closed() {
		t.Fatal("Closed() = false after Shutdown")
	}

	got := recv(t, res, "waiter")
	if !errors.Is(got.err, ErrSupervisorClosed) {
		t.Fatalf("waiter err = %v, want ErrSupervisorClosed", got.err)
	}

	s.mu.Lock()
	exited := s.jobs["stuck"].exited
	s.mu.Unlock()
	close(stuck.release)
	waitClosed(t, exited, "late-returning job to exit")
	if got.result.Value != nil {
		t.Fatalf("late result delivered: %+v", got.result)
	}
	if got := s.CancelAgent("stuck", false, CancelCauseUser); got != CancelNotActive {
		t.Fatalf("CancelAgent after late exit = %v, want CancelNotActive", got)
	}
}

func TestSupervisorShutdownIdempotent(t *testing.T) {
	t.Parallel()

	s, _ := newTestSupervisor(1, 20*time.Millisecond)
	stuck := newFakeChild("stuck", false)
	spawn(context.Background(), s, stuck.job)
	waitClosed(t, stuck.started, "start")

	first := s.Shutdown(context.Background(), CancelCauseSystem)
	second := s.Shutdown(context.Background(), CancelCauseUser)
	if len(first.Unjoined) != 1 || !reflect.DeepEqual(first, second) {
		t.Fatalf("reports differ: first=%+v second=%+v", first, second)
	}
	if causeFor(s, "stuck") != CancelCauseSystem {
		t.Fatalf("second Shutdown changed cause to %v", causeFor(s, "stuck"))
	}
	close(stuck.release)
}

func TestSupervisorShutdownBoundedByContext(t *testing.T) {
	t.Parallel()

	s, _ := newTestSupervisor(1, time.Hour)
	stuck := newFakeChild("stuck", false)
	spawn(context.Background(), s, stuck.job)
	waitClosed(t, stuck.started, "start")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := s.Shutdown(ctx, CancelCauseSystem)
	if len(report.Unjoined) != 1 {
		t.Fatalf("unjoined = %+v, want the stuck child", report.Unjoined)
	}
	close(stuck.release)
}

func TestSupervisorSpawnAfterShutdownErrors(t *testing.T) {
	t.Parallel()

	s, controller := newTestSupervisor(1, 0)
	s.Shutdown(context.Background(), CancelCauseSystem)

	child := newFakeChild("a", false)
	if _, err := s.SpawnAndWait(context.Background(), child.job); !errors.Is(err, ErrSupervisorClosed) {
		t.Fatalf("err = %v, want ErrSupervisorClosed", err)
	}
	select {
	case <-child.executed:
		t.Fatal("job executed after shutdown")
	default:
	}
	if ids := controller.ActiveAgentIDs(); len(ids) != 0 {
		t.Fatalf("registered after rejected spawn = %v", ids)
	}
}
