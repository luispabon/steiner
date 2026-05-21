package delegation

import (
	"sync"

	"github.com/luispabon/steiner/internal/agent"
)

// ChildSession tracks persisted state for a delegated child session.
type ChildSession struct {
	Spec          DelegationSpec
	Request       agent.RunRequest
	Conversation  []agent.Message
	TurnCount     int
	TokenCount    int
	ToolCallCount int
	FollowUpCount int
}

// SessionStore provides concurrent-safe access to child sessions by agent ID.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*ChildSession
}

// NewSessionStore returns an initialized SessionStore.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*ChildSession),
	}
}

// Save stores a full child session keyed by session.Spec.AgentID.
func (s *SessionStore) Save(session *ChildSession) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[session.Spec.AgentID] = session
}

// Get returns the stored child session for id.
func (s *SessionStore) Get(id string) (*ChildSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	return session, ok
}

// Update replaces the conversation and accumulates session usage counters.
func (s *SessionStore) Update(id string, conv []agent.Message, turns, tokens, toolCalls int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return
	}

	session.Conversation = conv
	session.TurnCount += turns
	session.TokenCount += tokens
	session.ToolCallCount += toolCalls
	session.FollowUpCount++
}
