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
	wrap    func(*exec.Cmd) *exec.Cmd
	stderr  io.Writer
	timeout time.Duration
	// managerCtx bounds reconnect attempt handshakes, binds reconnect server
	// processes, and gates session swaps: a swap never commits after it is
	// cancelled. For standalone sessions it is a background context so a
	// respawned process outlives the (timeout-bounded) connect context.
	managerCtx context.Context
	// onStatus, when non-nil, reports reconnect status transitions to the owner
	// (the Manager): reconnecting (err nil), then connected (err nil) or
	// unavailable (err = the final attempt error).
	onStatus func(ServerStatus, error)

	mu  sync.Mutex
	sdk *mcpsdk.ClientSession
	cmd *exec.Cmd

	// reconnect state, guarded by mu.
	reconnecting        bool
	reconnectDone       chan struct{} // closed when the in-flight reconnect resolves
	reconnectOutcome    error         // non-nil when the reconnect ended unavailable
	consecutiveFailures int           // consecutive failed reconnect attempts
}

// SessionOptions configures the lifecycle behaviour of a Session.
type SessionOptions struct {
	// ManagerCtx is the owning manager's context. When nil, standalone sessions
	// pin reconnect attempts to a background context (the connect context is
	// timeout-bounded and its expiry would kill a respawned process).
	ManagerCtx context.Context
	// OnStatus, when non-nil, is invoked from the reconnect worker on each
	// reconnect status transition.
	OnStatus func(ServerStatus, error)
}

// ConnectSession establishes a connection to an MCP server and completes the
// handshake. It dispatches to stdio or HTTP transport based on spec.Transport.
// For stdio, the server described by spec is launched, optionally wrapped (e.g.
// with the sandbox); for HTTP, a request is made to the configured endpoint.
// The negotiated protocol version and the server's tool list are captured;
// ListTools is called exactly once per session. timeout bounds the connect and
// the initial ListTools together; a zero or negative timeout falls back to
// DefaultConnectTimeout. opts configures reconnect lifecycle behaviour; with no
// options the session is self-contained (reconnects still run, bound to a
// background context, with no status reporting).
func ConnectSession(ctx context.Context, spec ServerSpec, wrap func(*exec.Cmd) *exec.Cmd, stderr io.Writer, timeout time.Duration, opts ...SessionOptions) (*Session, error) {
	var opt SessionOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	var transport mcpsdk.Transport
	var cmd *exec.Cmd

	switch spec.Transport {
	case "http":
		var err error
		transport, err = newHTTPTransport(spec)
		if err != nil {
			return nil, fmt.Errorf("connect mcp server %q: %w", spec.Name, err)
		}
	case "", "stdio":
		transport, cmd = newStdioTransport(ctx, spec, wrap, stderr)
	default:
		return nil, fmt.Errorf("connect mcp server %q: unsupported transport %q", spec.Name, spec.Transport)
	}

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "steiner", Version: clientVersion}, nil)

	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect mcp server %q: %w", spec.Name, err)
	}

	// Reconnect attempts must not be bound to the connect context: it is
	// timeout-bounded and its expiry would kill a respawned stdio process
	// (buildCommand binds the process to the context it was started with). The
	// manager pins this to its own context; standalone sessions pin it to a
	// background context.
	managerCtx := opt.ManagerCtx
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
		stderr:          stderr,
		timeout:         timeout,
		managerCtx:      managerCtx,
		onStatus:        opt.OnStatus,
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		_ = session.Close() // Best-effort cleanup after a failed tool list.
		return nil, fmt.Errorf("list tools for mcp server %q: %w", spec.Name, err)
	}
	s.tools = tools.Tools
	return s, nil
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
// its outcome future bounded by ctx; then dispatch on the current SDK session
// while holding the session mutex for the whole invocation, so a concurrent
// reconnect swap or Close cannot race it. On a classified transport error the
// call is NOT replayed: it returns a "disconnected, verify state" error and
// kicks a background reconnect (unless the consecutive-failure cap is already
// reached, in which case the server is unavailable and no worker spawns).
func (s *Session) Call(ctx context.Context, toolName string, args map[string]any) (*mcpsdk.CallToolResult, error) {
	if err := s.waitForReconnect(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.sdk.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolName, Arguments: args})
	if err != nil {
		if !isTransportError(err) {
			return nil, err
		}
		if s.consecutiveFailures >= maxReconnectAttempts {
			// Cap already reached: the server is unavailable; never spawn
			// another worker.
			return nil, fmt.Errorf("mcp server %q unavailable after %d consecutive failed reconnect attempts; last failure: %w", s.name, maxReconnectAttempts, err)
		}
		s.kickReconnectLocked()
		return nil, fmt.Errorf("mcp server %q disconnected; the call may or may not have been applied, verify state before retrying: %w", s.name, err)
	}
	return result, nil
}

