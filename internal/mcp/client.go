// Package mcp connects steiner to Model Context Protocol servers over stdio
// or HTTP transports. It handles transport construction, MCP handshake,
// protocol negotiation, and server lifecycle management.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// DefaultConnectTimeout is the per-server MCP handshake timeout applied when
	// a server config leaves connect_timeout zero. It mirrors the config default
	// (15s) so direct ConnectSession callers get the same bound.
	DefaultConnectTimeout = 15 * time.Second

	// maxReconnectAttempts caps consecutive failed reconnect attempts per server
	// session (locked decision 2). The cap is reset by a successful reconnect;
	// once reached, the server is marked unavailable and no further reconnect
	// workers spawn.
	maxReconnectAttempts = 3
)

// clientVersion identifies the steiner MCP client during the handshake. The
// build version lives in cmd/steiner as main.version (injected via ldflags)
// and is not importable from internal packages.
const clientVersion = "dev"

// ServerSpec describes one MCP server to connect to. Transport selects which of the
// two field groups applies; config validation guarantees the other group is empty.
type ServerSpec struct {
	Name      string
	Transport string // "stdio" or "http"; empty is treated as "stdio"

	// stdio
	Command string
	Args    []string
	Env     map[string]string

	// http
	URL     string
	Headers map[string]string
}

// Session is a stable per-server lifecycle handle over one MCP server. The
// underlying SDK session can be swapped out by a background reconnect worker
// (after a transport error) without changing the handle, so tool definitions
// bound to this Session keep working across reconnects. All access to the
// current SDK session is guarded by mu: Call holds the mutex for the entire
// CallTool invocation so a concurrent reconnect swap or Close cannot race an
// in-flight call.
type Session struct {
	name            string
	tools           []*mcpsdk.Tool
	protocolVersion string

	// Reconnect configuration captured at connect time.
	spec    ServerSpec
	wrap    WrapFn
	release ReleaseFn
	stderr  io.Writer
	timeout time.Duration
	// managerCtx bounds reconnect attempt handshakes, binds reconnect server
	// processes, and gates session swaps: a swap never commits after it is
	// cancelled. For standalone sessions it is a background context so a
	// respawned process outlives the (timeout-bounded) connect context.
	managerCtx context.Context
	// onStatus, when non-nil, reports reconnect status transitions to the owner
	// (the Manager): reconnecting (Err nil), then connected (Err nil) or
	// unavailable (Err = the final attempt error). It runs outside mu, so the
	// report carries the transition's sequence for the owner to order.
	onStatus func(StatusReport)

	mu  sync.Mutex
	sdk *mcpsdk.ClientSession
	cmd *exec.Cmd

	// reconnect state, guarded by mu.
	reconnect           *reconnectAttempt // in-flight transition; nil when idle
	consecutiveFailures int               // consecutive failed reconnect attempts
	statusSeq           uint64            // last status transition sequence assigned
	closed              bool              // Close was called; failed calls no longer kick a reconnect
}

// reconnectAttempt is one reconnect transition's publication state. Outcome is
// written under mu before done is closed, so a waiter released by the close
// reads this attempt's outcome and can never observe a later attempt's.
type reconnectAttempt struct {
	done    chan struct{} // closed when the attempt resolves
	outcome error         // non-nil when the attempt ended unavailable
	// begin is the attempt's reconnecting transition, sequenced by
	// beginReconnectLocked under mu and reported by the worker outside it.
	begin StatusReport
}

