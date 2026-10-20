package provider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/luispabon/steiner/internal/quota"
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
	// errClaudeSubSessionRetired reports that a waiter's session was retired while
	// the pool still runs. acquire retries from the map and never returns it.
	errClaudeSubSessionRetired = errors.New("claude_subscription session was retired")
	// errClaudeSubPoolShutdownIncomplete reports that shutdown reached its one
	// deadline before every startup settled and every session tore down. It is
	// deliberately returned despite the deadline because an injected locate or
	// spawn may ignore context cancellation and outlive it, so the pool must not
	// claim every resource ended.
	errClaudeSubPoolShutdownIncomplete = errors.New("claude_subscription pool shutdown did not complete within deadline")
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
	// QuotaObserver, when set, receives a passive quota snapshot for each
	// get_usage reply the usage gate parses. Nil disables passive observation.
	QuotaObserver func(quota.Snapshot)
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
			lease, err := p.takeLease(ctx, s)
			if errors.Is(err, errClaudeSubSessionRetired) {
				return p.acquire(ctx, key, spec)
			}
			return lease, err
		}
		// Evict the exited process, then retry so this call starts a fresh one. The
		// caller's spawn spec decides whether it resumes or reseeds (D3). The
		// eviction teardown is registered as an in-flight startup so Close waits for
		// it like any other startup.
		st := p.evictLocked(key, s)
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
	lease, err := p.settleStartup(ctx, st, key, s, err)
	if errors.Is(err, errClaudeSubSessionRetired) {
		return p.acquire(ctx, key, spec)
	}
	return lease, err
}

// acquireLive returns the running session for key with its whole-call lock
// held, or nil with no error when none runs. It never starts a process, so a
// caller can inspect the live record before it decides how to answer. The caller
// releases a non-nil session.
func (p *ClaudeSubscriptionPool) acquireLive(ctx context.Context, key string) (*claudeSubSession, error) {
	p.mu.Lock()
	if p.state != claudeSubPoolRunning {
		p.mu.Unlock()
		return nil, errClaudeSubPoolClosed
	}
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	s, ok := p.sessions[key]
	if !ok || s.exited() {
		p.mu.Unlock()
		return nil, nil
	}
	s.active++
	p.active++
	p.mu.Unlock()
	lease, err := p.takeLease(ctx, s)
	if errors.Is(err, errClaudeSubSessionRetired) {
		// Retired while this call waited: no live session runs under key now.
		return nil, nil
	}
	return lease, err
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
// reservation and returns the error from leaseFailure.
func (p *ClaudeSubscriptionPool) takeLease(ctx context.Context, s *claudeSubSession) (*claudeSubSession, error) {
	if err := s.lockCall(ctx); err == nil {
		p.mu.Lock()
		// The final admission recheck is the acquisition's linearization point: a
		// request canceled before it (even after winning the call token) is rejected,
		// so cancellation exactly before admission never yields a lease. A
		// cancellation after this point is revocation-after-admission and allowed.
		if p.state == claudeSubPoolRunning && !s.isGone() && ctx.Err() == nil {
			p.mu.Unlock()
			return s, nil
		}
		p.mu.Unlock()
		s.unlockCall()
	}
	p.dropReservation(s)
	return nil, p.leaseFailure(ctx, s)
}

// leaseFailure returns the error for a lease that did not take. A canceled
// caller gets its own error. A session retired while the pool still runs yields
// errClaudeSubSessionRetired, so the caller takes a successor. Any other failure
// means the pool is closing.
func (p *ClaudeSubscriptionPool) leaseFailure(ctx context.Context, s *claudeSubSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	running := p.state == claudeSubPoolRunning
	p.mu.Unlock()
	if running && s.isGone() {
		return errClaudeSubSessionRetired
	}
	return errClaudeSubPoolClosed
}
