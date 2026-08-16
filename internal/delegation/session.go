package delegation

import (
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/usagestats"
)

// SubAgentHandlerDeps holds dependencies shared by sub-agent tool handlers
// (specialized tools, vision, and follow_up).
type SubAgentHandlerDeps struct {
	Provider             provider.Provider
	ParentReg            *tool.Registry
	SubAgentCfg          config.SubAgentConfig
	Events               output.EventSink
	Runner               AgentRunner
	WorkDir              string
	HomeDir              string
	ProjectContextConfig config.ProjectContextConfig
	ResolvedModel        provider.ResolvedModel
	MaxTokens            *int
	StreamingPreferred   bool
	CaveHuman            bool
	TraceLogger          *TraceLogger
	SessionStore         *SessionStore
	// ExtraAllowedTools provides per-agent-type extra tool names included in
	// child registries beyond the built-in allowlists. Nil or empty map grants
	// no extra tools.
	ExtraAllowedTools map[AgentType][]string
	// Sandbox is the parent sandbox. Sub-agents inherit it unchanged so that
	// child sandbox permissions cannot exceed parent permissions.
	Sandbox tool.SandboxWrapper
	// UsageRecorder is the singleton recorder shared across the process for cache-hit-rate tracking.
	UsageRecorder *usagestats.Recorder
	// CacheKeyStore is the process-lifetime store reused across all agent-type
	// delegations in this session for prompt-cache key reuse. Nil disables key
	// reuse: each delegation mints a fresh key, matching pre-Fix-3 behavior.
	// Like SessionStore (whose Reset has zero production callers today), this
	// store is process-lifetime, not conversation-scoped: it is NOT reset on
	// /new. This is a deliberate accepted scope choice — a stale shard hint
	// costs at most a cache miss, never a correctness issue — not a bug.
	CacheKeyStore *CacheKeyStore
}

// ChildSession tracks persisted state for a delegated child session.
type ChildSession struct {
	Spec          DelegationSpec
	Request       agent.RunRequest
	Conversation  []agent.Message
	TurnCount     int
	TokenCount    int
	ToolCallCount int
	CacheUsage    CacheUsage
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

// SessionUpdateParams carries the deltas applied by SessionStore.Update.
type SessionUpdateParams struct {
	Conversation  []agent.Message
	TurnCount     int
	TokenCount    int
	ToolCallCount int
	CacheUsage    CacheUsage
}

// Update replaces the conversation and accumulates session usage counters.
func (s *SessionStore) Update(id string, params SessionUpdateParams) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[id]
	if !ok {
		return
	}

	session.Conversation = params.Conversation
	session.TurnCount += params.TurnCount
	session.TokenCount += params.TokenCount
	session.ToolCallCount += params.ToolCallCount
	session.CacheUsage = session.CacheUsage.Add(params.CacheUsage)
	session.FollowUpCount++
}

// Reset clears all stored sessions. Call this on conversation boundaries
// (e.g. /new) to prevent cross-conversation session bleed.
func (s *SessionStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions = make(map[string]*ChildSession)
}

// Count returns the number of stored sessions.
func (s *SessionStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.sessions)
}

func saveChildSession(store *SessionStore, spec DelegationSpec, req agent.RunRequest, state agent.RunState, cache CacheUsage) {
	store.Save(&ChildSession{
		Spec:          spec,
		Request:       req,
		Conversation:  state.Conversation,
		TurnCount:     state.TurnCount,
		TokenCount:    state.TokenCount,
		ToolCallCount: countToolCalls(state.Conversation),
		CacheUsage:    cache,
	})
}
