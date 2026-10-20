package provider

import (
	"context"
	"time"
)

// teardownContext returns the context that bounds a session cleanup begun now,
// plus its release. After the running-to-closing transition it is the pool's
// stored absolute shutdown deadline, so a cleanup begun after closing (a
// startup's local teardown included) spends only that deadline's remaining time
// instead of a fresh per-session allowance. Before closing it is a fresh
// per-session bound.
func (p *ClaudeSubscriptionPool) teardownContext() (context.Context, context.CancelFunc) {
	p.mu.Lock()
	ctx := p.shutdownCtx
	p.mu.Unlock()
	if ctx != nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.Background(), claudeSubSessionCloseTimeout)
}

// closeSession tears a session down within the teardown context for the pool's
// current lifecycle state. It is used outside pool shutdown (start failure or
// reaping); shutdown itself passes the shared pool deadline directly.
func (p *ClaudeSubscriptionPool) closeSession(s *claudeSubSession) error {
	ctx, cancel := p.teardownContext()
	defer cancel()
	return s.close(ctx)
}

// forceCloseSession force-closes a session within the teardown context for the
// pool's current lifecycle state. It is used by a startup that will not publish
// its session, so a cleanup begun after closing shares the shutdown deadline.
func (p *ClaudeSubscriptionPool) forceCloseSession(s *claudeSubSession) error {
	ctx, cancel := p.teardownContext()
	defer cancel()
	return s.forceClose(ctx)
}

// finishEviction settles the teardown of a dead advisor that acquire evicted and
// releases its startup registration so shutdown can stop waiting for it. A
// failed teardown follows the same ownership rules as a reaped session.
func (p *ClaudeSubscriptionPool) finishEviction(st *claudeSubStartup, s *claudeSubSession, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.settleRetiredLocked(s, err)
	}
	st.err = err
	delete(p.startups, st)
	close(st.done)
}

// evictLocked removes s from the reusable map, only while it still owns key,
// and registers its teardown as an in-flight startup so Close waits for it. It
// must be called with mu held; settle the result with finishEviction after
// unlocking.
func (p *ClaudeSubscriptionPool) evictLocked(key string, s *claudeSubSession) *claudeSubStartup {
	if p.sessions[key] == s {
		delete(p.sessions, key)
	}
	st := &claudeSubStartup{done: make(chan struct{})}
	p.startups[st] = struct{}{}
	return st
}

// retire removes the process under key from the reusable map and tears it
// down. The caller holds s's whole-call lock and must still release it; the
// next acquire under key starts a fresh process.
func (p *ClaudeSubscriptionPool) retire(key string, s *claudeSubSession) {
	p.mu.Lock()
	st := p.evictLocked(key, s)
	p.mu.Unlock()
	p.finishEviction(st, s, p.closeSession(s))
}

// retireAndRelease retires the process under key, then releases the caller's
// whole-call lock on it. It is retire followed by release for callers that
// always do both, in that order.
func (p *ClaudeSubscriptionPool) retireAndRelease(key string, s *claudeSubSession) {
	p.retire(key, s)
	p.release(s)
}

// evictDead retires the process under key when it has exited, so the next
// acquire under key starts a fresh one. It reports whether a dead process was
// evicted. The check and the removal happen under one mutex hold.
func (p *ClaudeSubscriptionPool) evictDead(key string) bool {
	p.mu.Lock()
	s, ok := p.sessions[key]
	if p.state != claudeSubPoolRunning || !ok || !s.exited() {
		p.mu.Unlock()
		return false
	}
	st := p.evictLocked(key, s)
	p.mu.Unlock()
	p.finishEviction(st, s, p.closeSession(s))
	return true
}

// settleRetiredLocked records a failed teardown of a session the pool already
// removed from its map (a reaped or evicted advisor). A failure that leaves the
// CLI process not provably terminated is a safety failure: the session is
// retained and the pool fails closed. When only the private directory removal
// failed, the process is terminated, so the session and its leftover directory
// stay pool-owned without closing the pool; Close still reports the removal
// error through the cleanup ledger. It must be called with mu held.
func (p *ClaudeSubscriptionPool) settleRetiredLocked(s *claudeSubSession, err error) {
	if s.procErr != nil {
		p.retainUnresolvedLocked(s, err)
		return
	}
	p.unresolved = append(p.unresolved, claudeSubUnresolved{session: s, err: err})
}

func (p *ClaudeSubscriptionPool) reapLoop() {
	defer close(p.reaperDone)
	ticker := time.NewTicker(claudeSubReaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopReaper:
			return
		case <-ticker.C:
			p.reapIdleAdvisors(time.Now())
		}
	}
}

// reapIdleAdvisors closes advisor sessions idle longer than IdleTTL. It selects
// them under the pool mutex and removes them from the map there, so a later
// acquire starts a fresh process, then closes them outside the lock. Sessions
// with an active call and every non-advisor session are left alone (D23). A
// teardown failure is settled by settleRetiredLocked, which keeps the same
// ownership rules for a reaped and an evicted session.
func (p *ClaudeSubscriptionPool) reapIdleAdvisors(now time.Time) {
	p.mu.Lock()
	if p.state != claudeSubPoolRunning {
		p.mu.Unlock()
		return
	}
	var expired []*claudeSubSession
	for key, s := range p.sessions {
		if !s.advisor || s.active > 0 {
			continue
		}
		if now.Sub(s.lastUsed) < p.opts.IdleTTL {
			continue
		}
		delete(p.sessions, key)
		expired = append(expired, s)
	}
	p.mu.Unlock()
	for _, s := range expired {
		if err := p.closeSession(s); err != nil {
			p.mu.Lock()
			p.settleRetiredLocked(s, err)
			p.mu.Unlock()
		}
	}
}