// waitForReconnect blocks on the in-flight reconnect outcome future, bounded by
// ctx (locked decision 3). It returns nil when no reconnect is pending or the
// reconnect succeeded, the reconnect outcome when it ended unavailable, and
// ctx.Err() when the caller gave up first.
func (s *Session) waitForReconnect(ctx context.Context) error {
	s.mu.Lock()
	if !s.reconnecting {
		s.mu.Unlock()
		return nil
	}
	done := s.reconnectDone
	s.mu.Unlock()

	select {
	case <-done:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.reconnectOutcome != nil {
			return s.reconnectOutcome
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// kickReconnectLocked starts the reconnect worker unless one is already running
// or the consecutive-failure cap was reached (locked decision 2). Callers must
// hold mu. The worker runs in the background; the failed call is never replayed
// (D16).
func (s *Session) kickReconnectLocked() {
	if s.reconnecting {
		return
	}
	if s.consecutiveFailures >= maxReconnectAttempts {
		return // cap reached: server unavailable, no further workers
	}
	s.reconnecting = true
	s.reconnectDone = make(chan struct{})
	s.reconnectOutcome = nil
	go s.reconnectWorker()
}

// reconnectWorker runs sequential fresh-connect attempts until one succeeds or
// the consecutive-failure cap is reached. At most one worker runs per server at
// a time (kickReconnectLocked guarantees it). The tool set is never re-listed
// (D14) and the failed call is never replayed (D16).
func (s *Session) reconnectWorker() {
	s.reportStatus(ServerStatusReconnecting, nil)

	for {
		if s.managerCtx.Err() != nil {
			// The owning manager is closed; abandon the reconnect and release
			// every caller blocked on the outcome future.
			s.resolveReconnect(fmt.Errorf("reconnect aborted: manager closed: %w", s.managerCtx.Err()), ServerStatusUnavailable)
			return
		}

		newSDK, newCmd, err := s.reconnectOnce()
		if err == nil {
			s.mu.Lock()
			if s.managerCtx.Err() != nil {
				s.mu.Unlock()
				_ = newSDK.Close()
				s.resolveReconnect(fmt.Errorf("reconnect aborted: manager closed: %w", s.managerCtx.Err()), ServerStatusUnavailable)
				return
			}
			old := s.sdk
			s.sdk = newSDK
			s.cmd = newCmd
			s.protocolVersion = newSDK.InitializeResult().ProtocolVersion
			s.consecutiveFailures = 0 // a successful reconnect resets the cap
			s.reconnecting = false
			s.reconnectOutcome = nil
			close(s.reconnectDone)
			s.mu.Unlock()
			if old != nil {
				closeOldSession(old)
			}
			s.reportStatus(ServerStatusConnected, nil)
			return
		}

		s.mu.Lock()
		s.consecutiveFailures++
		reachedCap := s.consecutiveFailures >= maxReconnectAttempts
		s.mu.Unlock()
		if reachedCap {
			s.resolveReconnect(fmt.Errorf("mcp server %q unavailable after %d consecutive failed reconnect attempts: %w", s.name, maxReconnectAttempts, err), ServerStatusUnavailable)
			return
		}
	}
}

// reconnectOnce performs one reconnect attempt: a fresh transport built with the
// original spec and process-group wrapping, then a new ClientSession via
// client.Connect re-running the handshake. It never calls ListTools (D14).
func (s *Session) reconnectOnce() (*mcpsdk.ClientSession, *exec.Cmd, error) {
	// The transport is bound to the manager context (mirroring ConnectSession),
	// not the attempt's timeout context, so a respawned stdio server process
	// outlives the handshake deadline.
	var transport mcpsdk.Transport
	var cmd *exec.Cmd
	switch s.spec.Transport {
	case "http":
		t, err := newHTTPTransport(s.spec)
		if err != nil {
			return nil, nil, fmt.Errorf("reconnect to mcp server %q: %w", s.name, err)
		}
		transport = t
	case "", "stdio":
		transport, cmd = newStdioTransport(s.managerCtx, s.spec, s.wrap, s.stderr)
	default:
		return nil, nil, fmt.Errorf("reconnect to mcp server %q: unsupported transport %q", s.name, s.spec.Transport)
	}

	connectCtx, cancel := context.WithTimeout(s.managerCtx, s.timeout)
	defer cancel()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "steiner", Version: clientVersion}, nil)
	sdk, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, cmd, fmt.Errorf("reconnect to mcp server %q: %w", s.name, err)
	}
	return sdk, cmd, nil
}

// resolveReconnect records the reconnect outcome and closes the outcome future,
// releasing every caller blocked on it exactly once. Must not be called with
// the session mutex held.
func (s *Session) resolveReconnect(outcome error, status ServerStatus) {
	s.mu.Lock()
	s.reconnecting = false
	s.reconnectOutcome = outcome
	close(s.reconnectDone)
	s.mu.Unlock()
	s.reportStatus(status, outcome)
}

// reportStatus forwards a reconnect status transition to the owner, if any.
func (s *Session) reportStatus(status ServerStatus, err error) {
	if s.onStatus != nil {
		s.onStatus(status, err)
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
	defer s.mu.Unlock()
	return s.sdk.Close()
}
