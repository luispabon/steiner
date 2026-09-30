package delegation

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

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
