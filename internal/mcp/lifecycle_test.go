package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// TestLifecycleDeathReconnectNoReplay drives a full death + reconnect cycle
// through the Manager: a die call kills the server mid-request, the call fails
// with a disconnect error, the session reconnects in the background, and a
// subsequent echo succeeds on the fresh server. The failed die call must be
// received exactly once (D16: no replay) and the tool set must stay frozen
// across the cycle (D14: never re-list).
func TestLifecycleDeathReconnectNoReplay(t *testing.T) {
	fixtureBin := buildFixture(t)
	record := filepath.Join(t.TempDir(), "record.txt")

	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {
				Enabled: true,
				Command: fixtureBin,
				Env: map[string]string{
					"STEINER_FIXTURE_ENABLE_DIE": "1",
					"STEINER_FIXTURE_RECORD":     record,
				},
			},
		},
	}

	tr := &transitionRecorder{name: "fixture"}
	m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false,
		func(string) {}, func(string) {}, io.Discard, tr.onChange)
	tr.m.Store(m)
	defer m.Close() //nolint:errcheck
	waitInit(t, m)
	m.UpdateApprover(allowApprover())
	if st := stateByName(m.ServerStates(), "fixture"); st == nil || st.Status != ServerStatusConnected {
		t.Fatalf("initial state = %+v, want connected", st)
	}

	before := m.ToolDefs()
	if len(before) != 6 {
		t.Fatalf("ToolDefs() has %d tools, want 6", len(before))
	}

	// die exits the server before responding: the call must fail with a clear
	// disconnect error and must never be replayed.
	_, err := findTool(t, before, "mcp__fixture__die").Handler(context.Background(), nil)
	if err == nil {
		t.Fatal("die call returned nil error, want a disconnect error")
	}
	if !strings.Contains(err.Error(), "disconnected") || !strings.Contains(err.Error(), "verify state") {
		t.Errorf("die error %q does not explain the disconnect", err)
	}

	// A subsequent call either blocks on the in-flight reconnect or lands on
	// the reconnected session; either way it must succeed on the fresh server.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	env, err := findTool(t, m.ToolDefs(), "mcp__fixture__echo").Handler(ctx, map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("echo after reconnect returned Go error %v, want nil", err)
	}
	envelope, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("echo result type = %T, want tool.JSONEnvelope", env)
	}
	if !envelope.OK || envelope.Result != "hi" {
		t.Errorf("echo result = %+v, want OK with result %q", envelope, "hi")
	}

	// Tool set frozen: identical names and count before and after death +
	// reconnect (D14).
	after := m.ToolDefs()
	if len(after) != len(before) {
		t.Fatalf("tool count changed across reconnect: before=%d after=%d", len(before), len(after))
	}
	names := func(defs []tool.ToolDef) []string {
		out := make([]string, len(defs))
		for i, d := range defs {
			out[i] = d.Name
		}
		sort.Strings(out)
		return out
	}
	if !reflect.DeepEqual(names(before), names(after)) {
		t.Errorf("tool names changed across reconnect:\n before=%v\n after =%v", names(before), names(after))
	}

	// State machine: connected → reconnecting → connected, each transition
	// reported exactly once.
	if got := tr.waitFor(t); !reflect.DeepEqual(got, []ServerStatus{ServerStatusConnected, ServerStatusReconnecting, ServerStatusConnected}) {
		t.Errorf("status transitions = %v, want [connected reconnecting connected]", got)
	}

	// Request log: the failed die call was received exactly once (no replay),
	// the echo once, and the handshake ran twice (initial connect + reconnect).
	if got := countPrefix(t, record, "request_die="); got != 1 {
		t.Errorf("request_die count = %d, want 1 (no replay)", got)
	}
	if got := countPrefix(t, record, "request_echo="); got != 1 {
		t.Errorf("request_echo count = %d, want 1", got)
	}
	if got := countPrefix(t, record, "protocol_version="); got != 2 {
		t.Errorf("protocol_version count = %d, want 2 (initial connect + reconnect)", got)
	}
}