// SessionOptions configures a Session at connect time. Its zero value is valid
// and gives the defaults described on ConnectSession.
type SessionOptions struct {
	// Release, when non-nil, releases resources owned by the wrapped server
	// command. Reconnect attempts reuse it.
	Release ReleaseFn
	// Stderr receives the stdio server's stderr. Reconnect attempts reuse it.
	Stderr io.Writer
	// Timeout bounds the connect handshake and the initial ListTools together.
	// A zero or negative value falls back to DefaultConnectTimeout. Reconnect
	// attempts reuse the defaulted value.
	Timeout time.Duration
	// ManagerCtx is the owning manager's context. When nil, standalone sessions
	// pin reconnect attempts to a background context (the connect context is
	// timeout-bounded and its expiry would kill a respawned process).
	ManagerCtx context.Context
	// OnStatus, when non-nil, is invoked from the reconnect worker on each
	// reconnect status transition. It runs outside the session mutex, so the
	// report's sequence orders transitions the owner receives out of order.
	OnStatus func(StatusReport)
}

// ConnectSession establishes a connection to an MCP server and completes the
// handshake. It dispatches to stdio or HTTP transport based on spec.Transport.
// For stdio, the server described by spec is launched, optionally wrapped (e.g.
// with the sandbox); for HTTP, a request is made to the configured endpoint.
// The negotiated protocol version and the server's tool list are captured;
// ListTools is called exactly once per session. opts.Timeout bounds the connect
// and the initial ListTools together; a zero or negative timeout falls back to
// DefaultConnectTimeout. opts.Release and opts.Stderr apply to the stdio command.
// opts.ManagerCtx and opts.OnStatus configure reconnect lifecycle behaviour; with
// the zero opts the session is self-contained (reconnects still run, bound to a
// background context, with no status reporting). A stdio server is killed when
// the bounded connect ctx expires, so a deadline-driven connect failure is
// bounded by the timeout rather than the timeout plus the transport's shutdown
// grace.
func ConnectSession(ctx context.Context, spec ServerSpec, wrap WrapFn, opts SessionOptions) (*Session, error) {
	return connectSession(ctx, spec, wrap, opts)
}

func connectSession(ctx context.Context, spec ServerSpec, wrap WrapFn, opts SessionOptions) (*Session, error) {
	release, stderr, timeout := opts.Release, opts.Stderr, opts.Timeout

	transport, cmd, releaseCommand, err := buildConnectTransport(ctx, spec, wrap, release, stderr)
	if err != nil {
		return nil, fmt.Errorf("connect mcp server %q: %w", spec.Name, err)
	}
	if releaseCommand != nil {
		defer releaseCommand()
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "steiner", Version: clientVersion}, nil)

	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// A stdio server that fails its handshake must be killed the moment the
	// connect deadline fires, not after the SDK's 5s terminate grace: the SDK's
	// own failed-handshake Close runs inside client.Connect, so the kill has to
	// be concurrent with it. The killOnTimeoutTransport arms the watcher after
	// the SDK's Start, so its exec.Cmd reads are ordered after Start's writes
	// (a goroutine armed before Connect would race them); disarming right after
	// Connect returns guarantees we can never return a live session whose
	// server we killed (if the deadline fired in that window, ListTools under
	// the done ctx fails and the session is discarded). cmd.Cancel is set on
	// every platform: CommandContext installs a Process.Kill fallback, and
	// applyProcessGroup upgrades it to a process-group kill on unix. The
	// wrapper therefore applies to stdio on every platform, killing the group
	// on unix and the single process elsewhere; the HTTP transport has no
	// command to kill.
	var killDone chan struct{}
	if cmd != nil && cmd.Cancel != nil {
		killDone = make(chan struct{})
		transport = &killOnTimeoutTransport{inner: transport, cmd: cmd, killDone: killDone}
	}

	session, err := client.Connect(ctx, transport, nil)
	if killDone != nil {
		close(killDone)
	}
	if err != nil {
		return nil, fmt.Errorf("connect mcp server %q: %w", spec.Name, err)
	}

	// Reconnect attempts must not be bound to the connect context: it is
	// timeout-bounded and its expiry would kill a respawned stdio process
	// (buildCommand binds the process to the context it was started with). The
	// manager pins this to its own context; standalone sessions pin it to a
	// background context.
	managerCtx := opts.ManagerCtx
	if managerCtx == nil {
		managerCtx = context.Background()
	}

	s := &Session{
		name:            spec.Name,
		sdk:             session,
		cmd:             cmd,
		protocolVersion: session.InitializeResult().ProtocolVersion,
		spec:            spec,
		wrap:            wrap,
		release:         release,
		stderr:          stderr,
		timeout:         timeout,
		managerCtx:      managerCtx,
		onStatus:        opts.OnStatus,
	}

	tools, err := listSessionTools(ctx, spec.Name, session, cmd)
	if err != nil {
		return nil, err
	}
	s.tools = tools
	return s, nil
}

