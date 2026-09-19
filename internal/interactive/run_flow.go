package interactive

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

// submitPrompt handles a user-submitted prompt during an interactive session.
// It records history on submit, appends the user message, starts a
// cancellable model run, updates session lineage and title, and saves the
// session. Emits stop/error and history events consistently.
func (s *Session) submitPrompt(ctx context.Context, text string, images []agent.ImageBlock) {
	s.recordHistory(text)

	s.mu.Lock()
	startID := s.sessionID
	startMeta := runSessionMeta{
		cacheKey: s.promptCacheKey,
		group:    s.sessionGroup,
		title:    s.sessionTitle,
		mode:     string(s.mode),
		skills:   s.skills.Snapshot(),
		modelID:  currentModelConfig(s.deps.Config).ID,
	}
	isFirstPrompt := len(s.conversation) == 0
	s.conversation = append(s.conversation, agent.Message{Role: agent.MessageRoleUser, Content: text, Images: images})
	s.mu.Unlock()

	err := s.runWithInterruptOwnership(ctx, func(runCtx context.Context) error {
		drainSteers := s.runController.SteerQueue().Drain
		conversation := s.Conversation()
		notice := s.modeNotice()
		if notice != "" && len(conversation) > 0 && conversation[len(conversation)-1].Role == agent.MessageRoleUser {
			conversation[len(conversation)-1].Content = notice + conversation[len(conversation)-1].Content
		}
		runner := s.currentRunner()
		result, err := runner.Run(runCtx, conversation, s.skills.Snapshot(), drainSteers)

		if !s.applyRunResult(startID, result) {
			s.saveOrphanedRunResult(startID, startMeta, text, isFirstPrompt, result)
		}

		if err != nil {
			return err
		}
		return nil
	})

	if s.sessionChanged(startID) {
		slog.Warn("session changed during run; result saved under its original session", "run_session", startID)
		if err != nil {
			s.events.Emit(output.NewStopReasonEvent(0, fmt.Sprintf("Error: %v", err), err))
		}
		return
	}

	if isFirstPrompt && s.deps.SessionStore != nil {
		s.mu.Lock()
		s.sessionTitle = session.TitleFromPrompt(text)
		s.mu.Unlock()
	}

	if s.deps.SessionStore != nil {
		if saveErr := s.saveSession(); saveErr != nil {
			s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
				Kind:     "session_health",
				Severity: "warning",
				Notes:    []string{fmt.Sprintf("save session: %v", saveErr)},
			}))
		}
	}

	if err != nil {
		s.events.Emit(output.NewStopReasonEvent(0, fmt.Sprintf("Error: %v", err), err))
		return
	}
}

// applyRunResult writes a finished run's conversation back into the session,
// unless the session identity changed since the run started, in which case the
// result belongs to a different session and is left out of memory (the caller
// saves it under its original ID via saveOrphanedRunResult).
func (s *Session) applyRunResult(startID string, result RunResult) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessionID != startID {
		slog.Warn("session changed during run; discarding result", "run_session", startID, "current_session", s.sessionID)
		return false
	}
	if len(result.Conversation) > 0 {
		if result.WorkflowHandoff == nil {
			s.conversation = result.Conversation
		}
		s.lineage = lineageFromResult(result)
	}
	return true
}

func lineageFromResult(result RunResult) agent.ConversationLineage {
	return agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{ID: 1, SummaryPrefix: nil, Messages: cloneMessages(result.Conversation)},
		},
		NextGenerationID: 2,
	}
}

// runSessionMeta is the identity metadata of the session a run started in,
// captured so the run's result can still be saved after the session rotated.
type runSessionMeta struct {
	cacheKey string
	group    string
	title    string
	mode     string
	skills   []string
	modelID  string
}

// saveOrphanedRunResult persists a finished run's conversation under startID
// when the live session moved on mid-run (e.g. a workflow handoff cleared and
// rotated it). In-memory state is left untouched.
func (s *Session) saveOrphanedRunResult(startID string, meta runSessionMeta, prompt string, isFirstPrompt bool, result RunResult) {
	if s.deps.SessionStore == nil || len(result.Conversation) == 0 {
		return
	}
	if err := s.saveRunResultAs(startID, meta, prompt, isFirstPrompt, result); err != nil {
		slog.Warn("save orphaned run result", "run_session", startID, "error", err)
		s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Notes:    []string{fmt.Sprintf("save session: %v", err)},
		}))
	}
}

func (s *Session) saveRunResultAs(id string, meta runSessionMeta, prompt string, isFirstPrompt bool, result RunResult) error {
	lineage := lineageFromResult(result)
	var sess session.Session
	if existing, err := s.deps.SessionStore.Load(id); err == nil {
		sess = existing.WithLineage(lineage)
	} else {
		var newErr error
		if meta.group != "" {
			sess, newErr = session.NewSession(meta.modelID, lineage, meta.group)
		} else {
			sess, newErr = session.NewSession(meta.modelID, lineage)
		}
		if newErr != nil {
			return fmt.Errorf("create session: %w", newErr)
		}
		sess.ID = id
		sess.PromptCacheKey = meta.cacheKey
		title := meta.title
		if title == "" && isFirstPrompt {
			title = prompt
		}
		if title != "" {
			sess = sess.WithTitle(title)
		}
	}
	sess.Mode = meta.mode
	sess.Skills = meta.skills
	return s.deps.SessionStore.Save(sess)
}

func (s *Session) sessionChanged(startID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID != startID
}

// recordHistory persists text to prompt history and publishes the refreshed
// list so the TUI can recall it immediately.
func (s *Session) recordHistory(text string) {
	if s.deps.HistoryWriter == nil {
		return
	}
	if err := s.deps.HistoryWriter.Record(text); err != nil {
		s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Notes:    []string{fmt.Sprintf("history record: %v", err)},
		}))
	}
	prompts, err := s.deps.HistoryWriter.Load()
	if err != nil {
		s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
			Kind:     "session_health",
			Severity: "warning",
			Notes:    []string{fmt.Sprintf("history load: %v", err)},
		}))
		prompts = nil
	}
	s.events.Emit(output.NewHistoryLoadedEvent(prompts))
}

// cloneMessages returns a deep copy of a message slice.
func cloneMessages(messages []agent.Message) []agent.Message {
	return agent.CloneMessages(messages)
}

// runWithInterruptOwnership executes a run function with a cancellable context
// and manages the lifecycle of the run controller. It creates a derived context,
// registers the cancel function, runs the function, and ensures cleanup via
// cancel and controller clear.
func (s *Session) runWithInterruptOwnership(ctx context.Context, run func(context.Context) error) error {
	runCtx, cancel := context.WithCancel(ctx)
	token := s.runController.Set(cancel)
	defer func() {
		cancel()
		s.runController.Clear(token)
	}()
	return run(runCtx)
}
