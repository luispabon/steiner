package delegation

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

// testBatchID returns a tool batch ID in the runtime format with sequence n.
func testBatchID(n uint64) string {
	return fmt.Sprintf("call#%d", n)
}

func batchCtx(id string) context.Context {
	return agent.WithToolBatchID(context.Background(), id)
}

func TestSupervisorGroupHeldUntilSealed(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	a := newAsyncChild("a", "g")
	spawnAsync(batchCtx(testBatchID(1)), t, s, a)
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
	spawnAsync(batchCtx(testBatchID(1)), t, s, b)
	<-b.started
	close(b.release)
	waitFinished(t, s, "b")
	sink.none(t)

	s.SealGroupBatch("", testBatchID(1))
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
	spawnAsync(batchCtx(testBatchID(1)), t, s, a)
	spawnAsync(batchCtx(testBatchID(1)), t, s, b)
	<-a.started
	<-b.started
	s.SealGroupBatch("", testBatchID(1))
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
		spawnAsync(batchCtx(testBatchID(1)), t, s, kid)
		<-kid.started
	}
	// Finish in reverse of enqueue order.
	for _, kid := range []*asyncChild{kids[2], kids[0], kids[1]} {
		close(kid.release)
		waitFinished(t, s, kid.job.AgentID)
	}
	s.SealGroupBatch("", testBatchID(1))
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

func TestSupervisorLabelReuseAcrossBatchesRejectsConversationReuse(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	a, b := newAsyncChild("a", "g"), newAsyncChild("b", "g")
	a.job.GroupScope, b.job.GroupScope = scope, scope
	spawnAsync(batchCtx(testBatchID(1)), t, s, a)
	if _, _, err := s.Spawn(batchCtx(testBatchID(2)), b.job); err == nil {
		t.Fatal("reused group name in another batch was accepted")
	}
	<-a.started
	close(a.release)
	waitFinished(t, s, "a")

	s.SealGroupBatch(scope, testBatchID(1))
	batch := recv(t, sink.ch, "b1 group")
	if len(batch) != 1 || batch[0].AgentID != "a" {
		t.Fatalf("b1 released %+v, want only a", batch)
	}
}

func TestSupervisorHeldGroupDoesNotBlockUngrouped(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	member, loose := newAsyncChild("m", "g"), newAsyncChild("u", "")
	spawnAsync(batchCtx(testBatchID(1)), t, s, member)
	spawnAsync(batchCtx(testBatchID(1)), t, s, loose)
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
	spawnAsync(batchCtx(testBatchID(1)), t, s, a)
	spawnAsync(batchCtx(testBatchID(1)), t, s, b)
	<-a.started
	s.SealGroupBatch("", testBatchID(1))

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
		{"no label", batchCtx(testBatchID(1)), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, sink := newAsyncSupervisor(1, nil)
			a := newAsyncChild("a", tt.group)
			if tt.name == "no batch id" {
				a.job.GroupScope = s.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
			}
			if tt.group != "" {
				if _, _, err := s.Spawn(tt.ctx, a.job); err == nil {
					t.Fatal("named group without batch was accepted")
				}
				sink.none(t)
				return
			}
			spawnAsync(tt.ctx, t, s, a)
			close(a.release)
			if batch := recv(t, sink.ch, "immediate post"); len(batch) != 1 {
				t.Fatalf("posted %+v", batch)
			}
		})
	}
}

func TestSupervisorNameRejectionDoesNotConsumeFreshName(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	a := newAsyncChild("a", "g")
	a.job.GroupScope = scope
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), a.job); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), a.job); err == nil {
		t.Fatal("duplicate active agent ID was accepted")
	}
	b := newAsyncChild("b", "h")
	b.job.GroupScope = scope
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), b.job); err != nil {
		t.Fatalf("different name rejected: %v", err)
	}
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), b.job); err == nil {
		t.Fatal("duplicate active agent ID was accepted")
	}
	c := newAsyncChild("c", "i")
	c.job.GroupScope = scope
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), c.job); !errors.Is(err, ErrOutstandingCap) {
		t.Fatalf("fresh-name cap rejection = %v, want ErrOutstandingCap", err)
	}
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 2 || got[0] != "g" || got[1] != "h" {
		t.Fatalf("rejected fresh name changed ledger: %v", got)
	}
	<-a.started
	close(a.release)
	<-b.started
	close(b.release)
	waitFinished(t, s, "b")
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), c.job); err != nil {
		t.Fatalf("rejected call consumed name i: %v", err)
	}
	<-c.started
	close(c.release)
	s.SealGroupBatch(scope, testBatchID(1))
	batches := []string{}
	for range 3 {
		batch := recv(t, sink.ch, "fresh name group")
		if len(batch) != 1 {
			t.Fatalf("batch = %+v", batch)
		}
		batches = append(batches, batch[0].AgentID)
	}
	if batches[0] != "a" || batches[1] != "b" || batches[2] != "c" {
		t.Fatalf("batch order = %v", batches)
	}
}

func TestSupervisorSealGroupBatchRejectsLateJoinsBeforeAndAfterAck(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	a := newAsyncChild("a", "g")
	spawnAsync(batchCtx(testBatchID(1)), t, s, a)
	waitClosed(t, a.started, "a started")
	s.SealGroupBatch("", testBatchID(1))
	b := newAsyncChild("b", "g")
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), b.job); err == nil {
		t.Fatal("SealGroupBatch accepted a late join before the last member finished")
	}
	close(a.release)
	batch := recv(t, sink.ch, "sealed group completion")
	if len(batch) != 1 || batch[0].AgentID != "a" {
		t.Fatalf("completion = %+v", batch)
	}
	s.MarkDelivered([]string{"call-a"})
	late := newAsyncChild("late", "g")
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), late.job); err == nil {
		t.Fatal("SealGroupBatch accepted a late join after the last member was acknowledged")
	}
	sink.none(t)
}

func TestSupervisorCapRejectedCallNeverJoinsGroup(t *testing.T) {
	s, sink := newAsyncSupervisor(1, nil)
	a, b, c := newAsyncChild("a", "g"), newAsyncChild("b", "g"), newAsyncChild("c", "g")
	spawnAsync(batchCtx(testBatchID(1)), t, s, a)
	spawnAsync(batchCtx(testBatchID(1)), t, s, b)
	if _, _, err := s.Spawn(batchCtx(testBatchID(1)), c.job); !errors.Is(err, ErrOutstandingCap) {
		t.Fatalf("third spawn error = %v, want ErrOutstandingCap", err)
	}
	s.SealGroupBatch("", testBatchID(1))
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