// TestLifecycleCallsBlockDuringReconnect proves locked decision 3: a call that
// arrives while a reconnect is in flight blocks on the reconnect outcome future
// and then succeeds, instead of racing the swap or failing against the dead
// session. The reconnect attempt is gated in the wrap func so the test controls
// exactly when it completes.
func TestLifecycleCallsBlockDuringReconnect(t *testing.T) {
	fixtureBin := buildFixture(t)

	// The initial spawn passes through; every later spawn (a reconnect
	// attempt) signals and then blocks until the test releases it.
	var spawns atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	releaseFn := func() { releaseOnce.Do(func() { close(release) }) }
	wrap := func(c *exec.Cmd) *exec.Cmd {
		if spawns.Add(1) > 1 {
			startedOnce.Do(func() { close(started) })
			<-release
		}
		return c
	}
	defer releaseFn()

	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {
				Enabled: true,
				Command: fixtureBin,
				Env:     map[string]string{"STEINER_FIXTURE_ENABLE_DIE": "1"},
			},
		},
	}

	tr := &transitionRecorder{name: "fixture"}
	m := Connect(context.Background(), cfg, config.LimitsConfig{}, wrap, false,
		func(string) {}, func(string) {}, io.Discard, tr.onChange)
	tr.m.Store(m)
	defer m.Close() //nolint:errcheck
	waitInit(t, m)
	m.UpdateApprover(allowApprover())
	if st := stateByName(m.ServerStates(), "fixture"); st == nil || st.Status != ServerStatusConnected {
		t.Fatalf("initial state = %+v, want connected", st)
	}

	_, err := findTool(t, m.ToolDefs(), "mcp__fixture__die").Handler(context.Background(), nil)
	if err == nil {
		t.Fatal("die call returned nil error, want a disconnect error")
	}

	// Wait until the reconnect attempt is mid-flight (blocked in the wrap), so
	// the echo call below is guaranteed to start while reconnecting.
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("reconnect attempt never started")
	}

	echoDef := findTool(t, m.ToolDefs(), "mcp__fixture__echo")
	type callOutcome struct {
		env any
		err error
	}
	echoDone := make(chan callOutcome, 1)
	go func() {
		env, err := echoDef.Handler(context.Background(), map[string]any{"text": "hi"})
		echoDone <- callOutcome{env, err}
	}()

	// The echo call must block while the reconnect is gated: it cannot complete
	// before the worker is released, so a bounded wait is a deterministic
	// assertion (locked decision 3).
	select {
	case <-echoDone:
		t.Fatal("echo returned while the reconnect attempt was in flight; it must block")
	case <-time.After(200 * time.Millisecond):
	}

	releaseFn()

	select {
	case out := <-echoDone:
		if out.err != nil {
			t.Fatalf("echo after reconnect returned Go error %v, want nil", out.err)
		}
		envelope, ok := out.env.(tool.JSONEnvelope)
		if !ok {
			t.Fatalf("echo result type = %T, want tool.JSONEnvelope", out.env)
		}
		if !envelope.OK || envelope.Result != "hi" {
			t.Errorf("echo result = %+v, want OK with result %q", envelope, "hi")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("echo never returned after the reconnect was released")
	}

	if got := tr.waitFor(t); !reflect.DeepEqual(got, []ServerStatus{ServerStatusConnected, ServerStatusReconnecting, ServerStatusConnected}) {
		t.Errorf("status transitions = %v, want [connected reconnecting connected]", got)
	}
}

