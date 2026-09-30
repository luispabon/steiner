package interactive

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
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
	sess.SubAgentLedger = slices.Clone(s.ledger)
	if s.sessionTitle != "" {
		sess = sess.WithTitle(s.sessionTitle)
	}

	return s.deps.SessionStore.Save(sess)
}

// rotateSession assigns a fresh session identity and optionally updates the group.
func (s *Session) rotateSession(group string, updateGroup bool) error {
	s.mu.Lock()
	if err := s.replacementGuardLocked("rotate session", false, true); err != nil {
		s.mu.Unlock()
		return s.reportReplacementGuardError("rotate session", err)
	}
	if s.deps.SessionStore == nil {
		s.mu.Unlock()
		return nil
	}

	id, err := generateSessionID()
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("rotate session id: %w", err)
	}
	old := s.swapDriverLocked(func() { s.applyRotationLocked(id, group, updateGroup) })
	s.mu.Unlock()
	s.retireDriver(old)
	return nil
}

// applyRotationLocked updates identity metadata after replacement has passed
// its guard and the new driver swap is in progress.
func (s *Session) applyRotationLocked(id, group string, updateGroup bool) {
	s.sessionID = id
	s.promptCacheKey = id
	s.bindImageStore(id, 1)
	s.sessionDate = prompt.NewSessionDate(s.now())
	s.sessionTitle = ""
	if updateGroup {
		s.sessionGroup = strings.TrimSpace(group)
	}
}

// resolveFallbackContextWindow resolves the current model's context window
// through s.deps.ResolveModel when it isn't statically configured, returning
// 0 when ResolveModel is unset or resolution fails.
func (s *Session) resolveFallbackContextWindow() int {
	if s.deps.ResolveModel == nil {
		return 0
	}
	rm, err := s.deps.ResolveModel(s.CurrentModelAlias())
	if err != nil {
		return 0
	}
	return rm.EffectiveLimits.ContextWindow
}

// refuseRunInProgress surfaces errRunInProgress as an overlay notice and error.
func (s *Session) refuseRunInProgress(action string) error {
	s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("%s: %s", action, errRunInProgress)))
	return fmt.Errorf("%s: %w", action, errRunInProgress)
}

// refuseWhilePending refuses a session-replacing action while sub-agents are
// still running, surfacing the reason as an overlay notice and error. Callers
// that also refuse during a run check that separately: clear and rotate stay
// allowed mid-run because a workflow handoff rotates from inside one.
func (s *Session) loadSessionGuardLocked() error {
	return s.replacementGuardLocked("load session", true, true)
}

func (s *Session) reportReplacementGuardError(action string, err error) error {
	if errors.Is(err, errRunInProgress) {
		return s.refuseRunInProgress(action)
	}
	s.events.Emit(output.NewOverlayReportEvent("Context Report", err.Error()))
	return err
}

func (s *Session) pendingRefusalLocked(action string) error {
	if s.deps.Background == nil {
		return nil
	}
	pending := s.deps.Background.Pending()
	if len(pending) == 0 {
		return nil
	}
	msg := fmt.Sprintf("%d sub-agents still running; wait for them or stop them first", len(pending))
	return fmt.Errorf("%s: %s", action, msg)
}

func (s *Session) refuseWhilePending(action string) error {
	err := s.pendingRefusalLocked(action)
	if err != nil {
		s.events.Emit(output.NewOverlayReportEvent("Context Report", err.Error()))
	}
	return err
}

