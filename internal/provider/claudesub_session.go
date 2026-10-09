package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// errClaudeSubTeardownIncomplete reports that a session's teardown did not
// finish before the deadline a waiting caller supplied. Teardown runs exactly
// once and keeps running after this error is returned, so the pool retains the
// session's ownership rather than promising its transport, writer or directory
// is gone.
var errClaudeSubTeardownIncomplete = errors.New("claude_subscription session teardown did not complete within deadline")

// claudeSubStartSpec is the settled startup specification for a new session:
// everything the process needs to launch, computed by the caller. Full request
// planning (delta, gates, streaming) happens in later steps; start only
// materialises this spec into a running CLI process.
type claudeSubStartSpec struct {
	// SystemPrompt is the rendered system prompt written to the session's
	// private system.md and passed with --system-prompt-file (D10).
	SystemPrompt string
	// Tools are steiner's tool specs. When empty (advisor requests) no MCP host
	// or --mcp-config is created (D19).
	Tools []ToolSpec
	// Model is the CLI --model value.
	Model string
	// Effort is steiner's reasoning effort, mapped by claudeSubEffort to the
	// CLI's --effort and thinking flags (D11).
	Effort string
}

// claudeSubPendingCall is one pending tool call: the tool-use id plus the
// opaque MCP host handle that owns the call. Later code resolves the result
// through the handle, never by re-deriving an id from the string, so a retired
// or reused id can never receive a stale result.
type claudeSubPendingCall struct {
	ID     string
	Handle *claudeSubCall
}

// claudeSubSession is one running claude CLI process plus its MCP host, control
// channel, event queue and append-only sync record.
//
// Locking: call is the whole-call lock, a single token that acquire takes and
// release returns. It serializes calls on one session, so the reaper (which
// only acts on sessions with no active call) can never tear down a session
// mid-call. Using a channel instead of a sync.Mutex lets a waiter abandon the
// wait when its context is canceled or the session is torn down. gone is closed
// when teardown begins, waking every waiter and marking the session terminal.
// teardown deliberately does NOT take the whole-call lock: it is safe against a
// running call (the transport Close/ForceClose is concurrency-safe) and must
// not block runtime shutdown behind a parked call.
type claudeSubSession struct {
	// call is the whole-call lock (capacity 1, one token).
	call chan struct{}
	// gone is closed when teardown begins.
	gone chan struct{}

	key     string
	advisor bool

	conn    claudeSubConn
	control *claudeSubControl
	host    *claudeSubMCPHost
	sync    *claudeSubSync
	dir     string
	// removeAll removes dir during teardown. It is seeded from the owning pool's
	// removeAll seam so a test can force a deterministic cleanup failure.
	removeAll func(string) error
	// pool owns this session's cleanup accounting: the teardown job is
	// registered with it, and the pool's stored shutdown deadline governs every
	// teardown begun after the pool starts closing.
	pool *ClaudeSubscriptionPool
	// closeHost tears down the session's MCP host; it is the pool's seam so a
	// test can make host shutdown block deterministically.
	closeHost func(*claudeSubMCPHost) error
	// removeJob, hostJob and teardownJob are the session's once-started cleanup
	// jobs with durable results: the directory-removal job (os.RemoveAll cannot
	// be canceled), the MCP host-close job (its Close has its own background
	// grace and no context API), and the session teardown that owns both. Every
	// caller waits for a job only up to its own deadline, so no caller blocks
	// behind cleanup already in progress.
	removeJob   *claudeSubJob
	hostJob     *claudeSubJob
	teardownJob *claudeSubJob

	model  string
	effort string

	// pending holds the opaque handles for tool-use ids awaiting results.
	// Guarded by the whole-call lock (the caller holds it for the whole call).
	pending []claudeSubPendingCall

	queue      *claudeSubQueue
	stop       chan struct{}
	routerDone chan struct{}

	// overage is terminal for this session's lifetime. It is independent of the
	// whole-call lock so the router can fail a parked call immediately.
	overageMu    sync.Mutex
	overageErr   error
	overageOnce  sync.Once
	overageAbort chan struct{}

	// active and lastUsed are guarded by the owning pool's mutex.
	active   int
	lastUsed time.Time

	// teardownStarted is set by the single caller that runs teardown to
	// completion; every other caller waits only to its own deadline through the
	// teardown job, so none blocks behind a teardown already in progress.
	teardownStarted atomic.Bool
}

