package interactive

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

// driverHandle ties one ConversationDriver to the session identity it serves.
type driverHandle struct {
	drv *agent.ConversationDriver
	// retired is set under Session.mu when the session moves to another
	// conversation. From then on the driver saves under that identity and
	// leaves the live session state alone.
	retired *runSessionMeta
	// lastSaved is the last snapshot the driver saved, initially its seed. Only
	// the driver's loop goroutine touches it after construction.
	lastSaved *agent.DriverSnapshot
}

// newDriverLocked builds and starts a driver over conv and lineage. The caller
// stores the handle in s.driver.
func (s *Session) newDriverLocked(conv []agent.Message, lineage agent.ConversationLineage) *driverHandle {
	h := &driverHandle{lastSaved: &agent.DriverSnapshot{Conversation: conv, Lineage: lineage, Ledger: s.ledger}}
	h.drv = agent.NewConversationDriver(agent.DriverOptions{
		Run:                 s.driverRun,
		Background:          s.deps.Background,
		Steers:              s.steersForNewDriverLocked(),
		Save:                s.driverSave(h),
		Events:              s.events,
		PrepareTurn:         s.prepareTurn,
		Clock:               s.deps.Clock,
		MaxTokensPerEpisode: s.deps.MaxTokensPerEpisode,
	}, conv, lineage)
	if s.deps.SetCompletionSink != nil {
		s.deps.SetCompletionSink(h.drv)
	}
	h.drv.Start(context.Background())
	return h
}

// swapDriverLocked replaces the driver after apply has moved the session to a
// new conversation or identity, and returns the old handle for retireDriver.
// The old driver's identity is captured before apply runs, so a run still in
// flight saves under the session it started in. The caller must hold s.mu and
// pass replacementGuardLocked before swapping. The caller must call retireDriver after releasing it.
func (s *Session) swapDriverLocked(apply func()) *driverHandle {
	old := s.driver
	meta := s.sessionMetaLocked()
	old.retired = &meta
	// The steer queue belongs to the live session: without this the old driver
	// would drain steers meant for its successor, or for a oneshot run.
	old.drv.DetachSteers()
	s.ledger = nil
	apply()
	s.driver = s.newDriverLocked(s.conversation, s.lineage)
	return old
}

// retireDriver stops a replaced driver. A run in flight is not cancelled: it
// finishes, its result is saved under the old session, and then the driver
// closes. The caller must not hold s.mu, because Close saves.
func (s *Session) retireDriver(old *driverHandle) {
	if old == nil {
		return
	}
	if !old.drv.Busy() {
		old.drv.Close(context.Background())
		return
	}
	s.runs.Add(1)
	go func() {
		defer s.runs.Done()
		// Only its own run matters: the session-wide sub-agents belong to the
		// live driver.
		_ = old.drv.WaitIdle(context.Background())
		old.drv.Close(context.Background())
	}()
}

// currentDriver returns the live driver. Callers invoke its methods without
// holding s.mu: driver calls can emit events, and a sink may re-enter the session.
func (s *Session) currentDriver() *agent.ConversationDriver {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.driver.drv
}

// replacementGuardLocked checks whether a driver replacement may proceed. The
// caller must hold s.mu and keep it held through the swap.
func (s *Session) replacementGuardLocked(action string, refuseRun bool) error {
	if s.driverAdmissions > 0 {
		return fmt.Errorf("%s: %w", action, errRunInProgress)
	}
	if refuseRun && s.driver.drv.Busy() {
		return fmt.Errorf("%s: %w", action, errRunInProgress)
	}
	return s.pendingRefusalLocked(action)
}

// driverBusyLocked reports whether the live driver has a run, compaction or
// queued prompt. The caller must hold s.mu.
func (s *Session) driverBusyLocked() bool {
	return s.driverAdmissions > 0 || s.driver.drv.Busy()
}

// driverSave persists a driver snapshot. A live driver refreshes the session's
// in-memory view and saves the current session; a retired one saves under the
// identity it was retired with and leaves memory untouched.
func (s *Session) driverSave(h *driverHandle) func(context.Context, agent.DriverSnapshot) error {
	return func(_ context.Context, snap agent.DriverSnapshot) error {
		s.mu.Lock()
		live := s.driver == h
		retired := h.retired
		if live {
			s.conversation = snap.Conversation
			s.lineage = snap.Lineage
			s.ledger = snap.Ledger
		}
		s.mu.Unlock()

		previous := h.lastSaved
		h.lastSaved = &snap
		if snapshotUnchanged(previous, snap) {
			return nil
		}
		switch {
		case live:
			return s.saveLive()
		case retired != nil:
			return s.saveSnapshotAs(*retired, snap.Lineage)
		}
		return nil
	}
}

// saveLive saves the current session and reports a failure as a session-health
// warning; a lost save must not stop the conversation.
func (s *Session) saveLive() error {
	if s.deps.SessionStore == nil {
		return nil
	}
	if err := s.saveSession(); err != nil {
		s.emitSaveWarning(err)
	}
	return nil
}

func (s *Session) emitSaveWarning(err error) {
	s.events.Emit(output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "session_health",
		Severity: "warning",
		Notes:    []string{fmt.Sprintf("save session: %v", err)},
	}))
}

// snapshotUnchanged reports whether snap equals the last saved snapshot. The
// driver saves on every transition, including a Close over an untouched
// session, and those must not rewrite the stored session (or create an empty
// one).
func snapshotUnchanged(previous *agent.DriverSnapshot, snap agent.DriverSnapshot) bool {
	if previous == nil {
		return false
	}
	if len(previous.Conversation) == 0 && len(snap.Conversation) == 0 && previous.Lineage.Empty() && snap.Lineage.Empty() {
		return true
	}
	return reflect.DeepEqual(previous.Conversation, snap.Conversation) &&
		reflect.DeepEqual(previous.Lineage, snap.Lineage) &&
		slices.Equal(previous.Ledger, snap.Ledger)
}

// saveSnapshotAs persists lineage under a session identity that is no longer
// live, warning on failure.
func (s *Session) saveSnapshotAs(meta runSessionMeta, lineage agent.ConversationLineage) error {
	if s.deps.SessionStore == nil {
		return nil
	}
	if err := s.writeLineageAs(meta, lineage); err != nil {
		s.emitSaveWarning(err)
	}
	return nil
}

func (s *Session) writeLineageAs(meta runSessionMeta, lineage agent.ConversationLineage) error {
	var sess session.Session
	if existing, err := s.deps.SessionStore.Load(meta.id); err == nil {
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
		sess.ID = meta.id
		sess.PromptCacheKey = meta.cacheKey
		if meta.title != "" {
			sess = sess.WithTitle(meta.title)
		}
	}
	sess.Mode = meta.mode
	sess.Skills = meta.skills
	return s.deps.SessionStore.Save(sess)
}

// Close stops the session's conversation driver: the current run is cancelled,
// buffered items are settled into the conversation and the result is saved. It
// returns when done or when ctx ends.
func (s *Session) Close(ctx context.Context) {
	s.currentDriver().Close(ctx)
}
