package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaudeSubSessionStartWritesPrivateFilesAndArgs(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{ToolOutputMaxBytes: 65536, WorkDir: "/work"})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "read",
			Description: "read a file",
			Parameters:  map[string]any{"type": "object"},
		},
	}}

	s, err := pool.acquire(context.Background(), "parent", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)

	if info, err := os.Stat(s.dir); err != nil {
		t.Fatalf("stat session dir: %v", err)
	} else if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("session dir mode = %o, want 0700", got)
	}
	for _, name := range []string{"system.md", "mcp.json"} {
		info, err := os.Stat(filepath.Join(s.dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %o, want 0600", name, got)
		}
	}

	call := spawner.call(t, 0)
	if call.Path != "/fake/claude" {
		t.Errorf("spawn path = %q, want /fake/claude", call.Path)
	}
	if call.Dir != "/work" {
		t.Errorf("spawn dir = %q, want /work", call.Dir)
	}
	if got, _ := claudeSubArgValue(call.Args, "--model"); got != spec.Model {
		t.Errorf("--model = %q, want %q", got, spec.Model)
	}
	if got, _ := claudeSubArgValue(call.Args, "--effort"); got != "high" {
		t.Errorf("--effort = %q, want high", got)
	}
	if got, _ := claudeSubArgValue(call.Args, "--system-prompt-file"); got != filepath.Join(s.dir, "system.md") {
		t.Errorf("--system-prompt-file = %q, want %q", got, filepath.Join(s.dir, "system.md"))
	}
	if got, _ := claudeSubArgValue(call.Args, "--mcp-config"); got != filepath.Join(s.dir, "mcp.json") {
		t.Errorf("--mcp-config = %q, want %q", got, filepath.Join(s.dir, "mcp.json"))
	}
	if got, _ := claudeSubEnvValue(call.Env, "MAX_MCP_OUTPUT_TOKENS"); got != "32768" {
		t.Errorf("MAX_MCP_OUTPUT_TOKENS = %q, want 32768", got)
	}
}

func TestClaudeSubSessionAdvisorOmitsMCPConfig(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), claudeSubAdvisorSessionKey("parent"), claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)

	if !s.advisor {
		t.Error("advisor session was not classified as advisor")
	}
	if s.host != nil {
		t.Error("advisor session created an MCP host")
	}
	call := spawner.call(t, 0)
	if claudeSubHasArg(call.Args, "--mcp-config") {
		t.Error("advisor session passed --mcp-config")
	}
	if claudeSubHasArg(call.Args, "--allowedTools") {
		t.Error("advisor session passed --allowedTools")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "mcp.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("advisor session wrote an mcp.json (stat err = %v)", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "system.md")); err != nil {
		t.Errorf("advisor system.md missing: %v", err)
	}
}

func TestClaudeSubSessionStartFailureCleansUp(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)

	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spawner.setErr(errors.New("spawn failed"))
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}

	if _, err := pool.acquire(context.Background(), "parent", spec); err == nil {
		t.Fatal("acquire with a failing spawn must return an error")
	}
	pool.mu.Lock()
	n := len(pool.sessions)
	pool.mu.Unlock()
	if n != 0 {
		t.Errorf("pool kept %d sessions after a start failure", n)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("partial start left %v in the temp dir", names)
	}
}

func TestClaudeSubSessionCloseIdempotent(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	conn := spawner.call(t, 0).Conn

	if err := s.close(context.Background()); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.close(context.Background()); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if !conn.isClosed() {
		t.Error("connection was not closed")
	}
	if _, err := os.Stat(s.dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("session dir not removed (stat err = %v)", err)
	}
}

func TestClaudeSubSessionRouterForwardsAndStops(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	conn := spawner.call(t, 0).Conn

	conn.push(claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant"}`)})
	popCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ev, ok := s.queue.pop(popCtx)
	if !ok {
		t.Fatal("router did not forward an event to the queue")
	}
	if ev.Type != "assistant" {
		t.Errorf("forwarded event type = %q, want assistant", ev.Type)
	}

	if err := s.close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-s.routerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("router did not stop after close")
	}
	if _, ok := s.queue.pop(popCtx); ok {
		t.Error("queue still yields events after close")
	}
}

