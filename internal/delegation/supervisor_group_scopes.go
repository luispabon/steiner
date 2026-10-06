package delegation

import (
	"fmt"
	"sort"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

type delegationGroupScope struct {
	names map[string]string
	// sealedThrough is the highest tool batch sequence sealed in this scope.
	// Batches in a scope run sequentially with increasing sequence numbers, so
	// one counter closes every batch up to it without per-batch state.
	sealedThrough uint64
	released      bool
}

// newGroupScope creates a runtime scope seeded with durable reserved names.
func (s *Supervisor) newGroupScope(seed agent.DelegationGroupLedger) string {
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

// OpenGroupScope opens a scope seeded with durable reserved names for one
// sequential run stream and returns it with its release func. The stream seals
// only this scope; release is idempotent.
func (s *Supervisor) OpenGroupScope(seed agent.DelegationGroupLedger) (scope string, release func()) {
	scope = s.newGroupScope(seed)
	return scope, func() { s.releaseGroupScope(scope) }
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
// every earlier batch with it. An empty or unknown scope seals nothing. A
// batch ID without a sequence number cannot advance the seal point; its groups
// still settle.
func (s *Supervisor) SealGroupBatch(scope, batchID string) {
	if scope == "" {
		return
	}
	s.mu.Lock()
	if state := s.scopes[scope]; state != nil {
		state.sealThroughLocked(batchID)
	}
	s.mu.Unlock()
	s.sealBatch(scope, batchID)
}

// releaseGroupScope marks a scope released and removes it after its jobs finish.
func (s *Supervisor) releaseGroupScope(scope string) {
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
// and SealGroupBatch has no error return to report it.
func (g *delegationGroupScope) sealThroughLocked(batchID string) {
	if seq, ok := agent.ToolBatchSeq(batchID); ok {
		g.sealedThrough = max(g.sealedThrough, seq)
	}
}

// requireGroupScope rejects a named group when the handler has no run-stream
// group scope, before anything is enqueued or reserved. Ungrouped calls pass.
func requireGroupScope(deps SubAgentHandlerDeps, input map[string]any) error {
	if deps.GroupScope != "" {
		return nil
	}
	if group := groupInput(input); group != "" {
		return tool.WithModelGuidance(fmt.Errorf("group %q rejected: grouped delegation needs a run-stream group scope, which this session does not provide; omit group to delegate without grouping", group))
	}
	return nil
}

// groupInput returns the trimmed "group" string of a tool input, or "" when it
// is absent or not a string.
func groupInput(input map[string]any) string {
	group, _ := input["group"].(string)
	return agent.NormalizeDelegationGroup(group)
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
	name = agent.NormalizeDelegationGroup(name)
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
		return tool.WithModelGuidance(&groupReservationError{name: name, batch: batch, sealed: true})
	}
	if owner, exists := state.names[name]; exists {
		if owner == batch {
			return nil
		}
		return tool.WithModelGuidance(&groupReservationError{name: name, batch: batch})
	}
	state.names[name] = batch
	return nil
}