// TestLifecycleReconnectCap proves locked decision 2: reconnect attempts are
// capped at 3 consecutive failures, the counter persists across the disconnect
// event, and once the cap is reached the server is unavailable and no further
// respawns happen. The fixture crash file makes every respawn exit immediately
// at startup, so each reconnect attempt fails fast; the start log counts the
// respawns.
func TestLifecycleReconnectCap(t *testing.T) {
	fixtureBin := buildFixture(t)
	dir := t.TempDir()
	crashFile := filepath.Join(dir, "crash.marker")
	startLog := filepath.Join(dir, "start.log")
	record := filepath.Join(dir, "record.txt")

	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {
				Enabled: true,
				Command: fixtureBin,
				Env: map[string]string{
					"STEINER_FIXTURE_ENABLE_DIE": "1",
					"STEINER_FIXTURE_RECORD":     record,
					"STEINER_FIXTURE_START_LOG":  startLog,
					"STEINER_FIXTURE_CRASH_FILE": crashFile,
				},
			},
		},
	}

	tr := &transitionRecorder{name: "fixture"}
	m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false,
		func(string) {}, func(string) {}, io.Discard, tr.onChange)
	tr.m.Store(m)
	defer m.Close() //nolint:errcheck
	waitInit(t, m)
	m.UpdateApprover(allowApprover())
	if st := stateByName(m.ServerStates(), "fixture"); st == nil || st.Status != ServerStatusConnected {
		t.Fatalf("initial state = %+v, want connected", st)
	}

	// Arm the crash loop after the initial connect: every respawn now exits
	// immediately, so reconnect attempts fail fast at the handshake.
	if err := os.WriteFile(crashFile, []byte("crash\n"), 0o600); err != nil {
		t.Fatalf("write crash file: %v", err)
	}

	_, err := findTool(t, m.ToolDefs(), "mcp__fixture__die").Handler(context.Background(), nil)
	if err == nil {
		t.Fatal("die call returned nil error, want a disconnect error")
	}

	// The worker burns through exactly 3 attempts, then the server is
	// unavailable: connected → reconnecting → unavailable.
	if got := tr.waitFor(t); !reflect.DeepEqual(got, []ServerStatus{ServerStatusConnected, ServerStatusReconnecting, ServerStatusUnavailable}) {
		t.Errorf("status transitions = %v, want [connected reconnecting unavailable]", got)
	}

	// Calls now fail with an unavailable error and never spawn another worker.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = findTool(t, m.ToolDefs(), "mcp__fixture__echo").Handler(ctx, nil)
	if err == nil {
		t.Fatal("echo after cap returned nil error, want an unavailable error")
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Errorf("echo error %q does not report the server as unavailable", err)
	}

	// Start log proves the respawn pattern: 1 initial + 3 attempts, and the
	// unavailable echo call above spawned nothing new.
	if got := countPrefix(t, startLog, "startup="); got != 4 {
		t.Errorf("startup count = %d, want 4 (initial + 3 attempts, no respawn after cap)", got)
	}

	// The failed die call was received exactly once; the post-cap echo never
	// reached any server (no replay, no further workers).
	if got := countPrefix(t, record, "request_die="); got != 1 {
		t.Errorf("request_die count = %d, want 1 (no replay)", got)
	}
	if got := countPrefix(t, record, "request_echo="); got != 0 {
		t.Errorf("request_echo count = %d, want 0 (echo never reached a server)", got)
	}
}

