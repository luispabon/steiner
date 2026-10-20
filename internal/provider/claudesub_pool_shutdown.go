package provider

import (
	"context"
	"errors"
	"sync"
	"time"
)

// retainUnresolvedLocked transfers a session whose teardown did not finish
// cleanly to the pool's unresolved ownership and fails the pool closed, so no
// later acquire proceeds while the resource's fate is uncertain. It must be
// called with mu held and never blocks.
func (p *ClaudeSubscriptionPool) retainUnresolvedLocked(s *claudeSubSession, err error) {
	p.unresolved = append(p.unresolved, claudeSubUnresolved{session: s, err: err})
	p.beginClosingLocked()
}

// retainUnresolved is retainUnresolvedLocked for callers that do not hold mu.
func (p *ClaudeSubscriptionPool) retainUnresolved(s *claudeSubSession, err error) {
	p.mu.Lock()
	p.retainUnresolvedLocked(s, err)
	p.mu.Unlock()
}

// markCleanupIncomplete records under the pool mutex that a cleanup was still
// unsettled when its wait against the stored deadline expired.
func (p *ClaudeSubscriptionPool) markCleanupIncomplete() {
	p.mu.Lock()
	p.cleanupIncomplete = true
	p.mu.Unlock()
}

// claudeSubCleanupUncertain reports whether err leaves a cleanup's completion
// uncertain: the resource may still be running, so the pool retains ownership
// and reports the shutdown incomplete instead of a settled failure. A plain
// teardown error whose work finished is not uncertain.
func claudeSubCleanupUncertain(err error) bool {
	return errors.Is(err, errClaudeSubTeardownIncomplete) ||
		errors.Is(err, errClaudeSubDirCleanupIncomplete) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
}

// dropReservation returns a reservation taken under the pool mutex by an
// acquire that never took the session's whole-call lock.
func (p *ClaudeSubscriptionPool) dropReservation(s *claudeSubSession) {
	p.mu.Lock()
	if s.active > 0 {
		s.active--
	}
	if p.active > 0 {
		p.active--
	}
	p.mu.Unlock()
}

// release ends a call on a session returned by acquire: it returns the
// session's whole-call lock and records the session as just used, so the reaper
// never treats a session that was in use as idle.
func (p *ClaudeSubscriptionPool) release(s *claudeSubSession) {
	s.unlockCall()
	p.mu.Lock()
	if s.active > 0 {
		s.active--
	}
	if p.active > 0 {
		p.active--
	}
	s.lastUsed = time.Now()
	p.mu.Unlock()
}

// Close begins pool shutdown and returns the shared terminal result. The first
// caller atomically marks the pool closing and cancels the pool lifetime (which
// aborts every in-flight startup context), then runs the single shutdown
// coordinator; concurrent callers wait on the same done channel and return the
// same result. Close is idempotent.
func (p *ClaudeSubscriptionPool) Close() error {
	p.mu.Lock()
	p.beginClosingLocked()
	p.mu.Unlock()
	<-p.done
	p.mu.Lock()
	err := p.closeErr
	p.mu.Unlock()
	return err
}

// beginClosingLocked starts pool shutdown exactly once: it moves a running pool
// to closing, cancels the pool lifetime so every in-flight startup aborts, and
// runs the single shutdown coordinator. It must be called with mu held. A later
// caller observes closing/closed and does nothing. It is the shared trigger for
// an explicit Close and for a safety failure that must fail the pool closed.
func (p *ClaudeSubscriptionPool) beginClosingLocked() {
	if p.state != claudeSubPoolRunning {
		return
	}
	p.state = claudeSubPoolClosing
	p.shutdownCtx, p.shutdownCancel = context.WithTimeout(context.Background(), claudeSubPoolShutdownTimeout)
	p.cancel()
	go p.shutdown()
}

