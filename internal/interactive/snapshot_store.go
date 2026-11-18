package interactive

import (
	"sync"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// snapshotAgentLimit caps how many per-agent request snapshots a
// SnapshotStore retains. Older agents are evicted in last-write order.
const snapshotAgentLimit = 32

// SnapshotStore provides thread-safe storage for the latest parent request
// context snapshot during an interactive session, plus a retained snapshot per
// sub-agent.
type SnapshotStore struct {
	mu sync.RWMutex
	// parentSnapshot is the most recent unscoped parent (top-level
	// orchestrator) request. Child sub-agent requests never replace it, so
	// manual compaction can reuse the parent's tools and cache prefix even
	// when a child request was the most recent event overall.
	parentSnapshot *RequestContextSnapshot
	// agents holds the latest request snapshot per sub-agent ID. Stored
	// snapshots are immutable by convention; readers clone on return.
	agents map[string]*RequestContextSnapshot
	// agentOrder lists retained agent IDs in last-write order, oldest first.
	agentOrder []string
}

// cloneRequestContextSnapshot deep-copies the provider-owned slices and the
// MaxTokens pointer so a stored or returned snapshot never aliases caller
// state.
func cloneRequestContextSnapshot(snapshot RequestContextSnapshot) RequestContextSnapshot {
	return RequestContextSnapshot{
		Model:    snapshot.Model,
		Messages: provider.CloneMessages(snapshot.Messages),
		Tools:    provider.CloneTools(snapshot.Tools),
		MaxTokens: func() *int {
			if snapshot.MaxTokens == nil {
				return nil
			}
			cloned := *snapshot.MaxTokens
			return &cloned
		}(),
		Blocks:                append([]prompt.ContextBlock(nil), snapshot.Blocks...),
		ModelBudget:           snapshot.ModelBudget,
		AgentID:               snapshot.AgentID,
		AgentType:             snapshot.AgentType,
		Kind:                  snapshot.Kind,
		EstimatedPromptTokens: snapshot.EstimatedPromptTokens,
		RawPromptTokens:       snapshot.RawPromptTokens,
	}
}

// Store saves a deep copy of the given snapshot. An unscoped snapshot (empty
// AgentID) is also recorded as the latest parent request snapshot; a scoped
// child snapshot is also retained for its agent ID.
func (s *SnapshotStore) Store(snapshot RequestContextSnapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := cloneRequestContextSnapshot(snapshot)
	stored := &cloned
	if cloned.AgentID == "" {
		s.parentSnapshot = stored
		return
	}
	if s.agents == nil {
		s.agents = make(map[string]*RequestContextSnapshot)
	}
	s.evictAgentLocked(cloned.AgentID)
	s.agents[cloned.AgentID] = stored
	s.touchAgentLocked(cloned.AgentID)
}

// evictAgentLocked drops the oldest retained agent when a new agent ID would
// exceed the retention cap. The caller must hold s.mu.
func (s *SnapshotStore) evictAgentLocked(agentID string) {
	if _, exists := s.agents[agentID]; exists || len(s.agentOrder) < snapshotAgentLimit {
		return
	}
	oldest := s.agentOrder[0]
	s.agentOrder = s.agentOrder[1:]
	delete(s.agents, oldest)
}

// touchAgentLocked moves agentID to the end of the last-write order so an
// update never pins an old slot. The caller must hold s.mu.
func (s *SnapshotStore) touchAgentLocked(agentID string) {
	for i, id := range s.agentOrder {
		if id != agentID {
			continue
		}
		copy(s.agentOrder[i:], s.agentOrder[i+1:])
		s.agentOrder = s.agentOrder[:len(s.agentOrder)-1]
		break
	}
	s.agentOrder = append(s.agentOrder, agentID)
}

// ParentSnapshot returns the latest unscoped parent request snapshot if one
// exists, with a boolean indicating whether data was available. Child
// sub-agent requests never replace it.
func (s *SnapshotStore) ParentSnapshot() (RequestContextSnapshot, bool) {
	if s == nil {
		return RequestContextSnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.parentSnapshot == nil {
		return RequestContextSnapshot{}, false
	}
	return cloneRequestContextSnapshot(*s.parentSnapshot), true
}

// AgentSnapshot returns the retained snapshot for the given sub-agent ID if one
// exists, with a boolean indicating whether data was available. An empty or
// unknown ID reports false.
func (s *SnapshotStore) AgentSnapshot(agentID string) (RequestContextSnapshot, bool) {
	if s == nil || agentID == "" {
		return RequestContextSnapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, ok := s.agents[agentID]
	if !ok || snapshot == nil {
		return RequestContextSnapshot{}, false
	}
	return cloneRequestContextSnapshot(*snapshot), true
}

// Clear discards every stored snapshot, including the per-agent retention set.
func (s *SnapshotStore) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parentSnapshot = nil
	s.agents = nil
	s.agentOrder = nil
}
