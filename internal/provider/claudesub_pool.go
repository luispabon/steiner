package provider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	// claudeSubDefaultIdleTTL is how long an advisor session may sit unused
	// before the reaper closes it (D23).
	claudeSubDefaultIdleTTL = 60 * time.Minute
	// claudeSubAdvisorKeySuffix marks the advisor session of a parent session.
	// The advisor gets its own CLI process per parent session (D9, D19), and it
	// is the only session kind the reaper closes (D23).
	claudeSubAdvisorKeySuffix = "|advisor"
)

var (
	// claudeSubReaperInterval is how often the pool scans for idle advisor
	// sessions. Tests shorten it.
	claudeSubReaperInterval = time.Minute
	// claudeSubSessionCloseTimeout bounds the teardown of one session the pool
	// closes outside shutdown (a start failure or a reap). Tests shorten it.
	claudeSubSessionCloseTimeout = 10 * time.Second
	// claudeSubPoolShutdownTimeout is the one absolute deadline for a whole pool
	// shutdown. It covers joining the reaper, waiting for every tracked in-flight
	// startup to settle, and every session's forced teardown. Tests shorten it.
	claudeSubPoolShutdownTimeout = 15 * time.Second
)

var (
	// errClaudeSubPoolClosed is returned by acquire once Close has begun.
	errClaudeSubPoolClosed = errors.New("claude_subscription pool is closed")
	// errClaudeSubPoolShutdownIncomplete reports that shutdown reached its one
	// deadline before every startup settled and every session tore down. It is
	// deliberately returned despite the deadline because an injected locate or
	// spawn may ignore context cancellation and outlive it, so the pool must not
	// claim every resource ended.
	errClaudeSubPoolShutdownIncomplete = errors.New("claude_subscription pool shutdown did not complete within deadline")
	// errClaudeSubSessionExited reports that a parent or child session's CLI
	// process ended. The dead session is never respawned: resuming its history is
	// not supported yet (#895).
	errClaudeSubSessionExited = errors.New("claude CLI process exited; start a new session (resume is not supported yet, #895)")
	// errClaudeSubDirCleanupIncomplete reports that a session's directory removal
	// did not finish before the caller's deadline. os.RemoveAll cannot be
	// canceled, so removal keeps running after this error is returned; the pool
	// retains the session and its removal job rather than promising the directory
	// is gone.
	errClaudeSubDirCleanupIncomplete = errors.New("claude_subscription session dir cleanup did not complete within deadline")
)

// claudeSubPoolState is the pool lifecycle state machine: running -> closing ->
// closed. acquire admits a call only while running; shutdown moves closing then
// closed exactly once, so no acquisition can observe running after shutdown
// begins.
type claudeSubPoolState int

const (
	claudeSubPoolRunning claudeSubPoolState = iota
	claudeSubPoolClosing
	claudeSubPoolClosed
)

// claudeSubStartup tracks one in-flight session startup (locate plus spawn plus
// start). It is registered under the pool mutex BEFORE locating or spawning, so
// Close can cancel it through the pool lifetime context and wait for it to
// settle. done is closed once the startup is published or torn down and its
// registration dropped; err is the local teardown result, set before done is
// closed.
type claudeSubStartup struct {
	// stop cancels the startup context and detaches it from the caller's
	// context. It is safe to call more than once.
	stop func()
	done chan struct{}
	err  error
}

// claudeSubUnresolved is one session the pool owns because its teardown did not
// finish cleanly: a partial-start cleanup failure, a caller-cancelled or
// duplicate startup's failed teardown, or a directory removal that did not
// complete before its deadline. Such a failure is a pool-owned safety failure,
// not a transient acquisition failure: the session is never put in the reusable
// map and the pool fails closed. A session's sync.Once close means only its
// cached teardown error can be replayed, so this retains both the session and
// that error (and, through the session, its directory-removal job). Close
// reports them when the transfer is known before its deadline; a transfer after
// publication stays durably owned here even though it cannot alter the returned
// result.
type claudeSubUnresolved struct {
	session *claudeSubSession
	err     error
}

// claudeSubAdvisorSessionKey returns the session key for the advisor process of
// a parent session. The suffix keeps the advisor's process distinct from the
// parent's and marks it as the only session kind the reaper closes (D9, D23).
func claudeSubAdvisorSessionKey(base string) string {
	return base + claudeSubAdvisorKeySuffix
}