// buildConnectTransport constructs the transport for spec. For HTTP, cmd and
// releaseCommand are nil. For stdio, releaseCommand is always non-nil on success.
func buildConnectTransport(ctx context.Context, spec ServerSpec, wrap WrapFn, release ReleaseFn, stderr io.Writer) (mcpsdk.Transport, *exec.Cmd, func(), error) {
	switch spec.Transport {
	case "http":
		transport, err := newHTTPTransport(spec)
		if err != nil {
			return nil, nil, nil, err
		}
		return transport, nil, nil, nil
	case "", "stdio":
		return newStdioTransport(ctx, spec, wrap, release, stderr)
	default:
		return nil, nil, nil, fmt.Errorf("unsupported transport %q", spec.Transport)
	}
}

// listSessionTools fetches the tool list for a freshly connected session. On
// failure the caller discards the session, so it kills cmd first and makes the
// close instant instead of waiting out the SDK's terminate grace.
func listSessionTools(ctx context.Context, name string, session *mcpsdk.ClientSession, cmd *exec.Cmd) ([]*mcpsdk.Tool, error) {
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		killDiscardedCommand(cmd)
		_ = session.Close() // Best-effort cleanup after a failed tool list.
		return nil, fmt.Errorf("list tools for mcp server %q: %w", name, err)
	}
	return result.Tools, nil
}

// killOnTimeoutTransport wraps a stdio transport so the server process group
// is killed the moment the connect deadline fires, instead of waiting out the
// SDK's terminate grace inside client.Connect's failed-handshake Close. The
// kill goroutine is armed after the inner Connect returns (which runs the
// SDK's Start), so its exec.Cmd reads are ordered after Start's writes; a
// goroutine armed before Connect would race them. killDone disarms the
// watcher; the owner closes it right after client.Connect returns.
//
// killOnTimeoutTransport must not be used for reconnect attempts: their
// transport is built against the manager context so a respawned process
// outlives the handshake deadline (reconnectOnce does not wrap).
type killOnTimeoutTransport struct {
	inner    mcpsdk.Transport
	cmd      *exec.Cmd
	killDone <-chan struct{}
}

func (t *killOnTimeoutTransport) Connect(ctx context.Context) (mcpsdk.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			killDiscardedCommand(t.cmd)
		case <-t.killDone:
		}
	}()
	return conn, nil
}

// killDiscardedCommand kills a discarded stdio command's process group so
// teardown of a session that will never be used is instant instead of waiting
// out the SDK's terminate grace. It is a no-op when the command was never
// started; errors are ignored because the kill may race a reap. It must only
// be called after the SDK's Start has run, and must not read cmd.ProcessState:
// that field is written by the SDK's Wait without synchronization.
func killDiscardedCommand(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cmd.Cancel != nil {
		_ = cmd.Cancel() // may race a reap
		return
	}
	_ = cmd.Process.Kill()
}

// Name returns the server name from the spec passed to Connect.
func (s *Session) Name() string {
	return s.name
}

// ProtocolVersion returns the protocol version negotiated during the handshake.
func (s *Session) ProtocolVersion() string {
	return s.protocolVersion
}

// Tools returns the tools advertised by the server at connect time.
func (s *Session) Tools() []*mcpsdk.Tool {
	return s.tools
}

