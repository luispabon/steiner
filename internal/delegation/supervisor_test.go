package delegation

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/tool"
)

type spawnResult struct {
	result tool.ExecutionResult
	err    error
}

// fakeChild is a job whose Execute signals started, then blocks until release
// is closed (or, when honourCancel is set, until its context is cancelled).
type fakeChild struct {
	job          ChildJob
	started      chan struct{}
	release      chan struct{}
	executed     chan struct{}
	honourCancel bool
}

func newFakeChild(id string, honourCancel bool) *fakeChild {
	f := &fakeChild{
		started:      make(chan struct{}),
		release:      make(chan struct{}),
		executed:     make(chan struct{}),
		honourCancel: honourCancel,
	}
	f.job = ChildJob{
		AgentID:      id,
		AgentType:    AgentTypeCode,
		ParentCallID: "call-" + id,
		Worktree:     CodeWorktree{Path: "/wt/" + id},
		Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
			close(f.executed)
			close(f.started)
			if f.honourCancel {
				select {
				case <-f.release:
				case <-ctx.Done():
					return tool.ExecutionResult{Value: "cancelled:" + id}, nil
				}
			} else {
				<-f.release
			}
			return tool.ExecutionResult{Value: "done:" + id}, nil
		},
		OnCancelledBeforeStart: func() tool.ExecutionResult {
			return tool.ExecutionResult{Value: "not-started:" + id}
		},
	}
	return f
}

func spawn(ctx context.Context, s *Supervisor, job ChildJob) <-chan spawnResult {
	ch := make(chan spawnResult, 1)
	go func() {
		res, err := s.SpawnAndWait(ctx, job)
		ch <- spawnResult{res, err}
	}()
	return ch
}

func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	recv(t, ch, what)
}

// waitOutstanding spins until the supervisor has exactly n running+queued jobs.
func waitOutstanding(t *testing.T, s *Supervisor, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.mu.Lock()
		got := s.running + len(s.queue)
		s.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("outstanding = %d, want %d", got, n)
		}
		runtime.Gosched()
	}
}

func newTestSupervisor(maxParallel int, joinTimeout time.Duration) (*Supervisor, *ActiveController) {
	controller := NewActiveController()
	return NewSupervisor(SupervisorOptions{MaxParallel: maxParallel, Controller: controller, JoinTimeout: joinTimeout}), controller
}

func TestSupervisorSpawnAndWaitDeliversResultAndCleansUp(t *testing.T) {
	t.Parallel()

	s, controller := newTestSupervisor(2, 0)
	child := newFakeChild("a", false)
	res := spawn(context.Background(), s, child.job)

	waitClosed(t, child.started, "start")
	if ids := controller.ActiveAgentIDs(); len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("active ids while running = %v, want [a]", ids)
	}
	close(child.release)

	got := recv(t, res, "result")
	if got.err != nil || got.result.Value != "done:a" {
		t.Fatalf("result = %+v, want done:a", got)
	}
	if ids := controller.ActiveAgentIDs(); len(ids) != 0 {
		t.Fatalf("active ids after join = %v, want none", ids)
	}
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if running != 0 {
		t.Fatalf("running = %d, want 0", running)
	}
}

func TestSupervisorCauseRecordedBeforeContextCancelled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cause CancelCause
	}{
		{"user", CancelCauseUser},
		{"system", CancelCauseSystem},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, _ := newTestSupervisor(1, 0)
			observed := make(chan CancelCause, 1)
			started := make(chan struct{})
			job := ChildJob{
				AgentID: "a",
				Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
					close(started)
					<-ctx.Done()
					observed <- s.CauseFor("a")
					return tool.ExecutionResult{}, nil
				},
			}
			res := spawn(context.Background(), s, job)
			waitClosed(t, started, "start")

			if out := s.CancelAgent("a", false, tt.cause); out != CancelAccepted {
				t.Fatalf("CancelAgent = %v, want CancelAccepted", out)
			}
			if got := recv(t, observed, "cause"); got != tt.cause {
				t.Fatalf("cause seen at ctx.Done = %v, want %v", got, tt.cause)
			}
			recv(t, res, "result")
		})
	}
}

func TestSupervisorCancelOutcomes(t *testing.T) {
	t.Parallel()

	s, _ := newTestSupervisor(1, 0)
	if out := s.CancelAgent("missing", false, CancelCauseUser); out != CancelNotActive {
		t.Fatalf("unknown agent = %v, want CancelNotActive", out)
	}

	child := newFakeChild("a", false)
	res := spawn(context.Background(), s, child.job)
	waitClosed(t, child.started, "start")

	if out := s.CancelAgent("a", false, CancelCauseUser); out != CancelAccepted {
		t.Fatalf("running agent = %v, want CancelAccepted", out)
	}
	if out := s.CancelAgent("a", false, CancelCauseSystem); out != CancelAccepted {
		t.Fatalf("running agent, second cancel = %v, want CancelAccepted (Execute has not returned)", out)
	}
	if got := s.CauseFor("a"); got != CancelCauseUser {
		t.Fatalf("cause = %v, want first cause CancelCauseUser", got)
	}

	close(child.release)
	recv(t, res, "result")
	if out := s.CancelAgent("a", false, CancelCauseUser); out != CancelAlreadyFinished {
		t.Fatalf("after Execute returned = %v, want CancelAlreadyFinished", out)
	}
}

func TestSupervisorSpawnAndWaitForwardsHandlerCancellation(t *testing.T) {
	t.Parallel()

	s, _ := newTestSupervisor(1, 0)
	child := newFakeChild("a", true)
	handlerCtx, cancel := context.WithCancel(context.Background())
	res := spawn(handlerCtx, s, child.job)
	waitClosed(t, child.started, "start")

	cancel()
	got := recv(t, res, "result")
	if got.err != nil || got.result.Value != "cancelled:a" {
		t.Fatalf("result = %+v, want the child's own cancelled result", got)
	}
	if cause := s.CauseFor("a"); cause != CancelCauseUser {
		t.Fatalf("cause = %v, want CancelCauseUser", cause)
	}
}

func TestSupervisorDuplicateAgentID(t *testing.T) {
	t.Parallel()

	s, _ := newTestSupervisor(2, 0)
	child := newFakeChild("a", false)
	res := spawn(context.Background(), s, child.job)
	waitClosed(t, child.started, "start")

	dup := newFakeChild("a", false)
	if _, err := s.SpawnAndWait(context.Background(), dup.job); !errors.Is(err, ErrAgentAlreadyActive) {
		t.Fatalf("duplicate spawn err = %v, want ErrAgentAlreadyActive", err)
	}
	close(child.release)
	recv(t, res, "result")
}