// TestLifecycleTimeoutNoReconnect proves a timed-out MCP call returns a usable
// error without triggering reconnect: the server stays connected, no process is
// respawned, and a follow-up call succeeds on the same session. The per-tool
// timeout entry (keyed by the full registered tool name) wins over the default.
func TestLifecycleTimeoutNoReconnect(t *testing.T) {
	fixtureBin := buildFixture(t)
	dir := t.TempDir()
	startLog := filepath.Join(dir, "start.log")
	record := filepath.Join(dir, "record.txt")

	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {
				Enabled: true,
				Command: fixtureBin,
				Env: map[string]string{
					"STEINER_FIXTURE_RECORD":    record,
					"STEINER_FIXTURE_START_LOG": startLog,
				},
			},
		},
	}
	limits := config.LimitsConfig{
		ToolTimeoutDefault: config.MustDuration("60s"),
		ToolTimeouts:       map[string]config.Duration{ToolName("fixture", "sleep"): config.MustDuration("50ms")},
	}

	tr := &transitionRecorder{name: "fixture"}
	m := Connect(context.Background(), cfg, limits, nil, false,
		func(string) {}, func(string) {}, io.Discard, tr.onChange)
	tr.m.Store(m)
	defer m.Close() //nolint:errcheck
	waitInit(t, m)
	m.UpdateApprover(allowApprover())
	if st := stateByName(m.ServerStates(), "fixture"); st == nil || st.Status != ServerStatusConnected {
		t.Fatalf("initial state = %+v, want connected", st)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := findTool(t, m.ToolDefs(), "mcp__fixture__sleep").Handler(ctx, map[string]any{"ms": float64(300)})
	if err == nil {
		t.Fatal("sleep call returned nil error, want a timeout error")
	}
	if !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("sleep error %q does not report the deadline", err)
	}

	// A deadline is not a transport error, so no reconnect: the status sequence
	// stays at the single initial connected transition and the start log counts
	// one spawn. Give any (wrong) reconnect worker time to report before
	// asserting the sequence.
	time.Sleep(200 * time.Millisecond)
	tr.mu.Lock()
	seq := append([]ServerStatus(nil), tr.seq...)
	tr.mu.Unlock()
	if !reflect.DeepEqual(seq, []ServerStatus{ServerStatusConnected}) {
		t.Errorf("status transitions = %v, want [connected] (timeout must not trigger reconnect)", seq)
	}
	if got := countPrefix(t, startLog, "startup="); got != 1 {
		t.Errorf("startup count = %d, want 1 (timeout must not respawn the server)", got)
	}

	// The server is still usable: the follow-up echo lands on the same process
	// once the fixture finishes its sleep.
	env, err := findTool(t, m.ToolDefs(), "mcp__fixture__echo").Handler(ctx, map[string]any{"text": "still alive"})
	if err != nil {
		t.Fatalf("echo after timeout returned Go error %v, want nil", err)
	}
	envelope, ok := env.(tool.JSONEnvelope)
	if !ok {
		t.Fatalf("echo result type = %T, want tool.JSONEnvelope", env)
	}
	if !envelope.OK || envelope.Result != "still alive" {
		t.Errorf("echo result = %+v, want OK with %q", envelope, "still alive")
	}

	// The sleep call reached the server exactly once; the echo once; a single
	// handshake (no reconnect).
	if got := countPrefix(t, record, "request_sleep="); got != 1 {
		t.Errorf("request_sleep count = %d, want 1", got)
	}
	if got := countPrefix(t, record, "request_echo="); got != 1 {
		t.Errorf("request_echo count = %d, want 1", got)
	}
	if got := countPrefix(t, record, "protocol_version="); got != 1 {
		t.Errorf("protocol_version count = %d, want 1 (no reconnect)", got)
	}
}