func TestClaudeSubSessionRouterStaysActiveWhileToolParked(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), "parent", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	conn := spawner.call(t, 0).Conn

	// Park a tool call the way a call would while it awaits a result.
	if handle := s.beginPendingCall("toolu_1"); handle == nil {
		t.Fatal("beginPendingCall returned nil")
	}

	// The router must keep forwarding events even with a tool parked.
	conn.push(claudeSubEvent{Type: "assistant", Raw: json.RawMessage(`{"type":"assistant"}`)})
	popCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ev, ok := s.queue.pop(popCtx)
	if !ok || ev.Type != "assistant" {
		t.Fatalf("router stopped forwarding while a tool was parked (ok=%v, type=%q)", ok, ev.Type)
	}
}

func TestClaudeSubSessionPendingHandles(t *testing.T) {
	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), "parent", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)

	handle := s.beginPendingCall("toolu_1")
	if handle == nil {
		t.Fatal("beginPendingCall returned nil for a session with a host")
	}
	if handle.id != "toolu_1" {
		t.Errorf("handle id = %q, want toolu_1", handle.id)
	}
	if len(s.pending) != 1 {
		t.Fatalf("pending calls = %d, want 1", len(s.pending))
	}
	if s.pending[0].ID != "toolu_1" || s.pending[0].Handle != handle {
		t.Errorf("pending call = %+v, want id toolu_1 and the returned handle", s.pending[0])
	}
}

func TestClaudeSubSessionPendingHandlesToolLess(t *testing.T) {
	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	s, err := pool.acquire(context.Background(), claudeSubAdvisorSessionKey("parent"), claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)

	if handle := s.beginPendingCall("toolu_1"); handle != nil {
		t.Error("tool-less session must not register a pending call")
	}
	if len(s.pending) != 0 {
		t.Errorf("tool-less session recorded %d pending calls", len(s.pending))
	}
}

func TestClaudeSubSessionAdvisorWithToolsOmitsMCP(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), claudeSubAdvisorSessionKey("parent"), spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer pool.release(s)

	if s.host != nil {
		t.Error("advisor session created an MCP host despite non-empty Tools")
	}
	call := spawner.call(t, 0)
	if claudeSubHasArg(call.Args, "--mcp-config") {
		t.Error("advisor session passed --mcp-config despite non-empty Tools")
	}
	if claudeSubHasArg(call.Args, "--allowedTools") {
		t.Error("advisor session passed --allowedTools despite non-empty Tools")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "mcp.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("advisor session wrote an mcp.json (stat err = %v)", err)
	}
}

