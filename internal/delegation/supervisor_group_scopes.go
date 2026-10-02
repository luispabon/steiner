package delegation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
)

type delegationGroupScope struct {
	names    map[string]string
	batches  map[string]bool
	released bool
}

// NewGroupScope creates a runtime scope seeded with durable reserved names.
func (s *Supervisor) NewGroupScope(seed agent.DelegationGroupLedger) string {
	if err := seed.Validate(); err != nil {
		panic(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopeSeq++
	id := fmt.Sprintf("scope-%d", s.scopeSeq)
	state := &delegationGroupScope{names: make(map[string]string), batches: make(map[string]bool)}
	for _, name := range seed.Names {
		state.names[name] = ""
	}
	s.scopes[id] = state
	return id
}

// SnapshotGroupLedger returns a sorted, independent view of the scope names.
func (s *Supervisor) SnapshotGroupLedger(scope string) agent.DelegationGroupLedger {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := agent.DelegationGroupLedger{Version: 1}
	if state := s.scopes[scope]; state != nil {
		for name := range state.names {
			result.Names = append(result.Names, name)
		}
	}
	sort.Strings(result.Names)
	return result
}

// SealGroupBatch closes membership for one named tool batch in the scope.
func (s *Supervisor) SealGroupBatch(scope, batchID string) {
	s.mu.Lock()
	state := s.scopes[scope]
	if state != nil {
		state.batches[batchID] = true
	}
	s.mu.Unlock()
	s.sealBatch(scope, batchID)
}

// ReleaseGroupScope marks a scope released and removes it after its jobs finish.
func (s *Supervisor) ReleaseGroupScope(scope string) {
	s.mu.Lock()
	if state := s.scopes[scope]; state != nil {
		state.released = true
		s.maybeDeleteScopeLocked(scope)
	}
	s.mu.Unlock()
}

func (s *Supervisor) maybeDeleteScopeLocked(scope string) {
	state := s.scopes[scope]
	if state == nil || !state.released {
		return
	}
	for _, job := range s.jobs {
		if job.job.GroupScope == scope {
			return
		}
	}
	delete(s.scopes, scope)
}

func normalizeGroup(name string) string { return strings.TrimSpace(name) }

type groupReservationError struct {
	name   string
	batch  string
	sealed bool
}

func (e *groupReservationError) Error() string {
	if e.sealed {
		return fmt.Sprintf("delegation group batch %q is sealed; use a fresh group name", e.batch)
	}
	return fmt.Sprintf("delegation group name %q was already used; choose a fresh name", e.name)
}

func (e *groupReservationError) correctiveReason() string {
	if e.sealed {
		return fmt.Sprintf("delegation group name %q cannot join sealed batch; use a fresh group name", e.name)
	}
	return e.Error()
}

func (s *Supervisor) reserveGroupLocked(scope, name, batch string) error {
	name = normalizeGroup(name)
	if name == "" {
		return nil
	}
	if batch == "" {
		return fmt.Errorf("named delegation group requires a tool batch")
	}
	state := s.scopes[scope]
	if state == nil {
		return fmt.Errorf("unknown delegation group scope %q", scope)
	}
	if state.batches[batch] {
		return &groupReservationError{name: name, batch: batch, sealed: true}
	}
	if owner, exists := state.names[name]; exists {
		if owner == batch {
			return nil
		}
		return &groupReservationError{name: name, batch: batch}
	}
	state.names[name] = batch
	return nil
}
