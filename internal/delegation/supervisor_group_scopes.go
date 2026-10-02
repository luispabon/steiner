package delegation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
)

type delegationGroupScope struct {
	names map[string]string
	// sealedThrough is the highest tool batch sequence sealed in this scope.
	// Batches in a scope run sequentially with increasing sequence numbers, so
	// one counter closes every batch up to it without per-batch state.
	sealedThrough uint64
	released      bool
}

// NewGroupScope creates a runtime scope seeded with durable reserved names.
func (s *Supervisor) NewGroupScope(seed agent.DelegationGroupLedger) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopeSeq++
	id := fmt.Sprintf("scope-%d", s.scopeSeq)
	state := &delegationGroupScope{names: make(map[string]string)}
	for _, name := range seed.Clone().Names {
		state.names[name] = ""
	}
	s.scopes[id] = state
	return id
}

// SnapshotGroupLedger returns a sorted, independent view of the scope names.
func (s *Supervisor) SnapshotGroupLedger(scope string) agent.DelegationGroupLedger {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion}
	if state := s.scopes[scope]; state != nil {
		for name := range state.names {
			result.Names = append(result.Names, name)
		}
	}
	sort.Strings(result.Names)
	return result
}

// SealGroupBatch closes membership for one named tool batch in the scope, and
// every earlier batch with it. A batch ID without a sequence number cannot
// advance the seal point; its groups still settle.
func (s *Supervisor) SealGroupBatch(scope, batchID string) {
	s.mu.Lock()
	if state := s.scopes[scope]; state != nil {
		state.sealThroughLocked(batchID)
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

// sealThroughLocked advances the seal point to batchID's sequence number. An
// unparseable ID is ignored: reserveGroupLocked rejects such batches anyway,
// and SealBatch has no error return to report it.
func (g *delegationGroupScope) sealThroughLocked(batchID string) {
	if seq, ok := agent.ToolBatchSeq(batchID); ok {
		g.sealedThrough = max(g.sealedThrough, seq)
	}
}

// groupInput returns the trimmed "group" string of a tool input, or "" when it
// is absent or not a string.
func groupInput(input map[string]any) string {
	group, _ := input["group"].(string)
	return NormalizeGroup(group)
}

// NormalizeGroup returns value with surrounding whitespace trimmed.
func NormalizeGroup(value string) string {
	return strings.TrimSpace(value)
}

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
	name = NormalizeGroup(name)
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
	seq, ok := agent.ToolBatchSeq(batch)
	if !ok {
		return fmt.Errorf("parse delegation group batch id %q: no sequence number", batch)
	}
	if seq <= state.sealedThrough {
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