func (s *Session) reportLoadGuardError(err error) error {
	if errors.Is(err, errRunInProgress) {
		return s.refuseRunInProgress("load session")
	}
	s.events.Emit(output.NewOverlayReportEvent("Context Report", err.Error()))
	return err
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
	if err := s.loadSessionGuardLocked(); err != nil {
		s.mu.Unlock()
		return s.reportLoadGuardError(err)
	}
	old := s.swapDriverLocked(func() {
		s.lineage = sess.Lineage
		s.conversation = sess.Lineage.FullMessages()
		s.sessionID = sess.ID
		s.promptCacheKey = sess.CacheKey()
		s.sessionDate = prompt.NewSessionDate(s.now())
		s.sessionTitle = sess.Title
		s.sessionGroup = strings.TrimSpace(sess.Group)
		s.mode = mode
		s.ledger = slices.Clone(sess.SubAgentLedger)
		s.skills.Reset()
		for _, name := range sess.Skills {
			s.skills.Set(name, true)
		}
	})
	listener := s.modeListener
	msgs := append([]agent.Message(nil), s.conversation...)
	drv := s.driver.drv
	s.mu.Unlock()
	s.retireDriver(old)

	deliverLostSubAgents(drv, sess.SubAgentLedger)

	s.bindImageStore(sess.ID, agent.NextImageIDFloor(sess.Lineage))

	// Notify after releasing the lock: the listener is caller-supplied and may
	// re-enter the session.
	if listener != nil {
		listener(mode)
	}

	s.replaySessionMessages(msgs)

	// Emit a context-diagnostics event so the TUI can populate the sidebar
	// token bar and the status bar with the model's context budget.
	currentModel := currentModelConfig(s.deps.Config)
	contextWindow := currentModel.Advanced.Limits.ContextWindow
	if contextWindow <= 0 {
		contextWindow = s.resolveFallbackContextWindow()
	}
	var promptTokens int
	var estimateFailures int
	var lastEstimateErr error
	for _, msg := range msgs {
		t, err := provider.EstimateMessageTokens(ctx, currentModel.ID, provider.Message{
			Role:    provider.MessageRole(msg.Role),
			Content: msg.Content,
		})
		if err != nil {
			// Fall back to a flat per-message estimate so the context-window
			// gauge stays approximate rather than failing the turn.
			estimateFailures++
			lastEstimateErr = err
			promptTokens += 4
		} else {
			promptTokens += t
		}
	}
	if estimateFailures > 0 {
		// A systematic tokenizer failure (e.g. an unsupported model ID) would
		// otherwise warn once per message on every session load; report the
		// count once instead.
		slog.Warn("estimate message tokens", "failures", estimateFailures, "total", len(msgs), "last_error", lastEstimateErr)
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

// deliverLostSubAgents records each ledger entry of a loaded session as lost:
// the previous process's sub-agents are gone. The driver settles them without
// a run and saves with the supervisor's (empty) ledger, clearing the stored one.
func deliverLostSubAgents(drv *agent.ConversationDriver, ledger []agent.SubAgentLedgerEntry) {
	if len(ledger) == 0 {
		return
	}
	lost := make([]agent.SubAgentCompletion, len(ledger))
	for i, entry := range ledger {
		lost[i] = agent.LostSubAgentCompletion(entry)
	}
	drv.DeliverCompletions(lost)
}

// bindImageStore scopes the image store to sessionID, emitting a non-fatal
// warning through the session event sink when binding fails.
func (s *Session) bindImageStore(sessionID string, minNext int) {
	if s.deps.ImageStore == nil {
		return
	}
	if err := s.deps.ImageStore.BindSession(sessionID, minNext); err != nil {
		s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Notes:    []string{fmt.Sprintf("bind image store: %v", err)},
		}))
	}
}

// copySessionImages copies the source session's image folder to the fork,
// emitting a non-fatal warning through the session event sink when the copy
// fails.
func (s *Session) copySessionImages(fromID, toID string) {
	if s.deps.ImageStore == nil {
		return
	}
	if err := s.deps.ImageStore.CopySession(fromID, toID); err != nil {
		s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Notes:    []string{fmt.Sprintf("copy session images: %v", err)},
		}))
	}
}

// handleForkSession forks the current live session after saving it, then switches to the fork.
func (s *Session) handleForkSession(ctx context.Context) error {
	if s.runActive() {
		return s.refuseRunInProgress("fork session")
	}
	if err := s.refuseWhilePending("fork session"); err != nil {
		return err
	}
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
		Mode:           string(s.mode),
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

	s.copySessionImages(currentSession.ID, forked.ID)

	s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("Forked from: %s", originalTitle)))
	return s.loadSession(ctx, forked.ID)
}

// handleForkSavedSession forks a saved session by ID, saves the fork, then switches to it.
func (s *Session) handleForkSavedSession(ctx context.Context, sessionID string) error {
	if s.runActive() {
		return s.refuseRunInProgress("fork saved session")
	}
	if err := s.refuseWhilePending("fork saved session"); err != nil {
		return err
	}
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

	s.copySessionImages(loadedSession.ID, forked.ID)

	s.events.Emit(output.NewOverlayReportEvent("Context Report", fmt.Sprintf("Forked from: %s", loadedSession.Title)))
	return s.loadSession(ctx, forked.ID)
}
