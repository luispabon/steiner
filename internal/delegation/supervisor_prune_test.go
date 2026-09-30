package delegation

import (
	"context"
	"testing"
	"time"
)

func hasJob(s *Supervisor, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.jobs[id]
	return ok
}

func TestSupervisorPrunesAsyncJobOnlyWhenDeliveredAndFinished(t *testing.T) {
	tests := []struct {
		name          string
		deliver       bool
		wantInMap     bool
		wantPending   bool
		wantCancelOut CancelOutcome
	}{
		{"finished but undelivered stays", false, true, true, CancelAlreadyFinished},
		{"finished and delivered is pruned", true, false, false, CancelNotActive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, sink := newAsyncSupervisor(1, nil)
			a := newAsyncChild("a", "")
			spawnAsync(context.Background(), t, s, a)
			<-a.started
			close(a.release)
			recv(t, sink.ch, "completion")
			if tt.deliver {
				s.MarkDelivered([]string{"call-a"})
			}
			if got := hasJob(s, "a"); got != tt.wantInMap {
				t.Fatalf("job in map = %v, want %v", got, tt.wantInMap)
			}
			if got := s.IsPending("a"); got != tt.wantPending {
				t.Fatalf("IsPending = %v, want %v", got, tt.wantPending)
			}
			if got := s.CancelAgent("a", false, CancelCauseUser); got != tt.wantCancelOut {
				t.Fatalf("CancelAgent = %v, want %v", got, tt.wantCancelOut)
			}
		})
	}
}

func TestSupervisorPrunesSpawnAndWaitJobOnReturn(t *testing.T) {
	t.Parallel()

	s, _ := newTestSupervisor(1, 0)
	child := newFakeChild("a", false)
	res := spawn(context.Background(), s, child.job)
	waitClosed(t, child.started, "start")
	if !hasJob(s, "a") {
		t.Fatal("running job missing from map")
	}
	close(child.release)
	recv(t, res, "result")
	if hasJob(s, "a") {
		t.Fatal("job still in map after SpawnAndWait returned")
	}
}

func TestSupervisorStaleStateDoesNotPruneReusedAgentID(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	first := newAsyncChild("a", "")
	spawnAsync(context.Background(), t, s, first)
	<-first.started
	close(first.release)
	recv(t, sink.ch, "first completion")
	s.mu.Lock()
	old := s.jobs["a"]
	s.mu.Unlock()
	s.MarkDelivered([]string{"call-a"})

	second := newAsyncChild("a", "")
	spawnAsync(context.Background(), t, s, second)
	<-second.started

	s.mu.Lock()
	defer s.mu.Unlock()
	if !old.acked || old.phase != phaseDone {
		t.Fatalf("old state acked=%v phase=%v, want acked and done", old.acked, old.phase)
	}
	current := s.jobs["a"]
	if current == nil || current == old {
		t.Fatal("reused agent ID not tracked by a new state")
	}
	s.pruneLocked(old)
	if s.jobs["a"] != current {
		t.Fatal("stale state pruned the reused agent ID")
	}
}

func TestSupervisorPrunesUnjoinedChildWhenLateRunFinishes(t *testing.T) {
	tests := []struct {
		name  string
		async bool
	}{
		{"blocking waiter acked at shutdown", false},
		{"async completion delivered after shutdown", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, sink := newAsyncSupervisor(1, nil)
			s.joinTimeout = 20 * time.Millisecond
			stuck := newFakeChild("stuck", false)
			var res <-chan spawnResult
			if tt.async {
				if _, err := s.Spawn(context.Background(), stuck.job); err != nil {
					t.Fatalf("Spawn: %v", err)
				}
			} else {
				res = spawn(context.Background(), s, stuck.job)
			}
			waitClosed(t, stuck.started, "start")

			s.Shutdown(context.Background(), CancelCauseSystem)
			if tt.async {
				recv(t, sink.ch, "shutdown completion")
				s.MarkDelivered([]string{"call-stuck"})
			} else {
				recv(t, res, "waiter")
			}
			s.mu.Lock()
			exited := s.jobs["stuck"].exited
			s.mu.Unlock()
			if !hasJob(s, "stuck") {
				t.Fatal("unjoined running job pruned before its late exit")
			}

			close(stuck.release)
			waitClosed(t, exited, "late exit")
			if hasJob(s, "stuck") {
				t.Fatal("unjoined job still in map after late exit")
			}
		})
	}
}