// TestIsTransportError pins the transport-error classifier: connection-closed,
// EOF, unexpected-EOF, and session-missing errors are transport failures;
// context cancellation/deadline and JSON-RPC application errors are not.
func TestIsTransportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "ErrConnectionClosed", err: fmt.Errorf("wrapped: %w", mcpsdk.ErrConnectionClosed), want: true},
		{name: "raw io.EOF", err: io.EOF, want: true},
		{name: "wrapped io.EOF", err: fmt.Errorf("read: %w", io.EOF), want: true},
		{name: "io.ErrUnexpectedEOF", err: io.ErrUnexpectedEOF, want: true},
		{name: "ErrSessionMissing", err: fmt.Errorf("session gone: %w", mcpsdk.ErrSessionMissing), want: true},
		{name: "context.DeadlineExceeded", err: context.DeadlineExceeded, want: false},
		{name: "context.Canceled", err: context.Canceled, want: false},
		{name: "jsonrpc application error", err: errors.New(`calling "tools/call": method not found`), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransportError(tt.err); got != tt.want {
				t.Errorf("isTransportError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestLifecycleHTTPReconnect proves reconnect on HTTP session loss: the test
// server 404s the current session's next message POST (the SDK surfaces this as
// ErrSessionMissing), the call fails as a disconnect, and a fresh session is
// established on reconnect.
func TestLifecycleHTTPReconnect(t *testing.T) {
	server, drop := newDropSessionMCPServer(t)
	defer server.Close()

	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"remote": {
				Enabled:   true,
				Transport: "http",
				URL:       server.URL,
			},
		},
	}

	tr := &transitionRecorder{name: "remote"}
	m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false,
		func(string) {}, func(string) {}, io.Discard, tr.onChange)
	tr.m.Store(m)
	defer m.Close() //nolint:errcheck
	waitInit(t, m)
	m.UpdateApprover(allowApprover())
	if st := stateByName(m.ServerStates(), "remote"); st == nil || st.Status != ServerStatusConnected {
		t.Fatalf("initial state = %+v, want connected", st)
	}
	call := func(text string) (tool.JSONEnvelope, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		env, err := findTool(t, m.ToolDefs(), "mcp__remote__test_tool").Handler(ctx, map[string]any{"text": text})
		if err != nil {
			return tool.JSONEnvelope{}, err
		}
		envelope, ok := env.(tool.JSONEnvelope)
		if !ok {
			t.Fatalf("test_tool result type = %T, want tool.JSONEnvelope", env)
		}
		return envelope, nil
	}

	envelope, err := call("hello")
	if err != nil {
		t.Fatalf("first test_tool call: %v", err)
	}
	if !envelope.OK || envelope.Result != "hello" {
		t.Fatalf("first call result = %+v, want OK with %q", envelope, "hello")
	}

	// Terminate the current session server-side: the next POST carrying its
	// session ID gets a 404, which the SDK maps to ErrSessionMissing.
	drop.dropNext(drop.lastSessionID())

	if _, err := call("dropped"); err == nil {
		t.Fatal("call after session drop returned nil error, want a disconnect error")
	}

	// The reconnect must land on a fresh session: wait for the state machine
	// to complete connected → reconnecting → connected, then call again.
	if got := tr.waitFor(t); !reflect.DeepEqual(got, []ServerStatus{ServerStatusConnected, ServerStatusReconnecting, ServerStatusConnected}) {
		t.Errorf("status transitions = %v, want [connected reconnecting connected]", got)
	}

	envelope, err = call("again")
	if err != nil {
		t.Fatalf("test_tool call after HTTP reconnect: %v", err)
	}
	if !envelope.OK || envelope.Result != "again" {
		t.Errorf("call after reconnect result = %+v, want OK with %q", envelope, "again")
	}
}

// TestLifecycleShutdownReapsChildren proves Manager.Close terminates every
// server and their spawned grandchildren concurrently: two servers, one of
// which spawned a sleep-600 child, shut down within the shutdown grace and the
// child's process group is reaped.
func TestLifecycleShutdownReapsChildren(t *testing.T) {
	fixtureBin := buildFixture(t)
	childPIDFile := filepath.Join(t.TempDir(), "child.pid")

	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"alpha": {Enabled: true, Command: fixtureBin},
			"beta": {
				Enabled: true,
				Command: fixtureBin,
				Env: map[string]string{
					"STEINER_FIXTURE_SPAWN_CHILD":    "1",
					"STEINER_FIXTURE_CHILD_PID_FILE": childPIDFile,
				},
			},
		},
	}

	m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	waitInit(t, m)

	pidData, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatalf("read child pid file: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", pidData, err)
	}

	start := time.Now()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > shutdownGrace {
		t.Errorf("Close took %v, want it bounded by the shutdown grace (%v)", elapsed, shutdownGrace)
	}

	// The sleep-600 grandchild sits in the server's process group; Close must
	// have killed and reaped it (applyProcessGroup SIGKILLs the whole group on
	// manager-context cancellation).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(childPID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("grandchild %d still alive after Close", childPID)
}

