package delegation

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

func TestGroupScopeSeedSnapshotAndIndependentScopes(t *testing.T) {
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1})
	seed := agent.DelegationGroupLedger{Version: 1, Names: []string{"seed"}}
	first := s.NewGroupScope(seed)
	second := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	if first == second {
		t.Fatal("scope IDs reused")
	}
	if got := s.SnapshotGroupLedger(first); !reflect.DeepEqual(got, seed) {
		t.Fatalf("snapshot = %+v, want %+v", got, seed)
	}
	snapshot := s.SnapshotGroupLedger(first)
	snapshot.Names[0] = "mutated"
	if got := s.SnapshotGroupLedger(first).Names[0]; got != "seed" {
		t.Fatalf("snapshot mutation changed ledger: %q", got)
	}
	if err := seed.Validate(); err != nil {
		t.Fatal(err)
	}
	job := newAsyncChild("a", "seed")
	job.job.GroupScope = first
	if _, err := s.Spawn(agent.WithToolBatchID(context.Background(), "b"), job.job); err == nil {
		t.Fatal("seeded name was accepted")
	}
	job.job.AgentID = "b"
	job.job.GroupScope = second
	if _, err := s.Spawn(agent.WithToolBatchID(context.Background(), "b"), job.job); err != nil {
		t.Fatalf("same name in independent scope rejected: %v", err)
	}
	s.CancelAll(CancelCauseSystem)
}

func TestSealedEmptyBatchRejectsLaterGroup(t *testing.T) {
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1})
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	s.SealGroupBatch(scope, "batch")
	job := newAsyncChild("a", "g")
	job.job.GroupScope = scope
	_, admission, err := s.SpawnWithAdmission(agent.WithToolBatchID(context.Background(), "batch"), job.job)
	if err == nil {
		t.Fatal("sealed empty batch accepted a group")
	}
	var reservationErr *groupReservationError
	if !errors.As(err, &reservationErr) || reservationErr.name != "g" || reservationErr.batch != "batch" || !reservationErr.sealed {
		t.Fatalf("reservation error = %T %#v, want sealed group g", err, err)
	}
	if got, want := err.Error(), `delegation group batch "batch" is sealed; use a fresh group name`; got != want {
		t.Fatalf("reservation error text = %q, want %q", got, want)
	}
	wantAdmission := &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, BatchID: "batch", Group: "g", AgentID: "a"}
	var carrier tool.DelegationAdmissionCarrier
	if admission == nil || *admission != *wantAdmission || !errors.As(err, &carrier) || *carrier.DelegationAdmissionMetadata() != *wantAdmission {
		t.Fatalf("rejected admission = %+v, err = %v", admission, err)
	}
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 0 {
		t.Fatalf("sealed rejection reserved group name: %v", got)
	}
}

func TestGroupScopeCanonicalNamesAndWhitespaceUngrouped(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	ctx := batchCtx("batch")
	a := newAsyncChild("a", " g ")
	a.job.GroupScope = scope
	spawnAsync(ctx, t, s, a)
	b := newAsyncChild("b", "g")
	b.job.GroupScope = scope
	spawnAsync(ctx, t, s, b)
	c := newAsyncChild("c", "   ")
	c.job.GroupScope = scope
	spawnAsync(ctx, t, s, c)
	for _, child := range []*asyncChild{a, b} {
		waitClosed(t, child.started, child.job.AgentID+" started")
	}
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 1 || got[0] != "g" {
		t.Fatalf("canonical ledger names = %v, want [g]", got)
	}
	conflict := newAsyncChild("conflict", "g")
	conflict.job.GroupScope = scope
	_, err := s.Spawn(batchCtx("other"), conflict.job)
	if err == nil {
		t.Fatal("canonical duplicate name in another batch was accepted")
	}
	var reservationErr *groupReservationError
	if !errors.As(err, &reservationErr) || reservationErr.name != "g" || reservationErr.batch != "other" || reservationErr.sealed {
		t.Fatalf("reservation error = %T %#v, want reused name g", err, err)
	}
	if got, want := err.Error(), `delegation group name "g" was already used; choose a fresh name`; got != want {
		t.Fatalf("reservation error text = %q, want %q", got, want)
	}
	close(a.release)
	waitClosed(t, c.started, "c started")
	close(c.release)
	batch := recv(t, sink.ch, "whitespace-only ungrouped completion")
	if len(batch) != 1 || batch[0].AgentID != "c" {
		t.Fatalf("ungrouped completion = %+v", batch)
	}
	sink.none(t)
	close(b.release)
	stateA, stateB := jobFor(s, "a"), jobFor(s, "b")
	if stateA == nil || stateB == nil {
		t.Fatal("held completion states missing before acknowledgment")
	}
	s.MarkDelivered([]string{"call-a", "call-b"})
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 1 {
		t.Fatalf("scope pruned while held completions were unacknowledged: %v", got)
	}
	s.SealGroupBatch(scope, "batch")
	batch = recv(t, sink.ch, "canonical group completion")
	if len(batch) != 2 || batch[0].AgentID != "a" || batch[1].AgentID != "b" {
		t.Fatalf("canonical group batch = %+v", batch)
	}
}

