package lsp

import (
	"context"
	"time"
)

// readinessState is the lifecycle state of a server's readiness.
type readinessState int

const (
	readinessNotReady readinessState = iota
	readinessReady
	readinessTimedOut
)

// readiness tracks the workspace-load state of a server session.
// Readiness is determined once per session: when at least one begin/end
// progress cycle completes, when ReadyGracePeriod elapses without any begin,
// or when ReadyTimeout elapses with an incomplete begin/end cycle.
// The state is read-only after readyCh closes.
// State transitions are owned exclusively by trackReadiness; awaitReadySession
// is a passive observer.
type readiness struct {
	state        readinessState
	readyCh      chan struct{}
	openTokens   map[string]struct{}
	completedOne bool
}

// newReadiness creates a readiness tracker for a newly-spawned session.
func newReadiness() *readiness {
	return &readiness{
		state:      readinessNotReady,
		readyCh:    make(chan struct{}),
		openTokens: make(map[string]struct{}),
	}
}

// markReady transitions the state to ready and closes the ready channel.
// It is safe to call multiple times; only the first call takes effect.
func (r *readiness) markReady() {
	if r.state == readinessNotReady {
		r.state = readinessReady
		close(r.readyCh)
	}
}

// markTimedOut transitions the state to timedOut and closes the ready channel.
// It is safe to call multiple times; only the first call takes effect.
func (r *readiness) markTimedOut() {
	if r.state == readinessNotReady {
		r.state = readinessTimedOut
		close(r.readyCh)
	}
}

// isTerminal reports whether readiness has been determined.
func (r *readiness) isTerminal() bool {
	return r.state != readinessNotReady
}

// awaitReadySession blocks until the leased session's readiness is determined,
// timeout expires, or the caller's context is cancelled. It returns immediately
// if readiness is already determined.
//
// r must be the tracker the lease captured with sess. Reading entry.readiness or
// entry.session here would be wrong: a later respawn replaces the tracker, and a
// concurrent crashed-session retirement clears the session while leaving the dead
// session's readiness installed.
//
// If ReadyTimeout expires before a complete begin/end cycle, awaitReadySession
// returns incomplete=true with err=nil, allowing the request to proceed anyway.
// If the server exits while notReady, it returns the server's exit error.
// Cancellation of ctx returns ctx.Err().
//
// Navigation, hover, and symbol queries await readiness; diagnostics does not.
func (m *Manager) awaitReadySession(ctx context.Context, r *readiness, sess session) (incomplete bool, err error) {
	if r == nil || sess == nil {
		return false, nil
	}

	select {
	case <-r.readyCh:
		// markReady and markTimedOut set the state before closing readyCh, so
		// receiving from it orders this read.
		return r.state == readinessTimedOut, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-sess.Exited():
		return false, errServerExited
	case <-m.mgrCtx.Done():
		return false, m.mgrCtx.Err()
	}
}

func processReadinessProgress(e *entry, r *readiness, event ProgressEvent, gracePeriodFired *bool, gracePeriodTimer *time.Timer) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch event.Kind {
	case "begin":
		if !*gracePeriodFired {
			gracePeriodTimer.Stop()
			*gracePeriodFired = true
		}
		r.openTokens[event.Token] = struct{}{}

	case "end":
		if _, ok := r.openTokens[event.Token]; ok {
			delete(r.openTokens, event.Token)
			r.completedOne = true
		}
	}

	if r.completedOne {
		r.markReady()
		return true
	}
	return false
}

func drainReadinessProgress(sess session, e *entry, r *readiness, gracePeriodFired *bool, gracePeriodTimer *time.Timer) bool {
	for {
		select {
		case event := <-sess.Progress():
			if processReadinessProgress(e, r, event, gracePeriodFired, gracePeriodTimer) {
				return true
			}
		default:
			return false
		}
	}
}

func finishReadinessAfterTimer(e *entry, r *readiness, gracePeriodFired, gracePeriod bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if r.isTerminal() || (gracePeriod && gracePeriodFired) {
		return false
	}
	if gracePeriod {
		r.markReady()
	} else {
		r.markTimedOut()
	}
	return true
}

// trackReadiness consumes progress events from the session and manages the
// readiness state. It runs as a background goroutine started once the session
// becomes live. It is responsible for closing e.readiness.readyCh when
// readiness is determined (ready or timedOut) via markReady() or markTimedOut().
// It exits cleanly when the session ends or the manager context is cancelled,
// leaving the state unmodified for those cases; awaitReadySession independently
// observes sess.Exited() or caller ctx.Done() to signal early exit.
func (m *Manager) trackReadiness(e *entry, sess session, r *readiness) {
	gracePeriodTimer := time.NewTimer(time.Duration(m.cfg.ReadyGracePeriod.Duration()))
	defer gracePeriodTimer.Stop()

	readyTimeoutTimer := time.NewTimer(time.Duration(m.cfg.ReadyTimeout.Duration()))
	defer readyTimeoutTimer.Stop()

	gracePeriodFired := false

	for {
		select {
		case event := <-sess.Progress():
			if processReadinessProgress(e, r, event, &gracePeriodFired, gracePeriodTimer) {
				return
			}

		case <-gracePeriodTimer.C:
			if drainReadinessProgress(sess, e, r, &gracePeriodFired, gracePeriodTimer) || finishReadinessAfterTimer(e, r, gracePeriodFired, true) {
				return
			}

		case <-readyTimeoutTimer.C:
			if drainReadinessProgress(sess, e, r, &gracePeriodFired, gracePeriodTimer) || finishReadinessAfterTimer(e, r, gracePeriodFired, false) {
				return
			}

		case <-sess.Exited():
			return

		case <-m.mgrCtx.Done():
			return
		}
	}
}