// TestLifecycleCloseCancelsInFlightConnect proves Close does not wait for a
// server whose handshake is stuck: the stall fixture sleeps for an hour inside
// initialize, but cancelling the manager context kills its process group, so
// Close returns within the shutdown grace instead of blocking on the stall.
func TestLifecycleCloseCancelsInFlightConnect(t *testing.T) {
	fixtureBin := buildFixture(t)

	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"stall": {
				Enabled:        true,
				Command:        fixtureBin,
				Env:            map[string]string{"STEINER_FIXTURE_STALL_HANDSHAKE": "1"},
				ConnectTimeout: config.MustDuration("1h"),
			},
			"fast": {Enabled: true, Command: fixtureBin},
		},
	}

	m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	defer m.Close() //nolint:errcheck

	// The fast server must reach connected while the stall server is still
	// stuck in its hour-long handshake, so Close below runs with a connect in
	// flight.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if st := stateByName(m.ServerStates(), "fast"); st != nil && st.Status == ServerStatusConnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fast server never connected; states=%+v", m.ServerStates())
		}
		time.Sleep(5 * time.Millisecond)
	}

	start := time.Now()
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > shutdownGrace {
		t.Errorf("Close took %v while a stall handshake was in flight, want <= %v", elapsed, shutdownGrace)
	}
}

// TestLifecycleCloseIdempotent proves Close can be called twice: the second
// call returns the first call's (filtered) result without re-running shutdown.
func TestLifecycleCloseIdempotent(t *testing.T) {
	fixtureBin := buildFixture(t)
	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {Enabled: true, Command: fixtureBin},
		},
	}

	m := Connect(context.Background(), cfg, config.LimitsConfig{}, nil, false, func(string) {}, func(string) {}, io.Discard, nil)
	waitInit(t, m)

	if err := m.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestLifecycleCloseDuringReconnect proves Close is safe while a reconnect is
// in flight: the gated reconnect attempt is released after Close, observes the
// cancelled manager context, and aborts to unavailable without panicking,
// publishing a connected state, or completing a fresh handshake (no leak).
func TestLifecycleCloseDuringReconnect(t *testing.T) {
	fixtureBin := buildFixture(t)
	record := filepath.Join(t.TempDir(), "record.txt")

	// The initial spawn passes through; every later spawn (a reconnect
	// attempt) signals and then blocks until the test releases it.
	var spawns atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	releaseFn := func() { releaseOnce.Do(func() { close(release) }) }
	wrap := func(c *exec.Cmd) *exec.Cmd {
		if spawns.Add(1) > 1 {
			startedOnce.Do(func() { close(started) })
			<-release
		}
		return c
	}
	defer releaseFn()

	cfg := config.MCPConfig{
		Enabled: true,
		Servers: map[string]config.MCPServerConfig{
			"fixture": {
				Enabled: true,
				Command: fixtureBin,
				Env:     map[string]string{"STEINER_FIXTURE_ENABLE_DIE": "1", "STEINER_FIXTURE_RECORD": record},
			},
		},
	}

	tr := &transitionRecorder{name: "fixture"}
	m := Connect(context.Background(), cfg, config.LimitsConfig{}, wrap, false,
		func(string) {}, func(string) {}, io.Discard, tr.onChange)
	tr.m.Store(m)
	waitInit(t, m)
	m.UpdateApprover(allowApprover())
	if st := stateByName(m.ServerStates(), "fixture"); st == nil || st.Status != ServerStatusConnected {
		t.Fatalf("initial state = %+v, want connected", st)
	}

	// Kill the server so a reconnect attempt starts and blocks in the wrap.
	if _, err := findTool(t, m.ToolDefs(), "mcp__fixture__die").Handler(context.Background(), nil); err == nil {
		t.Fatal("die call returned nil error, want a disconnect error")
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("reconnect attempt never started")
	}

	// Close while the reconnect is mid-flight, then release it to finish.
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	releaseFn()

	// The reconnect must abort to unavailable: connected → reconnecting →
	// unavailable, and never connected again after Close.
	if got := tr.waitFor(t); !reflect.DeepEqual(got, []ServerStatus{ServerStatusConnected, ServerStatusReconnecting, ServerStatusUnavailable}) {
		t.Errorf("status transitions = %v, want [connected reconnecting unavailable]", got)
	}
	for _, st := range m.ServerStates() {
		if st.Status == ServerStatusConnected {
			t.Errorf("server %q reports connected after Close; a closing manager must not republish sessions", st.Name)
		}
	}

	// The gated reconnect never completed a handshake: exactly one
	// protocol_version line (the initial connect), so no second server process
	// leaked into service.
	if got := countPrefix(t, record, "protocol_version="); got != 1 {
		t.Errorf("protocol_version count = %d, want 1 (the gated reconnect must not complete)", got)
	}
}

