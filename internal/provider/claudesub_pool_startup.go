package provider

import (
	"context"
	"errors"
)

// settleStartup finishes one in-flight startup. While the pool is still running
// it publishes the session atomically; otherwise, or when another startup
// already published this key, it tears the new session down locally BEFORE it
// drops the startup registration, so Close waits for that teardown and the
// session is neither published nor returned.
func (p *ClaudeSubscriptionPool) settleStartup(ctx context.Context, st *claudeSubStartup, key string, s *claudeSubSession, startErr error) (*claudeSubSession, error) {
	if startErr != nil || s == nil {
		// Tear the partial session down with an independently bounded context
		// (never the caller's, which may already be canceled and would make the
		// cleanup itself fail), then transfer any cleanup failure to the pool in
		// the same settlement transaction that deregisters the startup. Keeping
		// the startup registered through this cleanup and ownership transfer is
		// what stops a late failure from being lost to an early shutdown snapshot.
		var cleanupErr error
		if s != nil {
			cleanupErr = p.forceCloseSession(s)
		}
		p.deregisterStartup(st, s, cleanupErr)
		if cleanupErr != nil {
			return nil, errors.Join(startErr, cleanupErr)
		}
		return nil, startErr
	}

	// The startup may have succeeded after its caller canceled or after pool
	// shutdown began (a production spawn ignores context). Never publish or
	// return a session its caller no longer waits for: tear it down locally and
	// report the cancellation. Pool shutdown reports the closed error; a caller
	// cancellation reports the caller's own error.
	if cancelErr := p.startupCanceledErr(ctx); cancelErr != nil {
		teardownErr := p.forceCloseSession(s)
		p.deregisterStartup(st, s, teardownErr)
		if teardownErr != nil {
			// A cleanup failure is a pool-owned safety failure: it must reach
			// the caller, not be folded into the cancellation alone.
			return nil, errors.Join(cancelErr, teardownErr)
		}
		return nil, cancelErr
	}

	// Acquire the fresh session's whole-call token BEFORE publishing it. The
	// session is private and unpublished, so the token cannot contend except
	// with the session's own closure; the wait is cancellation-aware and the
	// pool mutex is never held while waiting for it.
	if err := s.lockCall(ctx); err != nil {
		// Cancellation before admission: the session is never published. Clean
		// it up through the same bounded, ownership-transferring path as any
		// other startup that will not publish.
		cleanupErr := p.forceCloseSession(s)
		p.deregisterStartup(st, s, cleanupErr)
		if cleanupErr != nil {
			return nil, errors.Join(err, cleanupErr)
		}
		return nil, err
	}
	if p.beforeAdmission != nil {
		p.beforeAdmission()
	}

	p.mu.Lock()
	// One final admission check under the pool mutex, and the acquisition's only
	// linearization point: passing it publishes the session and registers the
	// active reservation in the SAME critical section, so a request canceled
	// before it can neither publish its new session nor take a lease, while a
	// cancellation after it is valid post-admission revocation. This fuses the
	// former publish-then-takeLease pair, whose gap let a cancellation publish a
	// session it never leased.
	if p.state == claudeSubPoolRunning && !s.isGone() && ctx.Err() == nil {
		if _, exists := p.sessions[key]; !exists {
			p.sessions[key] = s
			s.active++
			p.active++
			p.mu.Unlock()
			p.deregisterStartup(st, nil, nil)
			if p.afterAdmission != nil {
				p.afterAdmission()
			}
			return s, nil
		}
	}
	p.mu.Unlock()

	// Pool shutdown began, the session is gone, the caller canceled inside the
	// critical section, or another startup already published this key: never
	// publish this session. Return its call token, tear it down locally, and
	// transfer any cleanup failure to the pool before dropping its registration.
	s.unlockCall()
	cancelErr := ctx.Err()
	teardownErr := p.forceCloseSession(s)
	p.deregisterStartup(st, s, teardownErr)
	if cancelErr != nil {
		if teardownErr != nil {
			return nil, errors.Join(cancelErr, teardownErr)
		}
		return nil, cancelErr
	}

	// If a live session still owns the key, hand it to this acquire.
	p.mu.Lock()
	existing := p.sessions[key]
	running := p.state == claudeSubPoolRunning
	if existing != nil && running {
		existing.active++
		p.active++
	}
	p.mu.Unlock()
	if existing == nil || !running {
		// A failed teardown has already failed the pool closed, so this must
		// not silently hand back the winning session while a resource's fate
		// is uncertain.
		if teardownErr != nil {
			return nil, errors.Join(errClaudeSubPoolClosed, teardownErr)
		}
		return nil, errClaudeSubPoolClosed
	}
	return p.takeLease(ctx, existing)
}

// startupCanceledErr reports why a completed startup must not publish its
// session: pool shutdown takes precedence and reports the closed error,
// otherwise a canceled caller request reports its own cancellation. It returns
// nil while the pool runs and the caller still waits.
func (p *ClaudeSubscriptionPool) startupCanceledErr(ctx context.Context) error {
	if p.ctx.Err() != nil {
		return errClaudeSubPoolClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// deregisterStartup removes st from the in-flight set, records its local
// teardown result, and closes done so Close can stop waiting for it. When the
// teardown of an unpublished session (s) failed, it atomically transfers the
// affected session and its cleanup error to the pool's unresolved accounting
// (never the reusable session map) under the same lock, and fails the pool
// closed so no later acquire proceeds while the resource's fate is uncertain.
// It is safe to call once per startup.
func (p *ClaudeSubscriptionPool) deregisterStartup(st *claudeSubStartup, s *claudeSubSession, err error) {
	st.stop()
	p.mu.Lock()
	if _, ok := p.startups[st]; ok {
		st.err = err
		delete(p.startups, st)
		if err != nil && s != nil {
			p.retainUnresolvedLocked(s, err)
		}
		close(st.done)
	}
	p.mu.Unlock()
}