// claudeSubJob is a once-started asynchronous cleanup owned by a pool or one of
// its sessions, with a durable completion and result. start launches its work
// exactly once in its own goroutine and returns immediately; the result is
// published and done is closed together under the pool mutex, so a caller that
// observes done always sees the final result and the shutdown coordinator can
// linearize "is this job settled?" against result publication. A cleanup job
// also records its error in the pool's accumulated ledger before it signals
// done, so a failure known before the close result is published is joined into
// it, while one discovered afterwards stays durably owned without retroactively
// changing the returned result.
type claudeSubJob struct {
	pool    *ClaudeSubscriptionPool
	once    sync.Once
	done    chan struct{}
	err     error
	started bool // guarded by pool.mu; set when the job is registered
	record  bool // record err in the pool's cleanup ledger on completion
}

// newCleanupJob returns a job whose error is recorded in the pool's cleanup
// ledger on completion.
func (p *ClaudeSubscriptionPool) newCleanupJob() *claudeSubJob {
	return &claudeSubJob{pool: p, done: make(chan struct{}), record: true}
}

// newTrackedJob returns a job that is tracked for unsettled detection but whose
// result is not recorded: the shared CLI lookup reports its outcome to acquire
// callers, not as a pool cleanup error.
func (p *ClaudeSubscriptionPool) newTrackedJob() *claudeSubJob {
	return &claudeSubJob{pool: p, done: make(chan struct{})}
}

// start launches the job's work once. It returns immediately and never blocks
// on the work: a later caller observes the durable result through wait instead
// of waiting inside once.
func (j *claudeSubJob) start(fn func() error) {
	j.once.Do(func() {
		j.pool.trackJob(j)
		go func() { j.finish(fn()) }()
	})
}

// trackJob registers a started job under the pool mutex so final result
// assembly can tell whether it is unsettled. It never blocks on the job.
func (p *ClaudeSubscriptionPool) trackJob(j *claudeSubJob) {
	p.mu.Lock()
	j.started = true
	p.cleanupJobs = append(p.cleanupJobs, j)
	p.mu.Unlock()
}

// finish publishes a job's result and closes its done channel atomically under
// the pool mutex. A cleanup job's error is recorded before done closes, so a
// result known before the close result is published is always joined and one
// discovered afterwards is durably owned without changing it.
func (j *claudeSubJob) finish(err error) {
	p := j.pool
	if p.beforeJobRecord != nil {
		p.beforeJobRecord(err)
	}
	p.mu.Lock()
	j.err = err
	if j.record && err != nil {
		p.cleanupErrs = append(p.cleanupErrs, err)
	}
	close(j.done)
	p.mu.Unlock()
}

// wait blocks until the job completes or ctx is done. It returns the job result
// once it completed, or incomplete wrapping ctx.Err() when the deadline expired
// first (the job keeps running).
func (j *claudeSubJob) wait(ctx context.Context, incomplete error) error {
	select {
	case <-j.done:
		return j.err
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", incomplete, ctx.Err())
	}
}

