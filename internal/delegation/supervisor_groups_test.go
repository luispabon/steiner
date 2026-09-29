package delegation

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func batchCtx(id string) context.Context {
	return agent.WithToolBatchID(context.Background(), id)
}

func TestSupervisorGroupHeldUntilSealed(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	a := newAsyncChild("a", "g")
	spawnAsync(batchCtx("b1"), t, s, a)
	<-a.started
	close(a.release)

	// a finished before its sibling registered; the open group must not release.
	waitFinished(t, s, "a")
	sink.none(t)
	if got := s.Pending(); len(got) != 1 || got[0].State != agent.SubAgentFinished {
		t.Fatalf("Pending = %+v, want a finished", got)
	}
	s.MarkDelivered([]string{"call-a"})
	if !s.IsPending("a") {
		t.Fatal("held member must not be ackable")
	}

	b := newAsyncChild("b", "g")
	spawnAsync(batchCtx("b1"), t, s, b)
	<-b.started
	close(b.release)
	waitFinished(t, s, "b")
	sink.none(t)

	s.SealBatch("b1")
	batch := recv(t, sink.ch, "group release")
	if len(batch) != 2 || batch[0].AgentID != "a" || batch[1].AgentID != "b" {
		t.Fatalf("released = %+v, want a then b", batch)
	}
	s.MarkDelivered([]string{"call-a", "call-b"})
	if s.HasPending() {
		t.Fatal("released group still pending after MarkDelivered")
	}
}

func TestSupervisorGroupReleasedWhenSealedBeforeLastFinish(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	a, b := newAsyncChild("a", "g"), newAsyncChild("b", "g")
	spawnAsync(batchCtx("b1"), t, s, a)
	spawnAsync(batchCtx("b1"), t, s, b)
	<-a.started
	<-b.started
	s.SealBatch("b1")
	close(a.release)
	waitFinished(t, s, "a")
	sink.none(t)
	close(b.release)
	if batch := recv(t, sink.ch, "group release"); len(batch) != 2 {
		t.Fatalf("released = %+v", batch)
	}
}

func TestSupervisorGroupOrderedBySeq(t *testing.T) {
	s, sink := newAsyncSupervisor(3, nil)
	kids := []*asyncChild{newAsyncChild("a", "g"), newAsyncChild("b", "g"), newAsyncChild("c", "g")}
	for _, kid := range kids {
		spawnAsync(batchCtx("b1"), t, s, kid)
		<-kid.started
	}
	// Finish in reverse of enqueue order.
	for _, kid := range []*asyncChild{kids[2], kids[0], kids[1]} {
		close(kid.release)
		waitFinished(t, s, kid.job.AgentID)
	}
	s.SealBatch("b1")
	batch := recv(t, sink.ch, "group release")
	var ids []string
	for i, c := range batch {
		ids = append(ids, c.AgentID)
		if i > 0 && batch[i-1].Seq >= c.Seq {
			t.Fatalf("batch not ordered by seq: %+v", batch)
		}
	}
	if want := []string{"c", "a", "b"}; len(ids) != 3 || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestSupervisorLabelReuseAcrossBatchesMakesSeparateGroups(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	a, b := newAsyncChild("a", "g"), newAsyncChild("b", "g")
	spawnAsync(batchCtx("b1"), t, s, a)
	spawnAsync(batchCtx("b2"), t, s, b)
	<-a.started
	<-b.started
	close(a.release)
	close(b.release)
	waitFinished(t, s, "a")
	waitFinished(t, s, "b")

	s.SealBatch("b2")
	batch := recv(t, sink.ch, "b2 group")
	if len(batch) != 1 || batch[0].AgentID != "b" {
		t.Fatalf("b2 released %+v, want only b", batch)
	}
	sink.none(t)
	s.SealBatch("b1")
	batch = recv(t, sink.ch, "b1 group")
	if len(batch) != 1 || batch[0].AgentID != "a" {
		t.Fatalf("b1 released %+v, want only a", batch)
	}
}

func TestSupervisorHeldGroupDoesNotBlockUngrouped(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	member, loose := newAsyncChild("m", "g"), newAsyncChild("u", "")
	spawnAsync(batchCtx("b1"), t, s, member)
	spawnAsync(batchCtx("b1"), t, s, loose)
	<-member.started
	<-loose.started
	close(member.release)
	waitFinished(t, s, "m")
	close(loose.release)
	batch := recv(t, sink.ch, "ungrouped")
	if len(batch) != 1 || batch[0].AgentID != "u" {
		t.Fatalf("posted %+v, want only u", batch)
	}
	sink.none(t)
}

func TestSupervisorCancelledMemberSettlesGroup(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	a, b := newAsyncChild("a", "g"), newAsyncChild("b", "g")
	spawnAsync(batchCtx("b1"), t, s, a)
	spawnAsync(batchCtx("b1"), t, s, b)
	<-a.started
	s.SealBatch("b1")

	if got := s.CancelAgent("b", false, CancelCauseUser); got != CancelAccepted {
		t.Fatalf("cancel queued member = %v", got)
	}
	sink.none(t)
	if got := s.CancelAgent("a", false, CancelCauseUser); got != CancelAccepted {
		t.Fatalf("cancel running member = %v", got)
	}
	batch := recv(t, sink.ch, "group release")
	if len(batch) != 2 || batch[0].Status != "cancelled" || batch[1].Status != "cancelled" || !batch[0].Quiet || !batch[1].Quiet {
		t.Fatalf("released = %+v, want both cancelled and quiet", batch)
	}
}

func TestSupervisorGroupingRequiresBatchAndLabel(t *testing.T) {
	tests := []struct {
		name  string
		ctx   context.Context
		group string
	}{
		{"no batch id", context.Background(), "g"},
		{"no label", batchCtx("b1"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, sink := newAsyncSupervisor(1, nil)
			a := newAsyncChild("a", tt.group)
			spawnAsync(tt.ctx, t, s, a)
			close(a.release)
			if batch := recv(t, sink.ch, "immediate post"); len(batch) != 1 {
				t.Fatalf("posted %+v", batch)
			}
		})
	}
}

func TestSupervisorCapRejectedCallNeverJoinsGroup(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	a, b, c := newAsyncChild("a", "g"), newAsyncChild("b", "g"), newAsyncChild("c", "g")
	spawnAsync(batchCtx("b1"), t, s, a)
	spawnAsync(batchCtx("b1"), t, s, b)
	if _, err := s.Spawn(batchCtx("b1"), c.job); !errors.Is(err, ErrOutstandingCap) {
		t.Fatalf("third spawn error = %v, want ErrOutstandingCap", err)
	}
	s.SealBatch("b1")
	close(a.release)
	<-b.started
	close(b.release)
	if batch := recv(t, sink.ch, "group release"); len(batch) != 2 {
		t.Fatalf("released = %+v, want a and b only", batch)
	}
}

// waitFinished blocks until the agent has a final completion recorded.
func waitFinished(t *testing.T, s *Supervisor, id string) {
	t.Helper()
	s.mu.Lock()
	state := s.jobs[id]
	s.mu.Unlock()
	<-state.done
}