// Call invokes a tool on the server. Tool-level failures (isError) surface as
// IsError on the result with a nil Go error; transport errors return non-nil.
//
// Call flow (D16, locked decisions 2-3): if a reconnect is in flight, block on
// its outcome future bounded by ctx; then snapshot the current SDK session
// under the mutex and dispatch on it WITHOUT holding the mutex, so concurrent
// calls run in parallel, a caller's ctx is always honoured, and Close or a
// reconnect swap never blocks behind a hung call (a swapped-out or closed SDK
// session just errors the in-flight call). On a classified transport error the
// call is NOT replayed: it returns a "disconnected, verify state" error and
// kicks a background reconnect (unless the consecutive-failure cap is already
// reached, in which case the server is unavailable and no worker spawns). The
// failure only counts against the session when the SDK session the call used is
// still current and the session is open; otherwise a concurrent call, reconnect
// or Close already handled it.
func (s *Session) Call(ctx context.Context, toolName string, args map[string]any) (*mcpsdk.CallToolResult, error) {
	if err := s.waitForReconnect(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	sdk := s.sdk
	s.mu.Unlock()

	result, err := sdk.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolName, Arguments: args})
	if err == nil {
		return result, nil
	}
	if !isTransportError(err) {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	disconnected := fmt.Errorf("mcp server %q disconnected; the call may or may not have been applied, verify state before retrying: %w", s.name, err)
	if s.closed || s.sdk != sdk {
		return nil, disconnected
	}
	if s.consecutiveFailures >= maxReconnectAttempts {
		// Cap already reached: the server is unavailable; never spawn
		// another worker.
		return nil, fmt.Errorf("mcp server %q unavailable after %d consecutive failed reconnect attempts; last failure: %w", s.name, maxReconnectAttempts, err)
	}
	if attempt := s.beginReconnectLocked(); attempt != nil {
		go s.reconnectWorker(attempt)
	}
	return nil, disconnected
}

// waitForReconnect blocks on the in-flight reconnect attempt, bounded by ctx
// (locked decision 3). It returns nil when no reconnect is pending or the
// reconnect succeeded, the attempt's outcome when it ended unavailable, and
// ctx.Err() when the caller gave up first.
func (s *Session) waitForReconnect(ctx context.Context) error {
	s.mu.Lock()
	attempt := s.reconnect
	s.mu.Unlock()
	if attempt == nil {
		return nil
	}
	return waitReconnectAttempt(ctx, attempt)
}

// waitReconnectAttempt blocks on one attempt's outcome future, bounded by ctx.
// The attempt publishes its outcome before closing done, so the channel close
// happens-before the receive here: a released waiter observes its own attempt's
// outcome even after the next attempt started, without taking the mutex. A
// cancelled ctx returns ctx.Err() and leaves the attempt untouched.
func waitReconnectAttempt(ctx context.Context, attempt *reconnectAttempt) error {
	select {
	case <-attempt.done:
		return attempt.outcome
	case <-ctx.Done():
		return ctx.Err()
	}
}

// beginReconnectLocked starts a reconnect transition unless one is already
// running or the consecutive-failure cap was reached (locked decision 2).
// Callers must hold mu. It returns the transition, whose future is also the
// ownership token for resolving it: the worker started with that attempt is the
// only one allowed to publish it (D16: the failed call is never replayed). A nil
// return means no transition started and no worker should run. The attempt's
// reconnecting report is sequenced here, in transition order, and reported by
// the worker outside the lock.
func (s *Session) beginReconnectLocked() *reconnectAttempt {
	if s.reconnect != nil {
		return nil
	}
	if s.consecutiveFailures >= maxReconnectAttempts {
		return nil // cap reached: server unavailable, no further workers
	}
	s.reconnect = &reconnectAttempt{
		done:  make(chan struct{}),
		begin: s.nextStatusLocked(ServerStatusReconnecting, nil),
	}
	return s.reconnect
}

// ownsReconnectLocked reports whether attempt is the in-flight reconnect
// transition. Callers must hold mu. Resolution is token-checked so a duplicate,
// superseded, or stale resolution can never publish an outcome for, or close the
// future of, a transition it did not begin.
func (s *Session) ownsReconnectLocked(attempt *reconnectAttempt) bool {
	return s.reconnect == attempt
}

