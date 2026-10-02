package delegation

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestGroupScopeSealsBySequence(t *testing.T) {
	tests := []struct {
		name       string
		seal       []string
		join       string
		wantSealed bool
		wantParse  bool
	}{
		{name: "current open batch joins", seal: nil, join: testBatchID(3)},
		{name: "later batch joins after earlier seal", seal: []string{testBatchID(2)}, join: testBatchID(3)},
		{name: "sealed batch rejects", seal: []string{testBatchID(3)}, join: testBatchID(3), wantSealed: true},
		{name: "older batch rejected after newer sealed", seal: []string{testBatchID(2), testBatchID(5)}, join: testBatchID(3), wantSealed: true},
		{name: "seal order does not lower the mark", seal: []string{testBatchID(5), testBatchID(2)}, join: testBatchID(4), wantSealed: true},
		{name: "unparseable batch is a distinct error", seal: nil, join: "no-sequence", wantParse: true},
		{name: "unparseable seal does not seal anything", seal: []string{"no-sequence"}, join: testBatchID(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSupervisor(SupervisorOptions{MaxParallel: 1})
			scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
			for _, id := range tt.seal {
				s.SealGroupBatch(scope, id)
			}
			s.mu.Lock()
			err := s.reserveGroupLocked(scope, "g", tt.join)
			s.mu.Unlock()
			var reservationErr *groupReservationError
			isReservation := errors.As(err, &reservationErr)
			switch {
			case tt.wantSealed:
				if !isReservation || !reservationErr.sealed {
					t.Fatalf("err = %v, want sealed reservation error", err)
				}
			case tt.wantParse:
				if err == nil || isReservation {
					t.Fatalf("err = %v, want non-reservation parse error", err)
				}
			default:
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			}
		})
	}
}

func TestGroupScopeStateStaysBoundedAcrossBatches(t *testing.T) {
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1})
	scope := s.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	for n := uint64(1); n <= 500; n++ {
		s.SealGroupBatch(scope, testBatchID(n))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.scopes[scope]
	if state.sealedThrough != 500 {
		t.Fatalf("sealedThrough = %d, want 500", state.sealedThrough)
	}
	if len(state.names) != 0 {
		t.Fatalf("sealing retained %d names", len(state.names))
	}
}

func TestSealingOneScopeLeavesOtherScopesOpen(t *testing.T) {
	s := NewSupervisor(SupervisorOptions{MaxParallel: 1})
	sealed := s.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	other := s.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	s.SealGroupBatch(sealed, testBatchID(10))

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reserveGroupLocked(other, "g", testBatchID(3)); err != nil {
		t.Fatalf("join in other stream's open batch = %v, want nil", err)
	}
	var reservationErr *groupReservationError
	if err := s.reserveGroupLocked(sealed, "h", testBatchID(3)); !errors.As(err, &reservationErr) || !reservationErr.sealed {
		t.Fatalf("join in sealed stream = %v, want sealed reservation error", err)
	}
}