// transitionRecorder captures the status transitions of one server from
// onStateChange callbacks. name must be set before Connect (the callback may
// fire from the connect goroutine); the manager is stored afterwards via an
// atomic pointer. The store lands before the connect goroutine can reach its
// first callback (the initial connect spawns a process and runs a handshake,
// which takes far longer than the store), so the full sequence including the
// initial connected transition is recorded; the nil check is defensive only.
type transitionRecorder struct {
	mu   sync.Mutex
	m    atomic.Pointer[Manager]
	name string
	seq  []ServerStatus
}

func (r *transitionRecorder) onChange() {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.m.Load()
	if m == nil {
		return
	}
	if st := stateByName(m.ServerStates(), r.name); st != nil {
		r.seq = append(r.seq, st.Status)
	}
}

// waitFor blocks until at least 3 transitions are recorded, then returns
// the recorded sequence. The recorder appends after the manager state write
// that triggers onStateChange, so observing the final transition is race-free.
func (r *transitionRecorder) waitFor(t *testing.T) []ServerStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r.mu.Lock()
		seq := append([]ServerStatus(nil), r.seq...)
		r.mu.Unlock()
		if len(seq) >= 3 {
			return seq
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for 3 status transitions; got %v", seq)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// countPrefix counts lines in path that start with prefix.
func countPrefix(t *testing.T, path, prefix string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// dropSessionHandler forwards requests to an MCP HTTP handler but answers 404
// to every POST carrying a configured session ID, simulating a server that
// terminated that session (the SDK maps 404 to ErrSessionMissing).
type dropSessionHandler struct {
	next http.Handler

	mu     sync.Mutex
	lastID string
	dropID string
}

func (h *dropSessionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sid := r.Header.Get("Mcp-Session-Id")
	if r.Method == http.MethodPost && sid != "" {
		h.mu.Lock()
		h.lastID = sid
		drop := sid == h.dropID
		h.mu.Unlock()
		if drop {
			http.Error(w, "session terminated", http.StatusNotFound)
			return
		}
	}
	h.next.ServeHTTP(w, r)
}

func (h *dropSessionHandler) dropNext(sid string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropID = sid
}

func (h *dropSessionHandler) lastSessionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastID
}

// newDropSessionMCPServer builds an HTTP MCP server (one echo-style test_tool)
// behind a dropSessionHandler so tests can terminate sessions server-side.
func newDropSessionMCPServer(t *testing.T) (*httptest.Server, *dropSessionHandler) {
	t.Helper()
	var drop *dropSessionHandler
	ts := newTestMCPServer(t, withRequestObservation(func(next http.Handler) http.Handler {
		drop = &dropSessionHandler{next: next}
		return drop
	}))
	return ts, drop
}