// claudeSubSessionIsAdvisor reports whether key names an advisor session. Only
// the exact |advisor suffix classifies: a key that merely mentions "advisor"
// elsewhere is an ordinary parent or child session and is never reaped.
func claudeSubSessionIsAdvisor(key string) bool {
	return strings.HasSuffix(key, claudeSubAdvisorKeySuffix)
}

// ClaudeSubscriptionPoolOptions configures a ClaudeSubscriptionPool.
type ClaudeSubscriptionPoolOptions struct {
	// ToolOutputMaxBytes is steiner's tool-output budget, forwarded to the CLI
	// as MAX_MCP_OUTPUT_TOKENS so the CLI never truncates steiner's already
	// shaped results (D12). Zero keeps the CLI's own default.
	ToolOutputMaxBytes int
	// WorkDir is the child process working directory. Empty uses the current
	// working directory.
	WorkDir string
	// IdleTTL is how long an advisor session may sit unused before the reaper
	// closes it. Zero or negative defaults to 60 minutes (D23).
	IdleTTL time.Duration
}

// ClaudeSubscriptionPool owns the claude CLI processes for one steiner runtime,
// one process per session key (D9). Sessions start lazily on first acquire and
// are closed by Close; only advisor sessions are also reaped after IdleTTL
// (D23). The pool never holds its mutex while starting or closing a process.
//
// Lifecycle protocol: Close atomically marks the pool closing and cancels the
// pool lifetime context, which fails every later acquire and aborts every
// in-flight startup. It then runs one shutdown coordinator that force-closes
// every published session WITHOUT taking a session's whole-call lock, so parked
// MCP/control/CLI calls wake and release naturally; active call leases are never
// a prerequisite to beginning teardown. Under one absolute deadline the
// coordinator waits for tracked startups to settle and for session teardown, then
// collects the retained cleanup ownership (including any directory-removal job
// still running) and publishes a single shared result every Close caller
// returns.
//
// A startup registers itself under the pool mutex before locating or spawning
// and only publishes its session atomically while the pool is still running; if
// it finishes after closing began it tears the session down locally before
// dropping its registration and never publishes or returns it. A call that
// waits on a session's whole-call lock does so with cancellation: it observes
// its context, the session's teardown, or the pool lifetime, and rechecks the
// terminal state under the pool mutex before a session is returned.
//
// acquire is linearizable only at that final pool-state admission: if it passes
// the recheck, acquire returns the session as an exclusive whole-call
// serialization lease. The lease guarantees serialization, not transport
// lifetime: because Close never waits for active leases, an admitted lease may
// be force-closed immediately afterward, before the caller resumes, and any
// later operation on the revoked session must observe a safe error, never a
// panic or deadlock. Callers must release the lease on every path.
//
// Lock order: the pool mutex is never held while a session's whole-call lock is
// taken, and a session's whole-call lock is never held while the pool mutex is
// taken. active and lastUsed are only ever read or written under the pool
// mutex, which is what lets the reaper and acquire agree on whether a session is
// in use.
type ClaudeSubscriptionPool struct {
	mu       sync.Mutex
	sessions map[string]*claudeSubSession
	// startups holds every in-flight startup, tracked separately from the active
	// call leases below, so Close can wait for them without waiting for calls.
	startups map[*claudeSubStartup]struct{}
	opts     ClaudeSubscriptionPoolOptions

	// active counts reservations across the pool, taken under mu before the
	// session's whole-call lock; the reaper uses a session's own active count to
	// skip in-use sessions.
	active int

	// state is the lifecycle state machine; ctx/cancel are the pool lifetime,
	// canceled the moment Close marks closing so every startup context derived
	// from it aborts.
	state  claudeSubPoolState
	ctx    context.Context
	cancel context.CancelFunc
	// shutdownCtx/shutdownCancel is the pool's one absolute shutdown deadline.
	// It is created at the running-to-closing transition and reused by every
	// cleanup begun after closing (a startup's local teardown included), so no
	// such cleanup gets a fresh per-session allowance and every wait shares the
	// one deadline. shutdownCancel releases it when the coordinator finishes.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc

	// cliJob is the pool's single shared CLI lookup, started by the first
	// acquire. It is tracked like every cleanup job so shutdown awaits it only to
	// the stored deadline and reports the pool incomplete (retaining the lookup)
	// when an injected lookup ignores cancellation.
	cliJob *claudeSubJob
	cli    claudeSubCLI
	cliErr error

	// Seams, defaulted in NewClaudeSubscriptionPool and overridden in tests so
	// no test locates or starts a real claude CLI or relies on filesystem
	// permissions for a failure.
	lookPath func(string) (string, error)
	run      claudeSubRunner
	spawn    claudeSubSpawner
	// chmod secures a freshly created session directory and removeAll removes a
	// session directory during teardown.
	chmod     func(string, os.FileMode) error
	removeAll func(string) error
	// closeHost tears down a session's MCP host; tests override it to make host
	// shutdown block deterministically.
	closeHost func(*claudeSubMCPHost) error
	// beforeJobRecord, when set, runs once before a cleanup job publishes its
	// result under mu. Tests use it to pause error transfer at the result
	// publication boundary.
	beforeJobRecord func(error)
	// beforeAdmission, when set, runs after a freshly started session's
	// whole-call token is taken and before the atomic admission check that
	// publishes it. Tests use it to pause exactly between the former
	// publication and admission stages so a cancellation can be injected in
	// that window deterministically.
	beforeAdmission func()
	// afterAdmission, when set, runs once a fresh session has been published
	// and admitted, before acquire returns its lease. Tests use it to inject a
	// post-admission cancellation deterministically.
	afterAdmission func()

	// unresolved holds sessions the pool still owns because their teardown did
	// not finish cleanly: a partial-start cleanup failure, a caller-cancelled or
	// duplicate startup's failed teardown, or a directory removal that did not
	// complete before its deadline. Each retains the session (and its removal
	// job) plus the teardown error. They are never in the reusable map and are
	// never cleared: ownership survives the shutdown deadline, so a failure
	// discovered after Close published its result stays durably owned even
	// though it cannot retroactively change that result. Guarded by mu.
	unresolved []claudeSubUnresolved

	// cleanupErrs is the pool-owned ledger of cleanup errors discovered so far,
	// appended under mu as cleanup jobs settle. Final close result assembly
	// merges it under mu, so an error known before publication is always joined;
	// one discovered afterwards stays durably owned here but cannot alter the
	// already-published result.
	cleanupErrs []error
	// cleanupIncomplete records that a cleanup was still unsettled when a wait
	// against the stored deadline expired. Set under mu, read at publication.
	cleanupIncomplete bool
	// cleanupJobs holds every started once-started cleanup job, so final result
	// assembly can tell whether one is still unsettled at publication.
	cleanupJobs []*claudeSubJob

	// closeErr is the shared terminal result, published under mu before done is
	// closed. Every Close caller waits on done and returns the same closeErr.
	closeErr error
	done     chan struct{}

	stopReaper chan struct{}
	reaperDone chan struct{}
}