func TestClaudeSubSessionToolOutputBudgetEnv(t *testing.T) {
	cases := []struct {
		name    string
		bytes   int
		wantSet bool
		want    string
	}{
		{name: "zero omits the variable", bytes: 0, wantSet: false},
		{name: "nonzero sets the token budget", bytes: 65536, wantSet: true, want: "32768"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{ToolOutputMaxBytes: tc.bytes})
			spec := claudeSubTestSpec()
			spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
			s, err := pool.acquire(context.Background(), "parent", spec)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			defer pool.release(s)

			got, ok := claudeSubEnvValue(spawner.call(t, 0).Env, "MAX_MCP_OUTPUT_TOKENS")
			if ok != tc.wantSet {
				t.Fatalf("MAX_MCP_OUTPUT_TOKENS present = %v, want %v", ok, tc.wantSet)
			}
			if tc.wantSet && got != tc.want {
				t.Errorf("MAX_MCP_OUTPUT_TOKENS = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestClaudeSubSessionCallerCancelledSpawnNeverPublishes proves a startup whose
// spawn ignores context never publishes a session after its caller canceled: the
// completed session is torn down locally, acquire returns the caller's
// cancellation, no session remains published, and a later acquisition still
// starts a fresh session while the pool stays running.
func TestClaudeSubSessionCallerCancelledSpawnNeverPublishes(t *testing.T) {
	var spawns atomic.Int32
	spawnEntered := make(chan struct{})
	spawnRelease := make(chan struct{})
	spawned := make(chan *claudeSubFakeConn, 2)
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		if spawns.Add(1) == 1 {
			close(spawnEntered)
			<-spawnRelease // deliberately ignores context cancellation
		}
		conn := newClaudeSubFakeConn()
		spawned <- conn
		return conn, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	acquireErr := make(chan error, 1)
	go func() {
		_, err := pool.acquire(ctx, "parent", claudeSubTestSpec())
		acquireErr <- err
	}()
	select {
	case <-spawnEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("spawn was never entered")
	}

	// Cancel the caller, then let the spawn (which ignores context) succeed.
	cancel()
	close(spawnRelease)

	select {
	case err := <-acquireErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("caller-cancelled spawn acquire = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("caller-cancelled spawn acquire did not settle")
	}

	first := <-spawned
	if !first.isClosed() {
		t.Error("caller-cancelled startup session was not torn down locally")
	}
	pool.mu.Lock()
	published := len(pool.sessions)
	pool.mu.Unlock()
	if published != 0 {
		t.Errorf("caller-cancelled startup published %d sessions, want 0", published)
	}

	// The pool is still running: a later acquisition starts and succeeds.
	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("later acquire: %v", err)
	}
	pool.release(s)
	if got := spawns.Load(); got != 2 {
		t.Fatalf("spawn called %d times, want 2", got)
	}
}

// TestClaudeSubSessionCallerCancelledCleanupFailureFailsPoolClosed proves a
// cleanup failure for a caller-cancelled, post-spawn, never-published session is
// a pool-owned safety failure, not a transient acquisition failure: the caller
// sees its cancellation joined with the cleanup error, the pool fails closed so
// no later acquire proceeds while the resource's fate is uncertain, and Close
// returns the same non-nil result from the pool-owned accounting.
func TestClaudeSubSessionCallerCancelledCleanupFailureFailsPoolClosed(t *testing.T) {
	cleanupErr := errors.New("local teardown failed")
	spawnEntered := make(chan struct{})
	spawnRelease := make(chan struct{})
	var spawned *claudeSubFakeConn
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		close(spawnEntered)
		<-spawnRelease // deliberately ignores context cancellation
		spawned = newClaudeSubFakeConn()
		spawned.closeErr = cleanupErr
		return spawned, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	acquireErr := make(chan error, 1)
	go func() {
		_, err := pool.acquire(ctx, "parent", claudeSubTestSpec())
		acquireErr <- err
	}()
	select {
	case <-spawnEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("spawn was never entered")
	}

	cancel()
	close(spawnRelease)

	select {
	case err := <-acquireErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("acquire = %v, want context.Canceled", err)
		}
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("acquire = %v, want the cleanup failure joined in", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not settle")
	}
	if !spawned.isForceClosed() {
		t.Error("unpublished session was not force-closed locally")
	}

	// The pool must fail closed while the resource's fate is uncertain.
	if _, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec()); !errors.Is(err, errClaudeSubPoolClosed) {
		t.Fatalf("acquire after cleanup failure = %v, want errClaudeSubPoolClosed", err)
	}

	if err := pool.Close(); !errors.Is(err, cleanupErr) {
		t.Fatalf("Close = %v, want it to report the cleanup failure", err)
	}
}

// TestClaudeSubSessionChmodFailureCleansUp proves a failure to secure the freshly
// created session directory is handled through the same bounded cleanup as every
// other partial start: the caller sees the security error, the directory is
// removed so it is never left outside safety accounting, and the pool stays
// running because no resource's fate is left uncertain.
func TestClaudeSubSessionChmodFailureCleansUp(t *testing.T) {
	chmodErr := errors.New("chmod failed")

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)

	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	pool.chmod = func(string, os.FileMode) error { return chmodErr }

	_, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if !errors.Is(err, chmodErr) {
		t.Fatalf("acquire = %v, want the security failure", err)
	}
	if n := spawner.callCount(); n != 0 {
		t.Fatalf("spawn called %d times, want 0 (the directory failed to secure)", n)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("early-dir cleanup left %v in the temp dir", entries)
	}

	pool.mu.Lock()
	state := pool.state
	pool.mu.Unlock()
	if state != claudeSubPoolRunning {
		t.Fatalf("pool state = %d, want running after a successful early-dir cleanup", state)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close = %v, want nil after a successful early-dir cleanup", err)
	}
}

// TestClaudeSubSessionEarlyDirCleanupFailureFailsPoolClosed proves that when
// securing the early session directory fails AND its independently bounded
// cleanup also fails, the caller sees both errors, the pool owns the unresolved
// directory (never leaving it outside safety accounting), fails closed so no
// later acquire proceeds, and Close reports the retained failure.
func TestClaudeSubSessionEarlyDirCleanupFailureFailsPoolClosed(t *testing.T) {
	chmodErr := errors.New("chmod failed")
	cleanupErr := errors.New("dir removal failed")

	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	pool.chmod = func(string, os.FileMode) error { return chmodErr }
	pool.removeAll = func(string) error { return cleanupErr }

	var leaked string
	t.Cleanup(func() {
		if leaked != "" {
			_ = os.RemoveAll(leaked)
		}
	})

	_, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if !errors.Is(err, chmodErr) {
		t.Fatalf("acquire = %v, want the security failure", err)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("acquire = %v, want the cleanup failure joined in", err)
	}

	pool.mu.Lock()
	unresolved := append([]claudeSubUnresolved(nil), pool.unresolved...)
	pool.mu.Unlock()
	if len(unresolved) != 1 {
		t.Fatalf("pool unresolved accounting holds %d sessions, want 1", len(unresolved))
	}
	if !errors.Is(unresolved[0].err, cleanupErr) {
		t.Fatalf("unresolved ownership error = %v, want the cleanup failure", unresolved[0].err)
	}
	leaked = unresolved[0].session.dir
	if leaked == "" {
		t.Fatal("unresolved session lost its directory")
	}

	if _, err := pool.acquire(context.Background(), "other", claudeSubTestSpec()); !errors.Is(err, errClaudeSubPoolClosed) {
		t.Fatalf("acquire after cleanup failure = %v, want errClaudeSubPoolClosed", err)
	}
	if err := pool.Close(); !errors.Is(err, cleanupErr) {
		t.Fatalf("Close = %v, want it to report the retained cleanup failure", err)
	}
}

// TestClaudeSubSessionEarlyDirCleanupDeadlineIncomplete proves directory removal
// is deadline-bounded and honest. os.RemoveAll cannot be canceled, so removal
// runs as a one-time async job. When it outlives the deadline, acquire and Close
// return an explicit incomplete-cleanup error, the pool stays closed to new
// acquisition, and the pool retains the session and its job. Releasing the
// removal lets the job record its late completion with no second removal call.
func TestClaudeSubSessionEarlyDirCleanupDeadlineIncomplete(t *testing.T) {
	chmodErr := errors.New("chmod failed")

	oldSession := claudeSubSessionCloseTimeout
	claudeSubSessionCloseTimeout = 100 * time.Millisecond
	defer func() { claudeSubSessionCloseTimeout = oldSession }()
	oldShutdown := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = 100 * time.Millisecond
	defer func() { claudeSubPoolShutdownTimeout = oldShutdown }()

	removeEntered := make(chan struct{})
	removeRelease := make(chan struct{})
	var removeOnce sync.Once
	releaseRemoval := func() { removeOnce.Do(func() { close(removeRelease) }) }
	defer releaseRemoval()
	var removeCalls atomic.Int32

	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	pool.chmod = func(string, os.FileMode) error { return chmodErr }
	pool.removeAll = func(string) error {
		removeCalls.Add(1)
		close(removeEntered)
		<-removeRelease
		return nil
	}

	start := time.Now()
	_, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("acquire took %s; cleanup must be bounded by the deadline", elapsed)
	}
	if !errors.Is(err, chmodErr) {
		t.Fatalf("acquire = %v, want the security failure", err)
	}
	if !errors.Is(err, errClaudeSubDirCleanupIncomplete) {
		t.Fatalf("acquire = %v, want the explicit incomplete-cleanup error", err)
	}
	select {
	case <-removeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("removal was never entered")
	}

	// The pool is closed to new acquisition while the removal's fate is unknown.
	if _, err := pool.acquire(context.Background(), "other", claudeSubTestSpec()); !errors.Is(err, errClaudeSubPoolClosed) {
		t.Fatalf("acquire after incomplete cleanup = %v, want errClaudeSubPoolClosed", err)
	}

	pool.mu.Lock()
	unresolved := append([]claudeSubUnresolved(nil), pool.unresolved...)
	pool.mu.Unlock()
	if len(unresolved) != 1 {
		t.Fatalf("pool retained %d sessions, want 1", len(unresolved))
	}
	job := unresolved[0].session.removeJob
	if job == nil {
		t.Fatal("retained session lost its removal job")
	}

	// Close must also be bounded and report the incomplete cleanup, not success.
	start = time.Now()
	closeErr := pool.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %s; it must be bounded by the deadline", elapsed)
	}
	if !errors.Is(closeErr, errClaudeSubDirCleanupIncomplete) {
		t.Fatalf("Close = %v, want the explicit incomplete-cleanup error", closeErr)
	}

	// Release the removal: the job completes once and records its late result.
	releaseRemoval()
	select {
	case <-job.done:
	case <-time.After(2 * time.Second):
		t.Fatal("removal job never completed after release")
	}
	if job.err != nil {
		t.Fatalf("removal job result = %v, want nil", job.err)
	}
	if got := removeCalls.Load(); got != 1 {
		t.Fatalf("removeAll called %d times, want 1 (started once, never retried)", got)
	}
}

