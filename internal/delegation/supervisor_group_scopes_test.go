package delegation

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
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
	if _, err := s.Spawn(agent.WithToolBatchID(context.Background(), "batch"), job.job); err == nil {
		t.Fatal("sealed empty batch accepted a group")
	}
}