// shutdown is the single shutdown coordinator. It stops the reaper, snapshots
// the published sessions and the in-flight startups, and under one absolute
// deadline force-closes every session concurrently and waits for every startup
// to settle. Force-closing never takes a session's whole-call lock and never
// waits for an active lease, so a parked call wakes and releases naturally
// instead of blocking shutdown; an admitted lease may therefore be revoked
// mid-flight.
//
// Every cleanup begun after closing shares the pool's one stored deadline, and
// every wait below is bounded by it: the coordinator never blocks past it even
// while an uncancelable teardown or an injected lookup keeps running. Cleanup
// errors are recorded in the pool's ledger under the mutex as each cleanup job
// settles, so final result assembly below linearizes under that same mutex: it
// inspects the tracked jobs for any still unsettled, marks the shutdown
// incomplete when one is, merges the ledger, and publishes the one immutable
// closeErr before closing done. A cleanup that settles after publication stays
// durably owned but can no longer change the returned result.
func (p *ClaudeSubscriptionPool) shutdown() {
	p.mu.Lock()
	ctx := p.shutdownCtx
	cancel := p.shutdownCancel
	cliJob := p.cliJob
	cliStarted := cliJob.started
	p.mu.Unlock()
	defer cancel()

	// waitGroup blocks until wg drains or the deadline expires, marking the
	// shutdown incomplete in the latter case. Every Add happens before Wait.
	waitGroup := func(wg *sync.WaitGroup) {
		drained := make(chan struct{})
		go func() { wg.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-ctx.Done():
			p.markCleanupIncomplete()
		}
	}

	// Stop the reaper so it cannot start new closes. It is joined below, under
	// the deadline, so a reaper mid-teardown never delays the forced teardown of
	// the published sessions.
	close(p.stopReaper)

	// Snapshot published sessions and in-flight startups atomically. Any startup
	// registered before this point had its context canceled by p.cancel(), so it
	// observes pool shutdown. Unresolved ownership is not touched here; it is
	// collected after the startups below have settled.
	p.mu.Lock()
	sessions := make([]*claudeSubSession, 0, len(p.sessions))
	for _, s := range p.sessions {
		sessions = append(sessions, s)
	}
	p.sessions = map[string]*claudeSubSession{}
	startups := make([]*claudeSubStartup, 0, len(p.startups))
	for st := range p.startups {
		startups = append(startups, st)
	}
	p.mu.Unlock()

	var wg sync.WaitGroup
	// join waits for done in a tracked goroutine. If the shutdown deadline expires
	// first, the shutdown is marked incomplete.
	join := func(done <-chan struct{}) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-done:
			case <-ctx.Done():
				p.markCleanupIncomplete()
			}
		}()
	}
	for _, s := range sessions {
		wg.Add(1)
		go func(s *claudeSubSession) {
			defer wg.Done()
			// Any force-close error means a resource's termination may be
			// uncertain: retain the session so it is never silently dropped, and
			// mark the shutdown incomplete when the error leaves completion
			// uncertain rather than reporting a settled failure.
			if err := s.forceClose(ctx); err != nil {
				p.retainUnresolved(s, err)
				if claudeSubCleanupUncertain(err) {
					p.markCleanupIncomplete()
				}
			}
		}(s)
	}
	// Wait for every in-flight startup to settle. A startup that finished after
	// closing began has already torn its session down locally and transferred
	// any cleanup failure to the ledger; a startup that has not settled by the
	// deadline leaves shutdown incomplete.
	for _, st := range startups {
		join(st.done)
	}
	// Join the reaper under the same deadline.
	join(p.reaperDone)
	// Await the shared CLI lookup only to the same deadline. Its completion is
	// durable, so if an injected lookup ignores cancellation the pool reports
	// incomplete and keeps the job (and its one lookup) tracked.
	if cliStarted {
		join(cliJob.done)
	}
	waitGroup(&wg)

	// Only now, after every tracked startup has settled (or the deadline
	// expired), collect the accumulated cleanup errors and the retained ownership
	// under the pool mutex. A retained session's sync.Once close means forceClose
	// only replays its cached error, so this reports the owned failure honestly
	// instead of promising a retry; any removal job it still owns is waited only
	// to the same deadline.
	p.mu.Lock()
	unresolved := make([]claudeSubUnresolved, 0, len(p.unresolved))
	unresolved = append(unresolved, p.unresolved...)
	p.mu.Unlock()

	for _, u := range unresolved {
		if err := u.session.forceClose(ctx); err != nil && claudeSubCleanupUncertain(err) {
			p.markCleanupIncomplete()
		}
		if job := u.session.removeJob; job != nil && !claudeSubJoined(ctx, job.done) {
			p.markCleanupIncomplete()
		}
	}

	// Final result assembly linearizes under the pool mutex against every job
	// completion and ownership transfer. Completed jobs have already recorded
	// their errors in the ledger; a tracked job still unsettled at this point
	// makes the shutdown incomplete; the one immutable result is then published
	// and done is closed together, all under the same lock.
	p.mu.Lock()
	for _, j := range p.cleanupJobs {
		select {
		case <-j.done:
		default:
			p.cleanupIncomplete = true
		}
	}
	errs := p.cleanupErrs
	if p.cleanupIncomplete {
		errs = append(append([]error(nil), errs...), errClaudeSubPoolShutdownIncomplete)
	}
	p.closeErr = errors.Join(errs...)
	p.state = claudeSubPoolClosed
	close(p.done)
	p.mu.Unlock()
}