func TestReleasedGroupScopeRetainedUntilAcknowledgedJobsPruned(t *testing.T) {
	s, sink := newAsyncSupervisor(2, nil)
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	a, b := newAsyncChild("a", "group"), newAsyncChild("b", "group")
	a.job.GroupScope, b.job.GroupScope = scope, scope
	ctx := batchCtx("batch")
	spawnAsync(ctx, t, s, a)
	spawnAsync(ctx, t, s, b)
	waitClosed(t, a.started, "a started")
	waitClosed(t, b.started, "b started")
	s.ReleaseGroupScope(scope)
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 1 {
		t.Fatalf("scope pruned while members running: %v", got)
	}
	close(a.release)
	close(b.release)
	s.SealGroupBatch(scope, "batch")
	batch := recv(t, sink.ch, "group completion")
	if len(batch) != 2 {
		t.Fatalf("completion batch = %+v", batch)
	}
	stateA, stateB := jobFor(s, "a"), jobFor(s, "b")
	if stateA == nil || stateB == nil {
		t.Fatal("job states missing before acknowledgment")
	}
	s.ReleaseGroupScope(scope)
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 1 || got[0] != "group" {
		t.Fatalf("scope pruned while jobs unacknowledged: %v", got)
	}
	s.MarkDelivered([]string{"call-a"})
	waitClosed(t, stateA.settled, "a settlement")
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 1 {
		t.Fatalf("scope pruned before last acknowledgment: %v", got)
	}
	s.MarkDelivered([]string{"call-b"})
	waitClosed(t, stateB.settled, "b settlement")
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 0 {
		t.Fatalf("scope retained after safe prune: %v", got)
	}
}

func TestConcurrentGroupScopeJoinsShareBatch(t *testing.T) {
	s, sink := newAsyncSupervisor(4, nil)
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	ctx := batchCtx("batch")
	const count = 4
	children := make([]*asyncChild, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range children {
		children[i] = newAsyncChild(string(rune('a'+i)), "shared")
		children[i].job.GroupScope = scope
		wg.Add(1)
		go func(child *asyncChild) {
			defer wg.Done()
			<-start
			if _, err := s.Spawn(ctx, child.job); err != nil {
				t.Errorf("Spawn(%s): %v", child.job.AgentID, err)
			}
		}(children[i])
	}
	close(start)
	wg.Wait()
	for _, child := range children {
		waitClosed(t, child.started, child.job.AgentID+" started")
	}
	if got := s.SnapshotGroupLedger(scope).Names; len(got) != 1 || got[0] != "shared" {
		t.Fatalf("shared ledger names = %v", got)
	}
	s.SealGroupBatch(scope, "batch")
	for _, child := range children {
		close(child.release)
	}
	batch := recv(t, sink.ch, "concurrent group completion")
	if len(batch) != count {
		t.Fatalf("completion count = %d, want %d: %+v", len(batch), count, batch)
	}
	for _, child := range children {
		found := false
		for _, completion := range batch {
			found = found || completion.AgentID == child.job.AgentID
		}
		if !found {
			t.Errorf("completion missing %s: %+v", child.job.AgentID, batch)
		}
	}
}
