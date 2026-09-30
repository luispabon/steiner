package delegation

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSupervisorFIFOStartOrder(t *testing.T) {
	t.Parallel()

	s, _ := newTestSupervisor(2, 0)
	a, b, c, d := newFakeChild("a", false), newFakeChild("b", false), newFakeChild("c", false), newFakeChild("d", false)

	resA := spawn(context.Background(), s, a.job)
	waitClosed(t, a.started, "a start")
	resB := spawn(context.Background(), s, b.job)
	waitClosed(t, b.started, "b start")
	resC := spawn(context.Background(), s, c.job)
	waitOutstanding(t, s, 3)
	resD := spawn(context.Background(), s, d.job)
	waitOutstanding(t, s, 4)

	for _, q := range []*fakeChild{c, d} {
		select {
		case <-q.executed:
			t.Fatalf("%s executed while both slots were taken", q.job.AgentID)
		default:
		}
	}

	close(a.release)
	recv(t, resA, "a result")
	waitClosed(t, c.started, "c start")
	select {
	case <-d.executed:
		t.Fatal("d started before c: queue is not FIFO")
	default:
	}

	close(b.release)
	recv(t, resB, "b result")
	waitClosed(t, d.started, "d start")

	close(c.release)
	close(d.release)
	recv(t, resC, "c result")
	if got := recv(t, resD, "d result"); got.result.Value != "done:d" {
		t.Fatalf("d result = %+v", got)
	}
}

func TestSupervisorOutstandingCapRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		maxParallel int
	}{
		{"max1", 1},
		{"max2", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, controller := newTestSupervisor(tt.maxParallel, 0)
			limit := 2 * tt.maxParallel
			children := make([]*fakeChild, 0, limit)
			results := make([]<-chan spawnResult, 0, limit)
			for i := range limit {
				child := newFakeChild(string(rune('a'+i)), false)
				children = append(children, child)
				results = append(results, spawn(context.Background(), s, child.job))
				waitOutstanding(t, s, i+1)
			}

			_, err := s.SpawnAndWait(context.Background(), newFakeChild("over", false).job)
			if !errors.Is(err, ErrOutstandingCap) {
				t.Fatalf("err = %v, want ErrOutstandingCap", err)
			}
			if want := "sub-agents already outstanding; wait for results before dispatching more"; !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %q, want it to contain %q", err, want)
			}
			if got := len(controller.ActiveAgentIDs()); got != tt.maxParallel {
				t.Fatalf("registered = %d, want %d (only started jobs register)", got, tt.maxParallel)
			}

			for i, child := range children {
				if i < tt.maxParallel {
					waitClosed(t, child.started, "start")
				}
				close(child.release)
			}
			for i, res := range results {
				if got := recv(t, res, "result"); got.err != nil {
					t.Fatalf("job %d err = %v", i, got.err)
				}
			}
			if ids := controller.ActiveAgentIDs(); len(ids) != 0 {
				t.Fatalf("active after joins = %v, want none", ids)
			}
		})
	}
}

func TestSupervisorCancelledQueuedJobNeverExecutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		cancel func(s *Supervisor)
	}{
		{"CancelAgent", func(s *Supervisor) { s.CancelAgent("b", false, CancelCauseUser) }},
		{"CancelAll", func(s *Supervisor) { s.CancelAll(CancelCauseSystem) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, controller := newTestSupervisor(1, 0)
			a, b := newFakeChild("a", true), newFakeChild("b", false)
			resA := spawn(context.Background(), s, a.job)
			waitClosed(t, a.started, "a start")
			resB := spawn(context.Background(), s, b.job)
			waitOutstanding(t, s, 2)
			stateB := jobFor(s, "b")

			tt.cancel(s)

			got := recv(t, resB, "b result")
			if got.err != nil || got.result.Value != "not-started:b" {
				t.Fatalf("b result = %+v, want OnCancelledBeforeStart result", got)
			}
			if causeOf(s, stateB) == CancelCauseNone {
				t.Fatal("cause for queued job was not recorded")
			}
			select {
			case <-b.executed:
				t.Fatal("cancelled queued job ran Execute")
			default:
			}

			close(a.release)
			recv(t, resA, "a result")
			select {
			case <-b.executed:
				t.Fatal("cancelled queued job ran Execute after slot freed")
			default:
			}
			if ids := controller.ActiveAgentIDs(); len(ids) != 0 {
				t.Fatalf("active after joins = %v, want none", ids)
			}
		})
	}
}
