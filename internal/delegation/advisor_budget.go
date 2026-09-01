package delegation

import (
	"sync"

	"github.com/luispabon/steiner/internal/advisor"
)

// AdvisorBudgetStore provides concurrent-safe per-child advisor budget tracking.
// Each child agent gets its own SharedState, keyed by agent ID, so that
// follow_up resumptions of the same child reuse their original budget.
type AdvisorBudgetStore struct {
	mu     sync.Mutex
	states map[string]*advisor.SharedState
}

// NewAdvisorBudgetStore returns an initialized AdvisorBudgetStore.
func NewAdvisorBudgetStore() *AdvisorBudgetStore {
	return &AdvisorBudgetStore{
		states: make(map[string]*advisor.SharedState),
	}
}

// StateFor returns the SharedState for the given agent ID, creating one if it
// does not exist. The returned state is stable across multiple calls with the
// same agent ID, allowing budgets to survive follow_up resumptions.
func (s *AdvisorBudgetStore) StateFor(agentID string) *advisor.SharedState {
	s.mu.Lock()
	defer s.mu.Unlock()

	if state, ok := s.states[agentID]; ok {
		return state
	}
	state := advisor.NewSharedState()
	s.states[agentID] = state
	return state
}

// Reset clears all stored states. Call this on conversation boundaries
// (e.g. /new) to prevent cross-conversation budget bleed.
func (s *AdvisorBudgetStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.states = make(map[string]*advisor.SharedState)
}
