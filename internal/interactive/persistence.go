package interactive

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
)

// generateSessionID creates a random hex ID using crypto/rand.
func generateSessionID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return fmt.Sprintf("%032x", b), nil
}

// LoadSessionByID loads a saved session with the given ID, replacing the current
// conversation with the restored lineage.
func (s *Session) LoadSessionByID(ctx context.Context, sessionID string) error {
	return s.loadSession(ctx, sessionID)
}

// saveSession saves the current session state to disk with the current title and lineage.
func (s *Session) saveSession() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.deps.SessionStore == nil {
		return nil
	}

	modelID := currentModelConfig(s.deps.Config).ID
	var (
		sess session.Session
		err  error
	)
	if s.sessionGroup != "" {
		sess, err = session.NewSession(modelID, s.lineage, s.sessionGroup)
	} else {
		sess, err = session.NewSession(modelID, s.lineage)
	}
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	sess.ID = s.sessionID
	sess.PromptCacheKey = s.promptCacheKey
	sess.Mode = string(s.mode)
	sess.Skills = s.skills.Snapshot()
	if s.sessionTitle != "" {
		sess = sess.WithTitle(s.sessionTitle)
	}

	return s.deps.SessionStore.Save(sess)
}

// rotateSession assigns a fresh session identity and optionally updates the group.
func (s *Session) rotateSession(group string, updateGroup bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deps.SessionStore == nil {
		return nil
	}

	id, err := generateSessionID()
	if err != nil {
		return fmt.Errorf("rotate session id: %w", err)
	}
	s.sessionID = id
	s.promptCacheKey = id
	s.sessionTitle = ""
	if updateGroup {
		s.sessionGroup = strings.TrimSpace(group)
	}
	return nil
}

// loadSession replaces the current conversation and lineage with a previously
// saved session, following the ClearConversation pattern but seeding from stored lineage.
func (s *Session) loadSession(ctx context.Context, sessionID string) error {
	if s.deps.SessionStore == nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", "session store not configured"))
		return nil
	}

	sess, err := s.deps.SessionStore.Load(sessionID)
	if err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("load session failed: %v", err)))
		return err
	}

	// Validate the persisted mode before touching session state: empty falls
	// back to the configured default, plan/build are accepted, and any other
	// value must not restore a writable session.
	mode := config.ExecutionMode(strings.TrimSpace(sess.Mode))
	if mode != "" && mode != config.ExecutionModePlan && mode != config.ExecutionModeBuild {
		err := fmt.Errorf("load session: unknown mode %q", mode)
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("load session failed: %v", err)))
		return err
	}
	if mode == "" {
		mode = s.deps.Config.Modes.Default
	}

	s.mu.Lock()
	s.lineage = sess.Lineage
	s.conversation = sess.Lineage.FullMessages()
	s.sessionID = sess.ID
	s.promptCacheKey = sess.CacheKey()
	s.sessionTitle = sess.Title
	s.sessionGroup = strings.TrimSpace(sess.Group)
	s.mode = mode
	if s.modeListener != nil {
		s.modeListener(mode)
	}
	s.skills.Reset()
	for _, name := range sess.Skills {
		s.skills.Set(name, true)
	}
	msgs := append([]agent.Message(nil), s.conversation...)
	s.mu.Unlock()

	s.replaySessionMessages(msgs)

	// Emit a context-diagnostics event so the TUI can populate the sidebar
	// token bar and the status bar with the model's context budget.
	currentModel := currentModelConfig(s.deps.Config)
	contextWindow := currentModel.Advanced.Limits.ContextWindow
	if contextWindow <= 0 {
		if rm, err := provider.ResolveWithDiscovery(s.deps.Config, s.CurrentModelAlias(), s.deps.HTTPClient); err == nil {
			contextWindow = rm.EffectiveLimits.ContextWindow
		}
	}
	var promptTokens int
	for _, msg := range msgs {
		t, err := provider.EstimateMessageTokens(ctx, currentModel.ID, provider.Message{
			Role:    provider.MessageRole(msg.Role),
			Content: msg.Content,
		})
		if err != nil {
			_ = err
			promptTokens += 4
		} else {
			promptTokens += t
		}
	}
	turnCount := promptTokens / 2
	if turnCount < 1 {
		turnCount = 1
	}
	s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:                "session_loaded",
		ContextWindow:       contextWindow,
		ContextTokens:       contextWindow,
		PromptTokens:        promptTokens,
		ContextUsagePercent: usagePercent(promptTokens, contextWindow),
		Status:              sessionLoadedStatus(contextWindow),
		TotalTokens:         promptTokens,
		Turn:                turnCount,
	}))

	return nil
}

// handleForkSession forks the current live session after saving it, then switches to the fork.
func (s *Session) handleForkSession(ctx context.Context) error {
	if s.deps.SessionStore == nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", "session store not configured"))
		return nil
	}

	if err := s.saveSession(); err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("fork session: save failed: %v", err)))
		return err
	}

	s.mu.RLock()
	currentSession := session.Session{
		ID:             s.sessionID,
		Title:          s.sessionTitle,
		Model:          currentModelConfig(s.deps.Config).ID,
		Group:          s.sessionGroup,
		Lineage:        s.lineage,
		PromptCacheKey: s.promptCacheKey,
		Skills:         s.skills.Snapshot(),
	}
	originalTitle := s.sessionTitle
	s.mu.RUnlock()

	forked, err := session.Fork(currentSession)
	if err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("fork session: %v", err)))
		return err
	}

	if err := s.deps.SessionStore.Save(forked); err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("fork session: save fork failed: %v", err)))
		return err
	}

	s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("Forked from: %s", originalTitle)))
	return s.loadSession(ctx, forked.ID)
}

// handleForkSavedSession forks a saved session by ID, saves the fork, then switches to it.
func (s *Session) handleForkSavedSession(ctx context.Context, sessionID string) error {
	if s.deps.SessionStore == nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", "session store not configured"))
		return nil
	}

	loadedSession, err := s.deps.SessionStore.Load(sessionID)
	if err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("fork saved session failed: %v", err)))
		return err
	}

	forked, err := session.Fork(loadedSession)
	if err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("fork saved session: %v", err)))
		return err
	}

	if err := s.deps.SessionStore.Save(forked); err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("fork saved session: save failed: %v", err)))
		return err
	}

	s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("Forked from: %s", loadedSession.Title)))
	return s.loadSession(ctx, forked.ID)
}