// NewClaudeSubscriptionPool creates an empty pool. No CLI is located and no
// process starts until the first acquire.
func NewClaudeSubscriptionPool(opts ClaudeSubscriptionPoolOptions) *ClaudeSubscriptionPool {
	if opts.IdleTTL <= 0 {
		opts.IdleTTL = claudeSubDefaultIdleTTL
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &ClaudeSubscriptionPool{
		sessions:   map[string]*claudeSubSession{},
		startups:   map[*claudeSubStartup]struct{}{},
		opts:       opts,
		ctx:        ctx,
		cancel:     cancel,
		lookPath:   exec.LookPath,
		run:        claudeSubExecRunner,
		spawn:      spawnClaudeSubProcess,
		chmod:      os.Chmod,
		removeAll:  os.RemoveAll,
		closeHost:  func(h *claudeSubMCPHost) error { return h.Close() },
		done:       make(chan struct{}),
		stopReaper: make(chan struct{}),
		reaperDone: make(chan struct{}),
	}
	p.cliJob = p.newTrackedJob()
	go p.reapLoop()
	return p
}

// locateCLI locates and caches the claude CLI once for the pool's life. The
// lookup runs as the pool's single shared tracked job under the pool lifetime
// context, started by the first caller, so it is a single lookup shared by every
// acquire and the result (success or failure) is cached for the pool's life. A
// caller that cancels while waiting stops waiting and returns its own
// cancellation WITHOUT cancelling the shared lookup or caching its outcome, so a
// transient caller cancellation never poisons the cache; only pool shutdown
// cancels the lookup, and shutdown awaits the job to the stored deadline.
func (p *ClaudeSubscriptionPool) locateCLI(ctx context.Context) (claudeSubCLI, error) {
	p.cliJob.start(func() error {
		cli, err := locateClaudeSubCLI(p.ctx, p.lookPath, p.run)
		p.mu.Lock()
		p.cli, p.cliErr = cli, err
		p.mu.Unlock()
		return err
	})
	select {
	case <-p.cliJob.done:
	case <-ctx.Done():
		return claudeSubCLI{}, ctx.Err()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cli, p.cliErr
}

// acquire returns the session for key, starting it from spec on first use. The
// returned session is locked (its whole-call lock is held) for the whole call,
// so later code must call release when the call ends. acquire blocks on the
// session lock while another call on the same key runs; the wait is
// cancellation-aware and observes pool shutdown.
//
// Close coordination: acquire fails once Close has begun, and a startup is
// registered under the pool mutex before it locates or spawns, so Close can
// cancel it and wait for it to settle. Every reservation is counted under the
// pool mutex before the whole-call lock is taken, so a call blocked on another
// call is still counted and the reaper never selects it.
func (p *ClaudeSubscriptionPool) acquire(ctx context.Context, key string, spec claudeSubStartSpec) (*claudeSubSession, error) {
	p.mu.Lock()
	if p.state != claudeSubPoolRunning {
		p.mu.Unlock()
		return nil, errClaudeSubPoolClosed
	}
	// Admission is decided under the pool mutex: an already-canceled request is
	// rejected here, before any session is returned or published, so it can
	// neither receive a lease nor race a ready call token.
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if s, ok := p.sessions[key]; ok {
		if !s.exited() {
			s.active++
			p.active++
			p.mu.Unlock()
			return p.takeLease(ctx, s)
		}
		if !s.advisor {
			p.mu.Unlock()
			return nil, errClaudeSubSessionExited
		}
		// Evict the dead advisor, then retry so this call starts a fresh process
		// that re-renders the snapshot. The eviction teardown is registered as an
		// in-flight startup so Close waits for it like any other startup.
		delete(p.sessions, key)
		st := &claudeSubStartup{done: make(chan struct{})}
		p.startups[st] = struct{}{}
		p.mu.Unlock()
		p.finishEviction(st, s, p.closeSession(s))
		return p.acquire(ctx, key, spec)
	}

	// Register the startup under the pool mutex BEFORE locating or spawning, so
	// Close can wait for it. The startup context is canceled by pool shutdown
	// (it derives from p.ctx) or by the caller (AfterFunc), so production
	// locate and spawn observe cancellation.
	startupCtx, cancelStartup := context.WithCancel(p.ctx)
	stopCaller := context.AfterFunc(ctx, cancelStartup)
	st := &claudeSubStartup{
		stop: func() { stopCaller(); cancelStartup() },
		done: make(chan struct{}),
	}
	p.startups[st] = struct{}{}
	p.mu.Unlock()

	cli, err := p.locateCLI(startupCtx)
	if err == nil {
		err = startupCtx.Err()
	}
	var s *claudeSubSession
	if err == nil {
		s, err = startClaudeSubSession(startupCtx, p, key, spec, cli)
	}
	if err != nil {
		// The startup context is canceled by pool shutdown or by the caller.
		// Only pool shutdown must surface as the closed error; a transient
		// caller cancellation must not masquerade as shutdown. Join rather than
		// replace so a cleanup failure already attached to the start error is
		// never lost.
		if cancelErr := p.startupCanceledErr(ctx); cancelErr != nil {
			err = errors.Join(cancelErr, err)
		}
	}
	return p.settleStartup(ctx, st, key, s, err)
}

// takeLease finishes an acquisition whose reservation (active counters) was
// already taken under the pool mutex. It takes the session's whole-call lock in
// a cancellation-aware way, then under the pool mutex rechecks that the pool is
// still running and the session is not being torn down. That recheck is the
// acquisition's only linearization point: once it passes, the session is
// returned as an exclusive whole-call serialization lease. Because Close never
// waits for active leases, shutdown may revoke the lease by force-closing the
// session immediately after the recheck, before the caller resumes; the lease
// therefore never promises transport liveness. On any failure it drops the
// reservation and returns the closed error.
func (p *ClaudeSubscriptionPool) takeLease(ctx context.Context, s *claudeSubSession) (*claudeSubSession, error) {
	if err := s.lockCall(ctx); err != nil {
		p.dropReservation(s)
		return nil, err
	}
	p.mu.Lock()
	// The final admission recheck is the acquisition's linearization point: a
	// request canceled before it (even after winning the call token) is rejected,
	// so cancellation exactly before admission never yields a lease. A
	// cancellation after this point is revocation-after-admission and allowed.
	if p.state != claudeSubPoolRunning || s.isGone() || ctx.Err() != nil {
		p.mu.Unlock()
		s.unlockCall()
		p.dropReservation(s)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errClaudeSubPoolClosed
	}
	p.mu.Unlock()
	return s, nil
}

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
