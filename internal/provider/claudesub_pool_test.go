package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// claudeSubTestRun is a fake claudeSubRunner: it answers the two locate
// subcommands and never starts a real CLI.
func claudeSubTestRun(_ context.Context, path string, args ...string) ([]byte, error) {
	switch {
	case len(args) == 1 && args[0] == "--version":
		return []byte("2.1.300 (Claude Code)"), nil
	case len(args) == 2 && args[0] == "auth" && args[1] == "status":
		return []byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}`), nil
	}
	return nil, fmt.Errorf("unexpected claude args %v for %s", args, path)
}

// claudeSubTestSpec is a minimal settled startup specification.
func claudeSubTestSpec() claudeSubStartSpec {
	return claudeSubStartSpec{
		SystemPrompt: "You are steiner, a coding agent.",
		Model:        "claude-haiku-5-5",
		Effort:       "high",
	}
}

// claudeSubTestSpawn is one recorded spawn call.
type claudeSubTestSpawn struct {
	Path string
	Args []string
	Env  []string
	Dir  string
	Conn *claudeSubFakeConn
}

// claudeSubTestSpawner records spawn calls and returns a fresh in-memory fake
// connection, so no test starts a real process.
type claudeSubTestSpawner struct {
	mu    sync.Mutex
	err   error
	calls []claudeSubTestSpawn
}

func (s *claudeSubTestSpawner) spawn(_ context.Context, path string, args, env []string, dir string) (claudeSubConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	conn := newClaudeSubFakeConn()
	s.calls = append(s.calls, claudeSubTestSpawn{
		Path: path,
		Args: append([]string(nil), args...),
		Env:  append([]string(nil), env...),
		Dir:  dir,
		Conn: conn,
	})
	return conn, nil
}

func (s *claudeSubTestSpawner) setErr(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *claudeSubTestSpawner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *claudeSubTestSpawner) call(t *testing.T, i int) claudeSubTestSpawn {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.calls) {
		t.Fatalf("spawn call %d missing; have %d", i, len(s.calls))
	}
	return s.calls[i]
}

// newClaudeSubTestPool builds a pool whose seams never touch a real CLI.
func newClaudeSubTestPool(t *testing.T, opts ClaudeSubscriptionPoolOptions) (*ClaudeSubscriptionPool, *claudeSubTestSpawner) {
	t.Helper()
	spawner := &claudeSubTestSpawner{}
	pool := NewClaudeSubscriptionPool(opts)
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = spawner.spawn
	t.Cleanup(func() { _ = pool.Close() })
	return pool, spawner
}

// claudeSubArgValue returns the token after flag in args.
func claudeSubArgValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// claudeSubHasArg reports whether flag appears in args.
func claudeSubHasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// claudeSubEnvValue returns the value of key in an env slice.
func claudeSubEnvValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix), true
		}
	}
	return "", false
}

func TestClaudeSubPoolLocatesCLIOnce(t *testing.T) {
	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	var lookups atomic.Int32
	pool.lookPath = func(string) (string, error) {
		lookups.Add(1)
		return "/fake/claude", nil
	}

	for _, key := range []string{"parent", "parent-child-1"} {
		s, err := pool.acquire(context.Background(), key, claudeSubTestSpec())
		if err != nil {
			t.Fatalf("acquire(%q): %v", key, err)
		}
		pool.release(s)
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("lookPath called %d times, want 1 (cached for the pool's life)", got)
	}
}

func TestClaudeSubPoolSessionKeyClassification(t *testing.T) {
	base := "session-1"
	advisor := claudeSubAdvisorSessionKey(base)
	if advisor == base {
		t.Fatal("advisor key must differ from the parent key")
	}
	if !claudeSubSessionIsAdvisor(advisor) {
		t.Fatalf("advisor key %q must classify as advisor", advisor)
	}
	if claudeSubSessionIsAdvisor(base) {
		t.Fatalf("parent key %q must not classify as advisor", base)
	}
	if claudeSubSessionIsAdvisor("session-advisor") {
		t.Fatal("only the |advisor suffix classifies as advisor")
	}
	if !strings.HasSuffix(advisor, "|advisor") {
		t.Fatalf("advisor key %q must end with |advisor", advisor)
	}
}

// TestClaudeSubPoolAdvisorIdleTTL pins the advisor idle TTL: the default is 60
// minutes, and an explicit IdleTTL still overrides it. Each case checks that an
// idle advisor survives just under the TTL and is reaped just over it, while a
// non-advisor session is never reaped.
func TestClaudeSubPoolAdvisorIdleTTL(t *testing.T) {
	tests := []struct {
		name string
		opts ClaudeSubscriptionPoolOptions
		ttl  time.Duration
	}{
		{name: "default", opts: ClaudeSubscriptionPoolOptions{}, ttl: 60 * time.Minute},
		{name: "explicit short override", opts: ClaudeSubscriptionPoolOptions{IdleTTL: time.Minute}, ttl: time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, _ := newClaudeSubTestPool(t, tt.opts)
			if pool.opts.IdleTTL != tt.ttl {
				t.Fatalf("IdleTTL = %v, want %v", pool.opts.IdleTTL, tt.ttl)
			}

			ctx := context.Background()
			advisorKey := claudeSubAdvisorSessionKey("s")
			parentKey := "s"
			for _, key := range []string{advisorKey, parentKey} {
				s, err := pool.acquire(ctx, key, claudeSubTestSpec())
				if err != nil {
					t.Fatalf("acquire(%q): %v", key, err)
				}
				pool.release(s)
			}

			pool.reapIdleAdvisors(time.Now().Add(tt.ttl - time.Second))
			pool.mu.Lock()
			_, advisorEarly := pool.sessions[advisorKey]
			pool.mu.Unlock()
			if !advisorEarly {
				t.Error("advisor session reaped before its idle TTL elapsed")
			}

			pool.reapIdleAdvisors(time.Now().Add(tt.ttl + time.Second))
			pool.mu.Lock()
			_, advisorLate := pool.sessions[advisorKey]
			_, parentStill := pool.sessions[parentKey]
			pool.mu.Unlock()
			if advisorLate {
				t.Error("idle advisor session not reaped after its idle TTL")
			}
			if !parentStill {
				t.Error("non-advisor session reaped by the advisor idle policy")
			}
		})
	}
}

func TestClaudeSubPoolReapsOnlyIdleAdvisorSessions(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{IdleTTL: time.Minute})
	ctx := context.Background()

	advisorKey := claudeSubAdvisorSessionKey("s")
	parentKey := "s"
	childKey := "s-child-1"
	for _, key := range []string{advisorKey, parentKey, childKey} {
		s, err := pool.acquire(ctx, key, claudeSubTestSpec())
		if err != nil {
			t.Fatalf("acquire(%q): %v", key, err)
		}
		pool.release(s)
	}

	activeKey := claudeSubAdvisorSessionKey("s2")
	active, err := pool.acquire(ctx, activeKey, claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire(active): %v", err)
	}
	defer pool.release(active)

	// Reap with a now far past the TTL so every idle advisor is eligible.
	pool.reapIdleAdvisors(time.Now().Add(time.Hour))

	pool.mu.Lock()
	_, advisorStill := pool.sessions[advisorKey]
	_, parentStill := pool.sessions[parentKey]
	_, childStill := pool.sessions[childKey]
	_, activeStill := pool.sessions[activeKey]
	pool.mu.Unlock()

	if advisorStill {
		t.Error("idle advisor session was not reaped")
	}
	if !parentStill {
		t.Error("parent session was reaped")
	}
	if !childStill {
		t.Error("child session was reaped")
	}
	if !activeStill {
		t.Error("active advisor session was reaped")
	}
	if !spawner.call(t, 0).Conn.isClosed() {
		t.Error("reaped advisor connection was not closed")
	}
	for i := 1; i <= 3; i++ {
		if spawner.call(t, i).Conn.isClosed() {
			t.Errorf("session %d connection was closed by the reaper", i)
		}
	}
}

func TestClaudeSubPoolCloseTerminatesAllSessions(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	ctx := context.Background()
	for _, key := range []string{"parent", claudeSubAdvisorSessionKey("parent"), "parent-child-1"} {
		s, err := pool.acquire(ctx, key, claudeSubTestSpec())
		if err != nil {
			t.Fatalf("acquire(%q): %v", key, err)
		}
		pool.release(s)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	for i := 0; i < 3; i++ {
		if !spawner.call(t, i).Conn.isClosed() {
			t.Errorf("session %d connection not closed by Close", i)
		}
	}
	if _, err := pool.acquire(ctx, "parent", claudeSubTestSpec()); !errors.Is(err, errClaudeSubPoolClosed) {
		t.Fatalf("acquire after Close = %v, want errClaudeSubPoolClosed", err)
	}
}

// claudeSubBlockingConn is a claudeSubConn whose Close blocks until release, so
// a test can prove the pool does not hold its mutex while closing a session.
type claudeSubBlockingConn struct {
	events    chan claudeSubEvent
	entered   chan struct{}
	release   chan struct{}
	closeOnce sync.Once
}

func newClaudeSubBlockingConn() *claudeSubBlockingConn {
	return &claudeSubBlockingConn{
		events:  make(chan claudeSubEvent),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (c *claudeSubBlockingConn) Send([]byte) error             { return nil }
func (c *claudeSubBlockingConn) Events() <-chan claudeSubEvent { return c.events }
func (c *claudeSubBlockingConn) Err() error                    { return nil }
func (c *claudeSubBlockingConn) RequestShutdown(error)         {}

func (c *claudeSubBlockingConn) Close(ctx context.Context) error      { return c.shutdown(ctx) }
func (c *claudeSubBlockingConn) ForceClose(ctx context.Context) error { return c.shutdown(ctx) }

func (c *claudeSubBlockingConn) shutdown(ctx context.Context) error {
	c.closeOnce.Do(func() {
		close(c.entered)
		select {
		case <-c.release:
		case <-ctx.Done():
		}
		close(c.events)
	})
	return nil
}

func TestClaudeSubPoolCloseDoesNotHoldMutex(t *testing.T) {
	conn := newClaudeSubBlockingConn()
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)

	done := make(chan error, 1)
	go func() { done <- pool.Close() }()

	select {
	case <-conn.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Close never reached conn.Close")
	}
	// Close is now blocked inside conn.Close; the pool mutex must be free so an
	// acquire, release or reap cannot deadlock against shutdown.
	if !pool.mu.TryLock() {
		t.Fatal("Close held the pool mutex while closing a session")
	}
	pool.mu.Unlock()

	close(conn.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after conn.Close released")
	}
}

// claudeSubWaitFor polls cond until it is true or the deadline passes.
func claudeSubWaitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met before the deadline")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestClaudeSubPoolCloseForcesWithoutWaitingForActiveCall proves Close begins
// forced teardown without waiting for an active call lease: a call that never
// releases does not block Close, and the transport is force-closed so the
// parked call would wake and release naturally.
func TestClaudeSubPoolCloseForcesWithoutWaitingForActiveCall(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	ctx := context.Background()

	s, err := pool.acquire(ctx, "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn

	closeDone := make(chan error, 1)
	go func() { closeDone <- pool.Close() }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked on an active call that never released")
	}

	if !conn.isForceClosed() {
		t.Error("Close did not force-close the transport of an active call")
	}
	if !conn.isClosed() {
		t.Error("Close did not close the transport of an active call")
	}
	// A new acquire must fail once Close has begun, without starting a session.
	if _, err := pool.acquire(ctx, "other", claudeSubTestSpec()); !errors.Is(err, errClaudeSubPoolClosed) {
		t.Fatalf("acquire during Close = %v, want errClaudeSubPoolClosed", err)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("acquire during Close started %d extra sessions, want 0", n-1)
	}

	// Releasing the never-released call must not deadlock after teardown.
	pool.release(s)
}

func TestClaudeSubPoolCloseWaitsForFirstClose(t *testing.T) {
	conn := newClaudeSubBlockingConn()
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)

	first := make(chan error, 1)
	go func() { first <- pool.Close() }()
	select {
	case <-conn.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first Close never reached conn.Close")
	}

	second := make(chan error, 1)
	go func() { second <- pool.Close() }()

	// The second Close must wait for the first instead of returning early.
	select {
	case err := <-second:
		t.Fatalf("second Close returned before the first completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(conn.release)
	if err := <-first; err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestClaudeSubPoolWaiterBehindSessionLockReturnsClosed proves a call waiting on
// a session's whole-call lock is cancellation-aware: when Close begins it
// returns the closed error instead of blocking until the holder releases.
func TestClaudeSubPoolWaiterBehindSessionLockReturnsClosed(t *testing.T) {
	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	ctx := context.Background()

	holder, err := pool.acquire(ctx, "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire holder: %v", err)
	}

	waiterErr := make(chan error, 1)
	go func() {
		_, err := pool.acquire(ctx, "parent", claudeSubTestSpec())
		waiterErr <- err
	}()
	// Wait until the waiter has taken its reservation and is parked on the
	// whole-call lock the holder owns.
	claudeSubWaitFor(t, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return pool.active >= 2
	})

	closeDone := make(chan error, 1)
	go func() { closeDone <- pool.Close() }()

	// The holder never releases before Close: only cancellation-aware locking
	// can unblock the waiter.
	select {
	case err := <-waiterErr:
		if !errors.Is(err, errClaudeSubPoolClosed) {
			t.Fatalf("waiter acquire = %v, want errClaudeSubPoolClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter behind the session lock did not return after Close began")
	}

	pool.release(holder)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not complete")
	}
}

// TestClaudeSubPoolCloseDuringBlockedStartup proves Close is bounded while a
// startup is blocked in an injected spawn that ignores context cancellation. It
// returns the incomplete-shutdown error, and once the spawn is released the late
// startup tears its session down locally and never publishes or returns it.
func TestClaudeSubPoolCloseDuringBlockedStartup(t *testing.T) {
	old := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = 100 * time.Millisecond
	defer func() { claudeSubPoolShutdownTimeout = old }()

	spawnEntered := make(chan struct{})
	spawnRelease := make(chan struct{})
	var spawned claudeSubConn
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		close(spawnEntered)
		<-spawnRelease // deliberately ignores context cancellation
		spawned = newClaudeSubFakeConn()
		return spawned, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	acquireErr := make(chan error, 1)
	go func() {
		_, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
		acquireErr <- err
	}()
	select {
	case <-spawnEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("spawn was never entered")
	}

	start := time.Now()
	err := pool.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %s; it must be bounded by the pool deadline", elapsed)
	}
	if !errors.Is(err, errClaudeSubPoolShutdownIncomplete) {
		t.Fatalf("Close = %v, want errClaudeSubPoolShutdownIncomplete", err)
	}

	// Release the blocked spawn: the startup completes after close began and
	// must tear its session down locally.
	close(spawnRelease)
	select {
	case err := <-acquireErr:
		if !errors.Is(err, errClaudeSubPoolClosed) {
			t.Fatalf("late startup acquire = %v, want errClaudeSubPoolClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late startup did not settle after its spawn was released")
	}
	if spawned == nil || !spawned.(*claudeSubFakeConn).isClosed() {
		t.Error("late startup session was not torn down locally")
	}
	pool.mu.Lock()
	published := len(pool.sessions)
	pool.mu.Unlock()
	if published != 0 {
		t.Errorf("late startup published %d sessions, want 0", published)
	}
}

// TestClaudeSubPoolCloseCancelsInFlightStartup proves a startup whose spawn
// observes context cancellation settles promptly, so Close completes cleanly
// and the aborted startup is never published.
func TestClaudeSubPoolCloseCancelsInFlightStartup(t *testing.T) {
	old := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = 100 * time.Millisecond
	defer func() { claudeSubPoolShutdownTimeout = old }()

	spawnEntered := make(chan struct{})
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(ctx context.Context, _ string, _, _ []string, _ string) (claudeSubConn, error) {
		close(spawnEntered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { _ = pool.Close() })

	acquireErr := make(chan error, 1)
	go func() {
		_, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
		acquireErr <- err
	}()
	select {
	case <-spawnEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("spawn was never entered")
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close = %v, want nil when the startup observes cancellation", err)
	}
	select {
	case err := <-acquireErr:
		if err == nil {
			t.Fatal("canceled startup acquire returned nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled startup did not settle")
	}
	pool.mu.Lock()
	published := len(pool.sessions)
	pool.mu.Unlock()
	if published != 0 {
		t.Errorf("canceled startup published %d sessions, want 0", published)
	}
}

// TestClaudeSubPoolConcurrentCloseSharesResult proves every Close caller returns
// the same terminal result, including a teardown error.
func TestClaudeSubPoolConcurrentCloseSharesResult(t *testing.T) {
	shutdownErr := errors.New("transport teardown failed")
	conn := newClaudeSubFakeConn()
	conn.closeErr = shutdownErr
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)

	const callers = 4
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { results <- pool.Close() }()
	}
	for i := 0; i < callers; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, shutdownErr) {
				t.Fatalf("Close = %v, want it to wrap the teardown error", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Close did not return for every caller")
		}
	}
}

// TestClaudeSubPoolAdmittedLeaseMayBeConcurrentlyForceClosed proves the real
// admission contract: acquire is linearizable only at its final pool-state
// check, and the exclusive whole-call lease it then returns guarantees
// serialization, not transport lifetime. Close never waits for an active lease,
// so it may force-close an admitted session immediately; the holder still owns
// the lease, and later operations observe safe errors instead of panicking or
// deadlocking.
func TestClaudeSubPoolAdmittedLeaseMayBeConcurrentlyForceClosed(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	ctx := context.Background()

	s, err := pool.acquire(ctx, "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	conn := spawner.call(t, 0).Conn

	// Close begins while the caller still holds the admitted whole-call lease.
	closeDone := make(chan error, 1)
	go func() { closeDone <- pool.Close() }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked on an admitted lease")
	}
	if !conn.isForceClosed() {
		t.Error("Close did not force-close the admitted session's transport")
	}

	// Teardown marked the session terminal, so a later operation on the revoked
	// lease observes a safe closed error instead of blocking.
	if !s.isGone() {
		t.Error("admitted session was not marked gone by teardown")
	}
	if err := s.lockCall(ctx); !errors.Is(err, errClaudeSubPoolClosed) {
		t.Errorf("lockCall on a revoked session = %v, want errClaudeSubPoolClosed", err)
	}

	// Releasing the held lease must neither panic nor deadlock, and the pool
	// stays closed.
	pool.release(s)
	if _, err := pool.acquire(ctx, "parent", claudeSubTestSpec()); !errors.Is(err, errClaudeSubPoolClosed) {
		t.Errorf("acquire after Close = %v, want errClaudeSubPoolClosed", err)
	}
}

// TestClaudeSubPoolReaperTeardownFailureFailsPoolClosed proves a reaper teardown
// failure of any kind is a pool-owned safety failure, not a transient error to
// record and forget: the failed session and its error are retained, the pool
// fails closed so no later acquire proceeds, and Close reports the failure
// because it was known before the deadline.
func TestClaudeSubPoolReaperTeardownFailureFailsPoolClosed(t *testing.T) {
	reapErr := errors.New("reaper teardown failed")
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{IdleTTL: time.Minute})

	s, err := pool.acquire(context.Background(), claudeSubAdvisorSessionKey("s"), claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)
	spawner.call(t, 0).Conn.closeErr = reapErr

	// Reap synchronously so the failure is known before Close runs.
	pool.reapIdleAdvisors(time.Now().Add(time.Hour))

	pool.mu.Lock()
	owned := append([]claudeSubUnresolved(nil), pool.unresolved...)
	state := pool.state
	pool.mu.Unlock()
	if len(owned) != 1 {
		t.Fatalf("pool retained %d sessions, want 1", len(owned))
	}
	if !errors.Is(owned[0].err, reapErr) {
		t.Fatalf("retained error = %v, want the reaper teardown failure", owned[0].err)
	}
	if state == claudeSubPoolRunning {
		t.Fatal("a reaper teardown failure left the pool running")
	}

	if _, err := pool.acquire(context.Background(), "other", claudeSubTestSpec()); !errors.Is(err, errClaudeSubPoolClosed) {
		t.Fatalf("acquire after a reaper teardown failure = %v, want errClaudeSubPoolClosed", err)
	}
	if err := pool.Close(); !errors.Is(err, reapErr) {
		t.Fatalf("Close = %v, want it to report the reaper teardown failure", err)
	}
}

// claudeSubWaitExited waits until the pool's session for key reports that its
// CLI process exited on its own. The pool keeps the dead session in its map.
func claudeSubWaitExited(t *testing.T, pool *ClaudeSubscriptionPool, key string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		pool.mu.Lock()
		s := pool.sessions[key]
		pool.mu.Unlock()
		if s != nil && s.exited() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %q did not report exit", key)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestClaudeSubPoolDeadParentOrChildReturnsTypedError proves a dead parent or
// child session is not respawned: acquire returns errClaudeSubSessionExited.
// Closing the fake connection models the CLI process exiting on its own.
func TestClaudeSubPoolDeadParentOrChildReturnsTypedError(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "parent", key: "s"},
		{name: "child", key: "s-child-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
			ctx := context.Background()
			s, err := pool.acquire(ctx, tt.key, claudeSubTestSpec())
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			pool.release(s)
			if err := spawner.call(t, 0).Conn.Close(ctx); err != nil {
				t.Fatalf("close fake CLI: %v", err)
			}
			claudeSubWaitExited(t, pool, tt.key)

			if _, err := pool.acquire(ctx, tt.key, claudeSubTestSpec()); !errors.Is(err, errClaudeSubSessionExited) {
				t.Fatalf("acquire after exit = %v, want errClaudeSubSessionExited", err)
			}
			if n := spawner.callCount(); n != 1 {
				t.Errorf("spawn count = %d, want 1 (no respawn of a dead session)", n)
			}
		})
	}
}

// TestClaudeSubPoolDeadAdvisorIsReplaced proves a dead advisor is evicted and
// the next advisor call starts a fresh process whose teardown ran before it.
func TestClaudeSubPoolDeadAdvisorIsReplaced(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	ctx := context.Background()
	key := claudeSubAdvisorSessionKey("s")
	first, err := pool.acquire(ctx, key, claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(first)
	if err := spawner.call(t, 0).Conn.Close(ctx); err != nil {
		t.Fatalf("close fake CLI: %v", err)
	}
	claudeSubWaitExited(t, pool, key)

	second, err := pool.acquire(ctx, key, claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire dead advisor = %v, want a fresh session", err)
	}
	defer pool.release(second)
	if second == first {
		t.Fatal("acquire returned the dead advisor session")
	}
	if n := spawner.callCount(); n != 2 {
		t.Fatalf("spawn count = %d, want 2 (fresh advisor process)", n)
	}
	select {
	case <-first.teardownJob.done:
	default:
		t.Error("evicted advisor teardown did not finish before the replacement was returned")
	}
}

// TestClaudeSubPoolReaperDirRemovalFailureKeepsPoolRunning proves a reaped
// advisor whose process terminated but whose directory removal failed does not
// close the pool. The leftover directory stays pool-owned and Close reports it.
func TestClaudeSubPoolReaperDirRemovalFailureKeepsPoolRunning(t *testing.T) {
	removeErr := errors.New("remove denied")
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{IdleTTL: time.Minute})
	pool.removeAll = func(string) error { return removeErr }
	ctx := context.Background()

	advisor, err := pool.acquire(ctx, claudeSubAdvisorSessionKey("s"), claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire advisor: %v", err)
	}
	pool.release(advisor)
	parent, err := pool.acquire(ctx, "s", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire parent: %v", err)
	}
	pool.release(parent)

	pool.reapIdleAdvisors(time.Now().Add(time.Hour))

	pool.mu.Lock()
	state := pool.state
	owned := append([]claudeSubUnresolved(nil), pool.unresolved...)
	pool.mu.Unlock()
	if len(owned) != 1 || !errors.Is(owned[0].err, removeErr) {
		t.Fatalf("unresolved = %v, want the directory removal error retained", owned)
	}
	dir := owned[0].session.dir
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if state != claudeSubPoolRunning {
		t.Fatalf("state = %d after a directory removal failure, want running", state)
	}
	if spawner.call(t, 1).Conn.isClosed() {
		t.Error("parent connection was closed by the advisor reap")
	}
	if _, err := pool.acquire(ctx, "s", claudeSubTestSpec()); err != nil {
		t.Fatalf("acquire after reap = %v, want the pool still running", err)
	}
	if err := pool.Close(); !errors.Is(err, removeErr) {
		t.Fatalf("Close = %v, want it to report the directory removal error", err)
	}
}

// TestClaudeSubPoolCancelledAcquireDoesNotPoisonCLILookup proves a transient
// caller cancellation cannot poison the pool's single CLI lookup: the lookup
// runs under the pool lifetime, a canceled first acquirer returns promptly
// without cancelling or caching it, and a second acquirer succeeds from the same
// one lookup while the pool stays running.
func TestClaudeSubPoolCancelledAcquireDoesNotPoisonCLILookup(t *testing.T) {
	var lookups atomic.Int32
	lookEntered := make(chan struct{})
	lookRelease := make(chan struct{})
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) {
		if lookups.Add(1) == 1 {
			close(lookEntered)
			<-lookRelease
		}
		return "/fake/claude", nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := pool.acquire(ctx, "parent", claudeSubTestSpec())
		firstErr <- err
	}()
	select {
	case <-lookEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("CLI lookup was never entered")
	}

	cancel()
	select {
	case err := <-firstErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled acquire = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled acquire did not return promptly")
	}

	// The pool is still running and the lookup is still shared: release it and a
	// second acquirer must succeed from the same single lookup.
	type acquireResult struct {
		session *claudeSubSession
		err     error
	}
	second := make(chan acquireResult, 1)
	go func() {
		s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
		second <- acquireResult{session: s, err: err}
	}()
	close(lookRelease)
	select {
	case got := <-second:
		if got.err != nil {
			t.Fatalf("second acquire = %v, want the shared lookup to succeed", got.err)
		}
		pool.release(got.session)
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire never completed")
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("lookPath called %d times, want 1 (single lookup across pool life)", got)
	}
	if n := spawner.callCount(); n != 1 {
		t.Fatalf("spawn called %d times, want 1", n)
	}
}

// TestClaudeSubPoolCloseCancelsInFlightCLILookup proves pool shutdown still
// cancels the single CLI lookup: a lookup that observes pool cancellation lets
// Close complete cleanly, and the canceled startup settles with the closed
// error rather than a transient caller event.
func TestClaudeSubPoolCloseCancelsInFlightCLILookup(t *testing.T) {
	old := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = time.Second
	defer func() { claudeSubPoolShutdownTimeout = old }()

	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	lookEntered := make(chan struct{})
	pool.lookPath = func(string) (string, error) {
		close(lookEntered)
		<-pool.ctx.Done() // the pool lifetime, not a caller context
		return "", pool.ctx.Err()
	}

	acquireErr := make(chan error, 1)
	go func() {
		_, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
		acquireErr <- err
	}()
	select {
	case <-lookEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("CLI lookup was never entered")
	}

	start := time.Now()
	if err := pool.Close(); err != nil {
		t.Fatalf("Close = %v, want nil once the lookup observes pool cancellation", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %s; cancelling the lookup must bound shutdown", elapsed)
	}
	select {
	case err := <-acquireErr:
		if !errors.Is(err, errClaudeSubPoolClosed) {
			t.Fatalf("acquire during Close = %v, want errClaudeSubPoolClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not settle after the lookup was cancelled")
	}
}

// claudeSubBlockingSendConn is a claudeSubFakeConn whose Send blocks until the
// connection is closed, modelling a control writer parked in a transport Send.
// entered closes on the first Send so a test can wait for the writer to park
// before it starts teardown.
type claudeSubBlockingSendConn struct {
	*claudeSubFakeConn
	entered chan struct{}
	once    sync.Once
}

func newClaudeSubBlockingSendConn() *claudeSubBlockingSendConn {
	return &claudeSubBlockingSendConn{
		claudeSubFakeConn: newClaudeSubFakeConn(),
		entered:           make(chan struct{}),
	}
}

func (c *claudeSubBlockingSendConn) Send([]byte) error {
	c.once.Do(func() { close(c.entered) })
	<-c.releaseSend
	return errors.New("send released by connection close")
}

// TestClaudeSubPoolCloseForcesTransportBeforeControlJoin proves forced teardown
// terminates the transport before it joins the router and control writer. A
// control writer parked in conn.Send would otherwise keep the router from
// finishing, so pool Close must ForceClose the transport first and complete
// within its deadline instead of returning the incomplete-shutdown error.
func TestClaudeSubPoolCloseForcesTransportBeforeControlJoin(t *testing.T) {
	old := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = time.Second
	defer func() { claudeSubPoolShutdownTimeout = old }()

	conn := newClaudeSubBlockingSendConn()
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)

	// Park the control writer in conn.Send: it stays in Send until the
	// connection is closed.
	s.control.enqueue([]byte(`{"type":"control_request"}`))
	select {
	case <-conn.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("control writer never entered Send")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- pool.Close() }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close = %v, want it to complete by forcing the transport", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked joining the control writer behind a blocked Send")
	}
	if !conn.isForceClosed() {
		t.Error("Close did not force-close the transport")
	}
}

// TestClaudeSubPoolDuplicateStartupCleanupFailureFailsClosed proves a duplicate
// competing startup whose loser's local cleanup fails never silently returns the
// winning session: the cleanup failure is joined into the loser's result, the
// pool fails closed while the resource's fate is uncertain, and Close owns and
// reports the unresolved session.
func TestClaudeSubPoolDuplicateStartupCleanupFailureFailsClosed(t *testing.T) {
	cleanupErr := errors.New("duplicate teardown failed")

	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	var spawns atomic.Int32

	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		conn := newClaudeSubFakeConn()
		if spawns.Add(1) == 1 {
			// The first starter wins: it blocks here until the second starter
			// has registered, so that loser registers before winner publishes.
			close(firstEntered)
			<-releaseFirst
			return conn, nil
		}
		close(secondEntered)
		<-releaseSecond
		conn.closeErr = cleanupErr
		return conn, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	type acquireResult struct {
		session *claudeSubSession
		err     error
	}

	winner := make(chan acquireResult, 1)
	go func() {
		s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
		winner <- acquireResult{session: s, err: err}
	}()
	select {
	case <-firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first startup never entered spawn")
	}

	loser := make(chan acquireResult, 1)
	go func() {
		s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
		loser <- acquireResult{session: s, err: err}
	}()
	select {
	case <-secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("loser startup never entered spawn")
	}

	// Release the winner so it publishes the key, then wait until it has.
	close(releaseFirst)
	claudeSubWaitFor(t, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return len(pool.sessions) == 1
	})

	// Release the loser: it finds the key already published, tears its own
	// session down (which fails), and must fail the pool closed instead of
	// returning the winning session.
	close(releaseSecond)
	select {
	case got := <-loser:
		if got.session != nil {
			t.Error("duplicate startup returned a session while its cleanup failed")
		}
		if !errors.Is(got.err, cleanupErr) {
			t.Fatalf("duplicate acquire = %v, want the cleanup failure surfaced", got.err)
		}
		if !errors.Is(got.err, errClaudeSubPoolClosed) {
			t.Fatalf("duplicate acquire = %v, want the pool to fail closed", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("duplicate startup did not settle")
	}

	select {
	case got := <-winner:
		if got.session != nil {
			pool.release(got.session)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("winning startup did not settle")
	}

	if _, err := pool.acquire(context.Background(), "other", claudeSubTestSpec()); !errors.Is(err, errClaudeSubPoolClosed) {
		t.Fatalf("acquire after cleanup failure = %v, want errClaudeSubPoolClosed", err)
	}
	if err := pool.Close(); !errors.Is(err, cleanupErr) {
		t.Fatalf("Close = %v, want it to report the unresolved cleanup failure", err)
	}
}

// TestClaudeSubPoolShutdownBoundedByStoredDeadline proves a session teardown
// already in progress cannot make Close wait past the pool's one stored
// deadline. A cleanup begun before closing owns the session's one-time teardown
// and blocks in an uncancelable transport close; shutdown observes it through a
// deadline-bounded wait, retains the session, and returns the incomplete result
// instead of blocking behind the teardown.
func TestClaudeSubPoolShutdownBoundedByStoredDeadline(t *testing.T) {
	old := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = 100 * time.Millisecond
	defer func() { claudeSubPoolShutdownTimeout = old }()

	conn := newClaudeSubBlockingConn()
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)

	// A cleanup begun before closing owns the session's teardown and blocks in
	// an uncancelable transport close.
	teardownReturned := make(chan error, 1)
	go func() { teardownReturned <- s.forceClose(context.Background()) }()
	select {
	case <-conn.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup never reached the transport close")
	}

	start := time.Now()
	closeErr := pool.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %s; it must not wait past its stored deadline", elapsed)
	}
	if closeErr == nil {
		t.Fatal("Close published clean success while a teardown was in progress")
	}
	if !errors.Is(closeErr, errClaudeSubPoolShutdownIncomplete) {
		t.Fatalf("Close = %v, want the incomplete-shutdown error", closeErr)
	}

	// The session stays owned while its teardown is still running, even if the
	// retention lands just after Close published its result.
	claudeSubWaitFor(t, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return len(pool.unresolved) > 0
	})

	// Release the cleanup: the retained session's teardown completes.
	close(conn.release)
	select {
	case err := <-teardownReturned:
		if err != nil {
			t.Fatalf("cleanup teardown = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup did not finish after release")
	}
}

// TestClaudeSubPoolLateStartupCleanupStaysOwnedAfterShutdownResult proves the
// approved deadline/result contract: when a tracked startup is still unsettled
// at the one absolute shutdown deadline, every Close caller receives the same
// honest incomplete result and never success, and the later cleanup failure
// stays durably owned. The late failure cannot retroactively join a result that
// was already published, so it appears only in durable ownership (and in the
// late startup's own acquire result), never in the returned Close result.
func TestClaudeSubPoolLateStartupCleanupStaysOwnedAfterShutdownResult(t *testing.T) {
	old := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = 100 * time.Millisecond
	defer func() { claudeSubPoolShutdownTimeout = old }()

	cleanupErr := errors.New("late startup cleanup failed")
	spawnEntered := make(chan struct{})
	spawnRelease := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(spawnRelease) }) }
	defer release()

	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		close(spawnEntered)
		<-spawnRelease // deliberately ignores context cancellation
		conn := newClaudeSubFakeConn()
		conn.closeErr = cleanupErr
		return conn, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	acquireErr := make(chan error, 1)
	go func() {
		_, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
		acquireErr <- err
	}()
	select {
	case <-spawnEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("startup never entered spawn")
	}

	// Close reaches its absolute deadline while the startup is unsettled, so
	// every caller must see an honest incomplete result, never success — and
	// never the not-yet-discovered cleanup failure.
	const callers = 3
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { results <- pool.Close() }()
	}
	for i := 0; i < callers; i++ {
		select {
		case err := <-results:
			if err == nil {
				t.Fatalf("Close caller %d published clean success while a startup was unsettled", i)
			}
			if !errors.Is(err, errClaudeSubPoolShutdownIncomplete) {
				t.Fatalf("Close caller %d = %v, want the incomplete-shutdown error", i, err)
			}
			if errors.Is(err, cleanupErr) {
				t.Fatalf("Close caller %d = %v; a late failure must not be joined retroactively", i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a Close caller did not return")
		}
	}

	// Release the startup: it settles after publication, reports its cleanup
	// failure to its own caller, and transfers ownership to the pool durably.
	release()
	select {
	case err := <-acquireErr:
		if !errors.Is(err, cleanupErr) {
			t.Fatalf("late startup acquire = %v, want its cleanup failure", err)
		}
		if !errors.Is(err, errClaudeSubPoolClosed) {
			t.Fatalf("late startup acquire = %v, want the pool closed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late startup did not settle")
	}

	claudeSubWaitFor(t, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return len(pool.unresolved) == 1
	})
	pool.mu.Lock()
	owned := pool.unresolved[0]
	published := len(pool.sessions)
	pool.mu.Unlock()
	if !errors.Is(owned.err, cleanupErr) {
		t.Fatalf("durable ownership error = %v, want the late cleanup failure", owned.err)
	}
	if owned.session == nil {
		t.Fatal("durable ownership lost the session")
	}
	if published != 0 {
		t.Errorf("late startup published %d sessions, want 0", published)
	}

	// A later Close returns the same immutable result.
	if err := pool.Close(); !errors.Is(err, errClaudeSubPoolShutdownIncomplete) {
		t.Fatalf("later Close = %v, want the same incomplete result", err)
	}
}

// claudeSubContextErrConn is a claudeSubConn whose teardown returns a generic
// context-wrapped error, modelling a production transport that reports an
// uncancelable forced shutdown as a context error rather than a session
// sentinel.
type claudeSubContextErrConn struct {
	events chan claudeSubEvent
	err    error
}

func newClaudeSubContextErrConn(err error) *claudeSubContextErrConn {
	return &claudeSubContextErrConn{events: make(chan claudeSubEvent), err: err}
}

func (c *claudeSubContextErrConn) Send([]byte) error             { return nil }
func (c *claudeSubContextErrConn) Events() <-chan claudeSubEvent { return c.events }
func (c *claudeSubContextErrConn) Err() error                    { return nil }
func (c *claudeSubContextErrConn) RequestShutdown(error)         {}
func (c *claudeSubContextErrConn) Close(context.Context) error   { return c.err }
func (c *claudeSubContextErrConn) ForceClose(context.Context) error {
	return c.err
}

// TestClaudeSubPoolShutdownRetainsGenericTeardownError proves a published
// session whose forced teardown returns a generic context-wrapped error is
// retained and fails the pool closed instead of the error being recorded and
// the resource dropped, and that Close reports a non-successful result.
func TestClaudeSubPoolShutdownRetainsGenericTeardownError(t *testing.T) {
	forceErr := fmt.Errorf("claude transport forced shutdown: %w", context.DeadlineExceeded)
	conn := newClaudeSubContextErrConn(forceErr)
	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		return conn, nil
	}
	t.Cleanup(func() { _ = pool.Close() })

	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)

	closeErr := pool.Close()
	if closeErr == nil {
		t.Fatal("Close returned success while a teardown reported a generic context error")
	}
	if !errors.Is(closeErr, forceErr) {
		t.Fatalf("Close = %v, want the generic teardown error joined", closeErr)
	}

	pool.mu.Lock()
	retained := false
	for _, u := range pool.unresolved {
		if u.session == s {
			retained = true
		}
	}
	pool.mu.Unlock()
	if !retained {
		t.Error("a generic teardown error dropped the session instead of retaining it")
	}
}

// TestClaudeSubPoolCancellationAdmission pins the precise admission contract: an
// already-canceled request is rejected inside acquire's pool-mutex admission
// section (and by lockCall before the token select), so it can neither start,
// publish nor receive a lease even when a call token is ready; a cancellation
// after a successful admission is revocation-after-admission and is permitted.
func TestClaudeSubPoolCancellationAdmission(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})

	// Canceled before acquire: no session is started or published and no lease
	// is returned.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if s, err := pool.acquire(canceled, "parent", claudeSubTestSpec()); s != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire = (%v, %v), want (nil, context.Canceled)", s, err)
	}
	if n := spawner.callCount(); n != 0 {
		t.Fatalf("canceled acquire started %d sessions, want 0", n)
	}
	pool.mu.Lock()
	published := len(pool.sessions)
	pool.mu.Unlock()
	if published != 0 {
		t.Fatalf("canceled acquire published %d sessions, want 0", published)
	}

	// A live session with a free call token: a canceled acquire must not take
	// the token, so a later normal acquire still gets the lease.
	live, err := pool.acquire(context.Background(), "live", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire live: %v", err)
	}
	pool.release(live)

	canceled2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if s, err := pool.acquire(canceled2, "live", claudeSubTestSpec()); s != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire of a live session = (%v, %v), want (nil, context.Canceled)", s, err)
	}
	leaseCtx, leaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer leaseCancel()
	again, err := pool.acquire(leaseCtx, "live", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire after a canceled acquire = %v; the canceled request kept the call token", err)
	}
	pool.release(again)

	// lockCall rejects an already-canceled context deterministically, before it
	// can win a ready token.
	canceledCall, cancelCall := context.WithCancel(context.Background())
	cancelCall()
	if err := live.lockCall(canceledCall); !errors.Is(err, context.Canceled) {
		t.Fatalf("lockCall with a canceled ctx = %v, want context.Canceled", err)
	}

	// Cancellation after a successful admission is permitted: the holder keeps
	// the lease and releasing it is safe.
	admittedCtx, admitCancel := context.WithCancel(context.Background())
	admitted, err := pool.acquire(admittedCtx, "admitted", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire admitted: %v", err)
	}
	admitCancel()
	pool.release(admitted)
	reuse, err := pool.acquire(context.Background(), "admitted", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire after post-admission cancellation = %v", err)
	}
	pool.release(reuse)
}

// TestClaudeSubPoolCancelBeforeAdmissionNeverPublishes proves the atomic
// first-lease publication protocol closes the former publish-then-lease gap: a
// cancellation injected exactly between taking the fresh session's whole-call
// token and the final admission check neither publishes the new session nor
// returns it a lease. The session is cleaned up locally, the pool stays running,
// and a later acquisition starts and uses a fresh session.
func TestClaudeSubPoolCancelBeforeAdmissionNeverPublishes(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seamRan := false
	pool.beforeAdmission = func() {
		// The token is taken and the session is still unpublished: cancel here
		// so it is the admission check, not a later stage, that observes it.
		seamRan = true
		cancel()
	}

	s, err := pool.acquire(ctx, "parent", claudeSubTestSpec())
	if !seamRan {
		t.Fatal("the admission seam never ran; acquire did not reach the admission stage")
	}
	if s != nil {
		t.Fatalf("cancel-before-admission acquire = %v, want a nil session", s)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel-before-admission acquire = %v, want context.Canceled", err)
	}

	// The created session is never published and is torn down locally.
	pool.mu.Lock()
	published := len(pool.sessions)
	pool.mu.Unlock()
	if published != 0 {
		t.Fatalf("canceled startup published %d sessions, want 0", published)
	}
	first := spawner.call(t, 0)
	if !first.Conn.isForceClosed() {
		t.Error("canceled startup session was not cleaned up locally")
	}

	// The pool is still running: a later acquisition starts and uses a fresh
	// session.
	s, err = pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("later acquire: %v", err)
	}
	pool.release(s)
	if got := spawner.callCount(); got != 2 {
		t.Fatalf("spawn called %d times, want 2", got)
	}
}

// TestClaudeSubPoolPostAdmissionCancellationKeepsLease proves the other side of
// the admission contract: once the atomic admission check has published the
// session and taken the reservation, a cancellation is valid post-admission
// revocation. acquire still returns the published session as its lease, release
// is safe, and a later acquisition reuses the same session.
func TestClaudeSubPoolPostAdmissionCancellationKeepsLease(t *testing.T) {
	pool, spawner := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seamRan := false
	pool.afterAdmission = func() {
		// The session is published and leased: a cancellation here is
		// post-admission and must not invalidate the return.
		seamRan = true
		cancel()
	}

	s, err := pool.acquire(ctx, "parent", claudeSubTestSpec())
	if !seamRan {
		t.Fatal("the post-admission seam never ran")
	}
	if err != nil {
		t.Fatalf("post-admission cancellation acquire = %v, want nil", err)
	}
	if s == nil {
		t.Fatal("post-admission cancellation acquire returned no lease")
	}

	pool.mu.Lock()
	published := len(pool.sessions)
	pool.mu.Unlock()
	if published != 1 {
		t.Fatalf("post-admission cancellation published %d sessions, want 1", published)
	}

	// Releasing the lease returned under a canceled context is safe, and the
	// session stays reusable.
	pool.release(s)
	reuse, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire after post-admission cancellation = %v", err)
	}
	if reuse != s {
		t.Error("a later acquire did not reuse the published session")
	}
	pool.release(reuse)
	if got := spawner.callCount(); got != 1 {
		t.Fatalf("spawn called %d times, want 1", got)
	}
}

// TestClaudeSubPoolShutdownBlockedMCPHostClose proves the MCP host close is a
// tracked once-started job: when its Close (which has its own background grace
// and no context API) outlives the stored deadline, Close returns the shared
// incomplete result without claiming the host was canceled, the job stays
// tracked and running, and it is never started a second time.
func TestClaudeSubPoolShutdownBlockedMCPHostClose(t *testing.T) {
	old := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = 100 * time.Millisecond
	defer func() { claudeSubPoolShutdownTimeout = old }()

	var hostCalls atomic.Int32
	hostEntered := make(chan struct{})
	hostRelease := make(chan struct{})
	var hostOnce sync.Once
	releaseHost := func() { hostOnce.Do(func() { close(hostRelease) }) }
	defer releaseHost()

	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	pool.closeHost = func(h *claudeSubMCPHost) error {
		hostCalls.Add(1)
		close(hostEntered)
		<-hostRelease
		return h.Close()
	}
	t.Cleanup(func() { _ = pool.Close() })

	spec := claudeSubTestSpec()
	spec.Tools = []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}}
	s, err := pool.acquire(context.Background(), "parent", spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)

	start := time.Now()
	closeErr := pool.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %s; it must be bounded by the stored deadline", elapsed)
	}
	if closeErr == nil || !errors.Is(closeErr, errClaudeSubPoolShutdownIncomplete) {
		t.Fatalf("Close = %v, want the incomplete-shutdown error", closeErr)
	}
	select {
	case <-hostEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("host Close was never invoked")
	}
	pool.mu.Lock()
	tracked := false
	for _, j := range pool.cleanupJobs {
		if j == s.hostJob {
			tracked = true
		}
	}
	pool.mu.Unlock()
	if !tracked {
		t.Error("blocked host-close job was not tracked")
	}
	select {
	case <-s.hostJob.done:
		t.Fatal("host-close job reported done while its Close is still blocked")
	default:
	}

	releaseHost()
	select {
	case <-s.hostJob.done:
	case <-time.After(2 * time.Second):
		t.Fatal("host-close job never completed after release")
	}
	if got := hostCalls.Load(); got != 1 {
		t.Fatalf("host Close invoked %d times, want exactly once", got)
	}
}

// TestClaudeSubPoolShutdownBlockedCLILookup proves the shared one-time CLI
// lookup is shutdown-tracked: when an injected lookup ignores the pool lifetime
// and outlives the stored deadline, Close returns the shared incomplete result,
// the lookup job stays tracked and running, and it is never started again.
func TestClaudeSubPoolShutdownBlockedCLILookup(t *testing.T) {
	old := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = 100 * time.Millisecond
	defer func() { claudeSubPoolShutdownTimeout = old }()

	var lookups atomic.Int32
	lookEntered := make(chan struct{})
	lookRelease := make(chan struct{})
	var lookOnce sync.Once
	releaseLook := func() { lookOnce.Do(func() { close(lookRelease) }) }
	defer releaseLook()

	pool, _ := newClaudeSubTestPool(t, ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) {
		lookups.Add(1)
		close(lookEntered)
		<-lookRelease // deliberately ignores the pool lifetime context
		return "/fake/claude", nil
	}

	acquireErr := make(chan error, 1)
	go func() {
		_, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
		acquireErr <- err
	}()
	select {
	case <-lookEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("CLI lookup was never entered")
	}

	start := time.Now()
	closeErr := pool.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close took %s; it must be bounded by the stored deadline", elapsed)
	}
	if closeErr == nil || !errors.Is(closeErr, errClaudeSubPoolShutdownIncomplete) {
		t.Fatalf("Close = %v, want the incomplete-shutdown error", closeErr)
	}

	pool.mu.Lock()
	job := pool.cliJob
	started := job.started
	pool.mu.Unlock()
	if !started {
		t.Fatal("CLI lookup job was not tracked")
	}
	select {
	case <-job.done:
		t.Fatal("CLI lookup job reported done while its lookup is still blocked")
	default:
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("lookPath called %d times, want 1", got)
	}

	select {
	case err := <-acquireErr:
		if !errors.Is(err, errClaudeSubPoolClosed) {
			t.Fatalf("acquire during Close = %v, want errClaudeSubPoolClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("startup did not settle")
	}

	releaseLook()
	select {
	case <-job.done:
	case <-time.After(2 * time.Second):
		t.Fatal("CLI lookup job never completed after release")
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("lookPath called %d times after release, want exactly 1", got)
	}
}

// TestClaudeSubPoolCleanupErrorAfterPublicationStaysDurable proves the close
// result is immutable at its publication linearization point: a cleanup result
// whose transfer is paused past that point is durably owned but can neither join
// nor otherwise change the already-published result, and a later Close returns
// the same result.
func TestClaudeSubPoolCleanupErrorAfterPublicationStaysDurable(t *testing.T) {
	removeErr := errors.New("late directory removal failed")

	old := claudeSubPoolShutdownTimeout
	claudeSubPoolShutdownTimeout = 100 * time.Millisecond
	defer func() { claudeSubPoolShutdownTimeout = old }()

	removeEntered := make(chan struct{})
	removeRelease := make(chan struct{})
	var removeOnce sync.Once
	releaseRemoval := func() { removeOnce.Do(func() { close(removeRelease) }) }
	defer releaseRemoval()

	recordEntered := make(chan struct{})
	recordRelease := make(chan struct{})
	var recordOnce sync.Once

	pool := NewClaudeSubscriptionPool(ClaudeSubscriptionPoolOptions{})
	pool.lookPath = func(string) (string, error) { return "/fake/claude", nil }
	pool.run = claudeSubTestRun
	pool.spawn = func(context.Context, string, []string, []string, string) (claudeSubConn, error) {
		return newClaudeSubFakeConn(), nil
	}
	pool.removeAll = func(path string) error {
		close(removeEntered)
		<-removeRelease
		_ = os.RemoveAll(path)
		return removeErr
	}
	// Pause the removal job's result transfer exactly at its publication
	// boundary, so publication can finish without it.
	pool.beforeJobRecord = func(err error) {
		if errors.Is(err, removeErr) {
			recordOnce.Do(func() {
				close(recordEntered)
				<-recordRelease
			})
		}
	}
	t.Cleanup(func() { _ = pool.Close() })

	s, err := pool.acquire(context.Background(), "parent", claudeSubTestSpec())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	pool.release(s)

	closeDone := make(chan error, 1)
	go func() { closeDone <- pool.Close() }()
	var closeErr error
	select {
	case closeErr = <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not publish while the removal job was unsettled")
	}
	if !errors.Is(closeErr, errClaudeSubPoolShutdownIncomplete) {
		t.Fatalf("Close = %v, want the incomplete-shutdown error", closeErr)
	}
	if errors.Is(closeErr, removeErr) {
		t.Fatal("Close joined an error that was not yet transferred")
	}

	// Let the removal finish and reach its paused transfer boundary.
	releaseRemoval()
	select {
	case <-removeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("removal never entered")
	}
	select {
	case <-recordEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("removal result never reached its publication boundary")
	}

	// Publish the transfer: it becomes durable, but the already-returned result
	// stays immutable and a later Close returns the same result.
	close(recordRelease)
	claudeSubWaitFor(t, func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		for _, e := range pool.cleanupErrs {
			if errors.Is(e, removeErr) {
				return true
			}
		}
		return false
	})
	if err := pool.Close(); !errors.Is(err, errClaudeSubPoolShutdownIncomplete) || errors.Is(err, removeErr) {
		t.Fatalf("later Close = %v; the published result must stay immutable", err)
	}
	pool.mu.Lock()
	owned := false
	for _, u := range pool.unresolved {
		if u.session == s {
			owned = true
		}
	}
	pool.mu.Unlock()
	if !owned {
		t.Error("late cleanup error did not leave durable ownership")
	}
}