// resolveReconnect publishes an attempt's outcome: recording it on the attempt,
// ending the in-flight transition, sequencing the terminal report, and closing
// the future happen in one critical section, so a waiter released by the close
// always observes this attempt's outcome. A token that no longer owns the
// transition is a no-op. Must not be called with the session mutex held, so
// status reporting stays outside the lock.
func (s *Session) resolveReconnect(attempt *reconnectAttempt, outcome error, status ServerStatus) {
	s.mu.Lock()
	if !s.ownsReconnectLocked(attempt) {
		s.mu.Unlock()
		return
	}
	s.reconnect = nil
	attempt.outcome = outcome
	report := s.nextStatusLocked(status, outcome)
	close(attempt.done)
	s.mu.Unlock()
	s.reportStatus(report)
}

// nextStatusLocked records one status transition and returns its report.
// Callers must hold mu. Assigning the sequence inside the lock orders the
// reports by the transitions they describe, not by when a slow callback
// delivers them.
func (s *Session) nextStatusLocked(status ServerStatus, err error) StatusReport {
	s.statusSeq++
	return StatusReport{Seq: s.statusSeq, Status: status, Err: err}
}

// commitReconnect adopts the freshly connected session of the attempt that began
// with attempt as its token. The SDK swap, the sequencing of the connected
// report, and the publication of the successful outcome happen in one critical
// section, so callers released by the closed future and Call snapshots taken
// afterwards both see the new session; the replaced session is returned for the
// caller to close outside the lock, and so is the connected report, which the
// caller delivers outside the lock. It reports committed=false when the manager
// context is cancelled, the session was closed, or attempt no longer owns the
// transition, leaving the live session, failure cap, and future untouched; the
// caller then closes next and resolves the abort through resolveReconnect.
func (s *Session) commitReconnect(attempt *reconnectAttempt, next *mcpsdk.ClientSession, cmd *exec.Cmd) (old *mcpsdk.ClientSession, report StatusReport, committed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.managerCtx.Err() != nil || s.closed || !s.ownsReconnectLocked(attempt) {
		return nil, StatusReport{}, false
	}
	old = s.sdk
	s.sdk = next
	s.cmd = cmd
	s.protocolVersion = next.InitializeResult().ProtocolVersion
	s.consecutiveFailures = 0 // a successful reconnect resets the cap
	s.reconnect = nil
	report = s.nextStatusLocked(ServerStatusConnected, nil)
	close(attempt.done)
	return old, report, true
}

// reportStatus forwards a reconnect status transition to the owner, if any. It
// must be called without the session mutex held.
func (s *Session) reportStatus(report StatusReport) {
	if s.onStatus != nil {
		s.onStatus(report)
	}
}

// closeOldSession closes a replaced SDK session in the background so the swap
// never blocks on teardown of a dead server; the SDK's Close is internally
// bounded (5s process-reap wait) so the goroutine cannot hang forever.
func closeOldSession(old *mcpsdk.ClientSession) {
	go func() {
		_ = old.Close()
	}()
}

// isTransportError reports whether err indicates the transport to the MCP
// server broke, as opposed to a tool-level, application, or context error.
// JSON-RPC application errors and context.Canceled/DeadlineExceeded are NOT
// transport errors; a server that died mid-call surfaces ErrConnectionClosed
// (with the underlying io error), and an HTTP session loss surfaces
// ErrSessionMissing.
func isTransportError(err error) bool {
	return errors.Is(err, mcpsdk.ErrConnectionClosed) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, mcpsdk.ErrSessionMissing)
}

// Close shuts the current SDK session down. For stdio transport, it reaps the
// server process. Safe to call concurrently with Call and reconnect swaps.
func (s *Session) Close() error {
	s.mu.Lock()
	s.closed = true
	sdk := s.sdk
	s.mu.Unlock()
	return sdk.Close()
}