// TestClaudeSubSessionRemovalErrorJoinsBeforePublication proves a retained
// session's directory-removal job that completes with an error before the close
// result is published is joined into that result instead of being dropped. The
// removal runs uncancelably past a short teardown deadline; once it records its
// failure the pool owns it and Close reports it.
func TestClaudeSubSessionRemovalErrorJoinsBeforePublication(t *testing.T) {
	removeErr := errors.New("directory removal failed")
	removeEntered := make(chan struct{})
	removeRelease := make(chan struct{})
	var removeOnce sync.Once
	releaseRemoval := func() { removeOnce.Do(func() { close(removeRelease) }) }
	defer releaseRemoval()

	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	pool.removeAll = func(path string) error {
		close(removeEntered)
		<-removeRelease
		_ = os.RemoveAll(path)
		return removeErr
	}
	t.Cleanup(func() { _ = pool.Close() })

	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)

	// Force-close with a short deadline: the removal job is left running and the
	// teardown reports it incomplete.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err = s.forceClose(ctx)
	cancel()
	if !errors.Is(err, errClaudeSubDirCleanupIncomplete) {
		t.Fatalf("forceClose = %v, want the incomplete-cleanup error", err)
	}
	select {
	case <-removeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("removal never entered")
	}

	// Let the removal finish with its error, before Close publishes its result.
	releaseRemoval()
	job := s.removeJob
	select {
	case <-job.done:
	case <-time.After(2 * time.Second):
		t.Fatal("removal job never completed")
	}
	if !errors.Is(job.err, removeErr) {
		t.Fatalf("removal job result = %v, want the removal failure", job.err)
	}

	if err := pool.Close(); !errors.Is(err, removeErr) {
		t.Fatalf("Close = %v, want the completed removal error joined", err)
	}
}