// startClaudeSubSession creates a session's private directory, optional MCP
// host, system-prompt file, child process and event router. Any failure tears
// down everything created so far and returns the error, so a partially started
// session never leaks a process, host or directory.
func startClaudeSubSession(ctx context.Context, p *ClaudeSubscriptionPool, key string, spec claudeSubStartSpec, cli claudeSubCLI) (*claudeSubSession, error) {
	dir, err := os.MkdirTemp("", "steiner-claudesub-*")
	if err != nil {
		return nil, fmt.Errorf("create claude_subscription session dir: %w", err)
	}
	s := &claudeSubSession{
		pool:        p,
		key:         key,
		advisor:     claudeSubSessionIsAdvisor(key),
		dir:         dir,
		model:       spec.Model,
		effort:      spec.Effort,
		sync:        &claudeSubSync{},
		call:        make(chan struct{}, 1),
		gone:        make(chan struct{}),
		lastUsed:    time.Now(),
		removeAll:   p.removeAll,
		closeHost:   p.closeHost,
		removeJob:   p.newCleanupJob(),
		hostJob:     p.newCleanupJob(),
		teardownJob: p.newCleanupJob(),
	}
	s.call <- struct{}{}

	// Secure the directory before it holds anything. A failure to make it 0o700
	// is a security failure, and the directory is already pool-owned: return the
	// partial session with the error so acquire's settlement transaction runs the
	// same independently bounded cleanup and unresolved ownership accounting as
	// every other partial start instead of removing it by hand and dropping it.
	// If that cleanup also fails, the pool keeps ownership, fails closed, and
	// Close reports the retained error.
	if err := p.chmod(dir, 0o700); err != nil {
		return s, fmt.Errorf("secure claude_subscription session dir: %w", err)
	}

	spawnOpts := claudeSubSpawnOptions{Model: spec.Model}
	spawnOpts.Effort, spawnOpts.ThinkingDisabled = claudeSubEffort(spec.Effort)

	// The MCP host and --mcp-config exist only when steiner has tools to
	// publish. An advisor session never publishes one, even if the caller
	// passed tools: its process is deliberately tool-less (D19).
	if !s.advisor && len(spec.Tools) > 0 {
		host, err := newClaudeSubMCPHost()
		if err != nil {
			return s, fmt.Errorf("start claude_subscription mcp host: %w", err)
		}
		s.host = host
		host.setTools(spec.Tools)
		mcpFile, err := host.writeConfig(dir)
		if err != nil {
			return s, err
		}
		spawnOpts.MCPConfigFile = mcpFile
	}

	promptFile, err := claudeSubWritePrivateFile(dir, "system.md", spec.SystemPrompt)
	if err != nil {
		return s, err
	}
	spawnOpts.SystemPromptFile = promptFile

	// Observe pool shutdown before launching: a startup canceled between locate
	// and spawn must not start a process.
	if err := ctx.Err(); err != nil {
		return s, err
	}

	spawnCtx := claudeSubContextWithSessionKey(ctx, key)
	conn, err := p.spawn(spawnCtx, cli.Path, claudeSubArgs(spawnOpts), claudeSubSessionEnv(os.Environ(), p.opts.ToolOutputMaxBytes), p.opts.WorkDir)
	if err != nil {
		return s, fmt.Errorf("start claude CLI: %w", err)
	}
	s.conn = conn

	s.control = newClaudeSubControl(conn)
	s.queue = newClaudeSubQueue()
	s.stop = make(chan struct{})
	s.routerDone = make(chan struct{})
	s.overageAbort = make(chan struct{})
	go func() {
		defer close(s.routerDone)
		claudeSubRoute(conn.Events(), s.stop, s.control, s.queue, s.observeEvent)
	}()
	return s, nil
}

// beginPendingCall registers id with the session's MCP host (when it has one)
// and records the opaque handle for later resolution, returning the handle. It
// returns nil for a tool-less session or a closing host. The caller must hold
// the whole-call lock (acquire holds it for the whole call).
func (s *claudeSubSession) beginPendingCall(id string) *claudeSubCall {
	if s.host == nil {
		return nil
	}
	call := s.host.beginCall(id)
	if call == nil {
		return nil
	}
	s.pending = append(s.pending, claudeSubPendingCall{ID: id, Handle: call})
	return call
}

