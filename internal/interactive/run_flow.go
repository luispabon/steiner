package interactive

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

// submitPrompt handles a user-submitted prompt during an interactive session.
// It records history, computes the skill blocks and, for a session's first
// prompt, the title, then queues the prompt on the conversation driver, which
// owns the conversation and starts the run.
func (s *Session) submitPrompt(ctx context.Context, text string, images []agent.ImageBlock) {
	s.recordHistory(text)

	// Skill content is injected only into the newly appended user message;
	// existing conversation messages are never rewritten.
	blocks := s.skillDeltaBlocks(ctx, s.Conversation())

	s.mu.Lock()
	drv := s.driver.drv
	selectionHook := s.submitSelectionHook
	s.mu.Unlock()
	if selectionHook != nil {
		selectionHook()
	}

	s.mu.Lock()
	if s.driver.drv != drv {
		s.mu.Unlock()
		return
	}
	if s.deps.SessionStore != nil && len(s.conversation) == 0 && s.sessionTitle == "" {
		s.sessionTitle = session.TitleFromPrompt(text)
	}
	s.driverAdmissions++
	admissionHook := s.submitAdmissionHook
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.driverAdmissions--
		s.mu.Unlock()
	}()
	drv.Submit(text, images, agent.SubmitMeta{SkillBlocks: blocks})
	if admissionHook != nil {
		admissionHook()
	}
}

// runSessionMeta is the identity metadata of a session, captured when a driver
// is retired so its last run can still be saved after the session moved on.
type runSessionMeta struct {
	id       string
	cacheKey string
	group    string
	title    string
	mode     string
	skills   []string
	modelID  string
}

// sessionMetaLocked captures the live session identity. The caller must hold s.mu.
func (s *Session) sessionMetaLocked() runSessionMeta {
	return runSessionMeta{
		id:       s.sessionID,
		cacheKey: s.promptCacheKey,
		group:    s.sessionGroup,
		title:    s.sessionTitle,
		mode:     string(s.mode),
		skills:   s.skills.Snapshot(),
		modelID:  currentModelConfig(s.deps.Config).ID,
	}
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