// lockCall takes the session's whole-call lock, waiting until it is free, the
// session is torn down, or ctx is canceled. acquire is the only caller, and
// every successful lockCall is paired with release (or unlockCall on the
// reservation-rollback paths).
func (s *claudeSubSession) lockCall(ctx context.Context) error {
	// Reject an already-canceled request deterministically before the select:
	// otherwise a canceled caller could win the ready call token below and take
	// a lease it must not have. A cancellation after a successful admission is
	// revocation-after-admission and is not retroactively invalidated.
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-s.call:
		return nil
	case <-s.gone:
		return errClaudeSubPoolClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// unlockCall returns the whole-call lock.
func (s *claudeSubSession) unlockCall() {
	s.call <- struct{}{}
}

// isGone reports whether teardown has begun, which marks the session terminal.
func (s *claudeSubSession) isGone() bool {
	select {
	case <-s.gone:
		return true
	default:
		return false
	}
}

var errClaudeSubPaidExtraUsage = errors.New("claude_subscription stopped because paid extra usage was detected")

func (s *claudeSubSession) observeEvent(ev claudeSubEvent) {
	if !claudeSubEventIsOverage(ev) {
		return
	}
	s.overageOnce.Do(func() {
		s.overageMu.Lock()
		s.overageErr = errClaudeSubPaidExtraUsage
		close(s.overageAbort)
		s.overageMu.Unlock()
		go func() { _ = s.pool.Close() }()
	})
}

func (s *claudeSubSession) overage() error {
	s.overageMu.Lock()
	defer s.overageMu.Unlock()
	return s.overageErr
}

// close terminates the session once with a graceful teardown bounded by ctx. It
// is idempotent and safe to call concurrently.
func (s *claudeSubSession) close(ctx context.Context) error {
	return s.shutdown(ctx, false)
}

// forceClose is close with forced teardown: the CLI transport is ForceClosed so
// teardown fits the caller's deadline. Pool shutdown uses it so a parked call
// is interrupted instead of waited for.
func (s *claudeSubSession) forceClose(ctx context.Context) error {
	return s.shutdown(ctx, true)
}

// shutdown initiates the session's one-time teardown and returns its result.
// The single initiating caller runs teardown to completion under its own ctx
// (which the pool derives from its stored shutdown deadline once closing) and
// then publishes the job's durable result and signals completion together under
// the pool mutex; every later caller observes that result through the teardown
// job but waits only to its own ctx, returning an incomplete-teardown error if
// ctx expires first. No caller blocks behind a teardown already in progress, and
// that deadline-bounded wait is what lets pool shutdown return at its one
// absolute deadline even while an uncancelable teardown keeps running.
func (s *claudeSubSession) shutdown(ctx context.Context, force bool) error {
	if s.teardownStarted.CompareAndSwap(false, true) {
		// Register the in-progress teardown so final result assembly sees it as
		// unsettled while it runs, then run it bounded by the caller's ctx.
		s.pool.trackJob(s.teardownJob)
		s.teardownJob.finish(s.teardown(ctx, force))
		return s.teardownJob.err
	}
	return s.teardownJob.wait(ctx, errClaudeSubTeardownIncomplete)
}

func (s *claudeSubSession) teardown(ctx context.Context, force bool) error {
	// Waking every waiter first keeps a call parked on the whole-call lock from
	// outliving the teardown it is waiting behind.
	if s.gone != nil {
		close(s.gone)
	}
	var errs []error
	if s.stop != nil {
		close(s.stop)
	}
	// Terminate the transport BEFORE joining the router and the control writer.
	// The router calls control.close, which joins the control writer, which may
	// be blocked in conn.Send; only terminating the transport releases that
	// Send. Doing the transport termination first keeps teardown bounded instead
	// of waiting a whole deadline for the writer to unblock. It needs no session
	// call lock, so a parked call is never a prerequisite to teardown.
	if s.conn != nil {
		var err error
		if force {
			err = s.conn.ForceClose(ctx)
		} else {
			err = s.conn.Close(ctx)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if s.routerDone != nil && !claudeSubJoined(ctx, s.routerDone) {
		errs = append(errs, fmt.Errorf("stop claude_subscription router: %w", ctx.Err()))
	}
	if s.queue != nil {
		// Release any caller blocked on pop once the router has stopped.
		s.queue.close()
	}
	if s.control != nil && !claudeSubJoined(ctx, s.control.writerDone) {
		errs = append(errs, fmt.Errorf("stop claude_subscription control writer: %w", ctx.Err()))
	}
	if s.host != nil {
		// The host's Close has its own background grace and no context API, so
		// run it as the session's one-time host-close job and wait only to this
		// teardown's deadline. If the deadline expires first the job keeps
		// running: the session stays owned and this reports the teardown
		// incomplete rather than claiming the host was canceled.
		s.hostJob.start(func() error { return s.closeHost(s.host) })
		if err := s.hostJob.wait(ctx, errClaudeSubTeardownIncomplete); err != nil {
			errs = append(errs, fmt.Errorf("stop claude_subscription mcp host: %w", err))
		}
	}
	if s.dir != "" && s.removeAll != nil {
		// os.RemoveAll cannot be canceled, so run the removal as a one-time async
		// job and wait for it only up to this teardown's deadline. If the deadline
		// expires first the removal keeps running and this reports it as incomplete
		// instead of falsely claiming the directory is gone.
		s.removeJob.start(func() error { return s.removeAll(s.dir) })
		if err := s.removeJob.wait(ctx, errClaudeSubDirCleanupIncomplete); err != nil {
			errs = append(errs, fmt.Errorf("remove claude_subscription session dir: %w", err))
		}
	}
	return errors.Join(errs...)
}

// claudeSubWritePrivateFile writes content to dir/name as an exact-0o600 regular
// file. dir is the session's private 0o700 directory, created by start.
func claudeSubWritePrivateFile(dir, name, content string) (string, error) {
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create claude_subscription %s: %w", name, err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("set claude_subscription %s mode: %w", name, err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write claude_subscription %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close claude_subscription %s: %w", name, err)
	}
	return path, nil
}

// claudeSubSessionEnv builds the child environment for a session. When
// ToolOutputMaxBytes is zero steiner must not set MAX_MCP_OUTPUT_TOKENS at all,
// so the CLI keeps its own default; otherwise the shared helper maps the byte
// budget to the CLI's token budget.
func claudeSubSessionEnv(base []string, toolOutputMaxBytes int) []string {
	env := claudeSubChildEnv(base, toolOutputMaxBytes)
	if toolOutputMaxBytes <= 0 {
		env = claudeSubEnvWithout(env, "MAX_MCP_OUTPUT_TOKENS")
	}
	return env
}

// claudeSubEnvWithout returns env without any key=... entries for key.
func claudeSubEnvWithout(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
