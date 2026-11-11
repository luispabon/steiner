package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// beginTestReconnect arms one reconnect transition without starting a worker,
// so tests can drive resolution and commit directly.
func beginTestReconnect(t *testing.T, s *Session) *reconnectAttempt {
	t.Helper()
	s.mu.Lock()
	attempt := s.beginReconnectLocked()
	s.mu.Unlock()
	if attempt == nil {
		t.Fatal("beginReconnectLocked started no transition on an idle session")
	}
	return attempt
}

// newInMemoryClientSession connects a client to an empty in-memory MCP server
// and returns the live SDK session.
func newInMemoryClientSession(t *testing.T) *mcpsdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "1.0"}, nil)
	serverT, clientT := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1.0"}, nil)
	sdk, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sdk.Close() })
	return sdk
}

// TestBeginReconnectLockedGuardsOneTransitionAtATime pins the begin contract: a
// second begin while a transition is in flight starts nothing, the
// consecutive-failure cap starts nothing, and a new attempt allocates a fresh
// future with no outcome.
func TestBeginReconnectLockedGuardsOneTransitionAtATime(t *testing.T) {
	s := &Session{name: "fake", managerCtx: context.Background()}
	first := beginTestReconnect(t, s)

	s.mu.Lock()
	armed := s.reconnect == first
	again := s.beginReconnectLocked()
	s.mu.Unlock()
	if !armed {
		t.Error("beginReconnectLocked did not record the in-flight transition")
	}
	if again != nil {
		t.Fatal("beginReconnectLocked started a second transition while one was in flight")
	}
	select {
	case <-first.done:
		t.Fatal("an unresolved attempt reports a closed future")
	default:
	}
	if first.outcome != nil {
		t.Errorf("unresolved attempt outcome = %v, want nil", first.outcome)
	}

	s.mu.Lock()
	s.consecutiveFailures = maxReconnectAttempts
	s.mu.Unlock()
	s.resolveReconnect(first, errors.New("unavailable"), ServerStatusUnavailable)

	s.mu.Lock()
	capped := s.beginReconnectLocked()
	s.mu.Unlock()
	if capped != nil {
		t.Fatal("beginReconnectLocked started a transition after the consecutive-failure cap")
	}

	s.mu.Lock()
	s.consecutiveFailures = 0
	s.mu.Unlock()
	second := beginTestReconnect(t, s)
	if second == first {
		t.Error("beginReconnectLocked reused a published attempt")
	}
	s.resolveReconnect(second, nil, ServerStatusConnected)
	if got := s.waitForReconnect(context.Background()); got != nil {
		t.Errorf("waitForReconnect on an idle session = %v, want nil", got)
	}
}

// TestResolveReconnectPublishesOutcomeOnce proves the resolution contract: the
// outcome is recorded and the future closed together, duplicate and stale tokens
// are no-ops, the original outcome survives a duplicate, and status reporting
// runs without the session mutex held.
func TestResolveReconnectPublishesOutcomeOnce(t *testing.T) {
	var (
		s        *Session
		statuses []ServerStatus
	)
	s = &Session{
		name:       "fake",
		managerCtx: context.Background(),
		onStatus: func(report StatusReport) {
			if !s.mu.TryLock() {
				t.Error("reportStatus ran with the session mutex held")
				return
			}
			s.mu.Unlock()
			statuses = append(statuses, report.Status)
		},
	}

	first := beginTestReconnect(t, s)
	want := errors.New("reconnect ended unavailable")
	s.resolveReconnect(first, want, ServerStatusUnavailable)

	// Duplicate resolution must not panic on a closed future, must not overwrite
	// the published outcome, and must not report a second transition.
	s.resolveReconnect(first, errors.New("duplicate"), ServerStatusConnected)

	select {
	case <-first.done:
	default:
		t.Fatal("resolveReconnect did not close the outcome future")
	}
	if !errors.Is(first.outcome, want) {
		t.Errorf("published outcome = %v, want %v", first.outcome, want)
	}
	if len(statuses) != 1 || statuses[0] != ServerStatusUnavailable {
		t.Errorf("reported statuses = %v, want [unavailable]", statuses)
	}

	// A stale token cannot resolve the next transition.
	next := beginTestReconnect(t, s)
	s.resolveReconnect(first, errors.New("stale"), ServerStatusConnected)
	s.mu.Lock()
	pending := s.reconnect == next
	s.mu.Unlock()
	if !pending {
		t.Fatal("a stale token ended the in-flight reconnect transition")
	}
	select {
	case <-next.done:
		t.Fatal("a stale token closed the current outcome future")
	default:
	}

	s.resolveReconnect(next, nil, ServerStatusConnected)
	s.mu.Lock()
	idle := s.reconnect == nil
	s.mu.Unlock()
	if !idle {
		t.Error("successful resolution left a transition in flight")
	}
	if next.outcome != nil {
		t.Errorf("successful resolution outcome = %v, want nil", next.outcome)
	}
}

// TestWaitReconnectAttemptKeepsOwnOutcome is the regression for waiters reading a
// shared outcome field: a waiter released by one attempt's future must observe
// that attempt's outcome even when the next attempt has already started and
// resolved, and waiter cancellation must not resolve the attempt.
func TestWaitReconnectAttemptKeepsOwnOutcome(t *testing.T) {
	s := &Session{name: "fake", managerCtx: context.Background()}
	first := beginTestReconnect(t, s)

	// A waiter that starts while the first attempt is still in flight.
	inflight := make(chan error, 1)
	go func() { inflight <- waitReconnectAttempt(context.Background(), first) }()

	failure := errors.New("reconnect ended unavailable")
	s.resolveReconnect(first, failure, ServerStatusUnavailable)

	// The next attempt starts and resolves successfully. A waiter that reads a
	// shared outcome field here would observe this attempt's nil outcome.
	second := beginTestReconnect(t, s)
	s.resolveReconnect(second, nil, ServerStatusConnected)

	select {
	case got := <-inflight:
		if !errors.Is(got, failure) {
			t.Errorf("waiter outcome = %v, want the first attempt's %v", got, failure)
		}
	case <-time.After(callTestTimeout):
		t.Fatal("waiter never returned")
	}

	// A waiter that captures the first attempt only after the second resolved
	// must still see the first attempt's outcome.
	if got := waitReconnectAttempt(context.Background(), first); !errors.Is(got, failure) {
		t.Errorf("late waiter outcome = %v, want %v", got, failure)
	}

	// Cancellation is preserved: a waiter on an unresolved attempt returns its
	// context error and leaves the attempt pending.
	pending := beginTestReconnect(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := waitReconnectAttempt(ctx, pending); !errors.Is(got, context.Canceled) {
		t.Errorf("cancelled waiter outcome = %v, want context.Canceled", got)
	}
	select {
	case <-pending.done:
		t.Error("a cancelled waiter resolved the pending attempt")
	default:
	}
	s.resolveReconnect(pending, errors.New("unavailable"), ServerStatusUnavailable)
}

// TestCommitReconnectSwapsSessionAtomically proves the success path publishes
// the SDK swap and the outcome in one transition: the replaced session is
// returned for closing, the failure cap resets, and the transition ends.
func TestCommitReconnectSwapsSessionAtomically(t *testing.T) {
	current := newInMemoryClientSession(t)
	next := newInMemoryClientSession(t)
	s := &Session{name: "fake", sdk: current, managerCtx: context.Background()}
	attempt := beginTestReconnect(t, s)
	s.mu.Lock()
	s.consecutiveFailures = maxReconnectAttempts - 1
	s.mu.Unlock()

	old, _, committed := s.commitReconnect(attempt, next, nil)
	if !committed {
		t.Fatal("commitReconnect refused a live session")
	}
	if old != current {
		t.Error("commitReconnect did not return the replaced session")
	}

	s.mu.Lock()
	gotSDK, gotCap, inFlight := s.sdk, s.consecutiveFailures, s.reconnect
	s.mu.Unlock()
	if gotSDK != next {
		t.Error("commitReconnect did not adopt the new session")
	}
	if gotCap != 0 {
		t.Errorf("consecutiveFailures after a successful reconnect = %d, want 0", gotCap)
	}
	if inFlight != nil {
		t.Error("commitReconnect left a transition in flight")
	}
	select {
	case <-attempt.done:
	default:
		t.Error("commitReconnect did not publish the outcome future")
	}
	if attempt.outcome != nil {
		t.Errorf("successful commit outcome = %v, want nil", attempt.outcome)
	}
	if want := next.InitializeResult().ProtocolVersion; s.ProtocolVersion() != want {
		t.Errorf("protocol version = %q, want %q", s.ProtocolVersion(), want)
	}
}

// TestCommitReconnectRefusesAfterShutdown proves a successful attempt that races
// session or manager shutdown never adopts its session: the live session and the
// in-flight transition stay untouched, and the abort resolution still releases
// callers with the distinct abort error.
func TestCommitReconnectRefusesAfterShutdown(t *testing.T) {
	tests := []struct {
		name  string
		abort error
		shut  func(*Session, context.CancelFunc)
	}{
		{
			name:  "session closed",
			abort: errors.New("reconnect aborted: session closed"),
			shut: func(s *Session, _ context.CancelFunc) {
				s.mu.Lock()
				s.closed = true
				s.mu.Unlock()
			},
		},
		{
			name:  "manager closed",
			abort: errors.New("reconnect aborted: manager closed: context canceled"),
			shut: func(_ *Session, cancel context.CancelFunc) {
				cancel()
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			current := newInMemoryClientSession(t)
			next := newInMemoryClientSession(t)
			managerCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &Session{name: "fake", sdk: current, managerCtx: managerCtx}
			attempt := beginTestReconnect(t, s)

			tc.shut(s, cancel)

			old, _, committed := s.commitReconnect(attempt, next, nil)
			if committed || old != nil {
				t.Fatalf("commitReconnect committed after shutdown (old = %v)", old)
			}

			s.mu.Lock()
			keptSDK, inFlight := s.sdk, s.reconnect
			s.mu.Unlock()
			if keptSDK != current {
				t.Error("refused commit changed the live session")
			}
			if inFlight != attempt {
				t.Error("refused commit ended the in-flight reconnect transition")
			}

			s.resolveReconnect(attempt, tc.abort, ServerStatusUnavailable)
			if !errors.Is(attempt.outcome, tc.abort) {
				t.Errorf("published outcome = %v, want %v", attempt.outcome, tc.abort)
			}
			select {
			case <-attempt.done:
			default:
				t.Error("abort resolution did not publish the outcome future")
			}
		})
	}
}

// TestCommitReconnectRejectsStaleAttempt proves a resolved attempt cannot swap
// the session or publish its future once a newer attempt owns the transition.
func TestCommitReconnectRejectsStaleAttempt(t *testing.T) {
	current := newInMemoryClientSession(t)
	next := newInMemoryClientSession(t)
	s := &Session{name: "fake", sdk: current, managerCtx: context.Background()}
	stale := beginTestReconnect(t, s)
	s.resolveReconnect(stale, errors.New("unavailable"), ServerStatusUnavailable)
	live := beginTestReconnect(t, s)

	old, _, committed := s.commitReconnect(stale, next, nil)
	if committed || old != nil {
		t.Fatal("commitReconnect committed a stale attempt")
	}

	s.mu.Lock()
	keptSDK, inFlight := s.sdk, s.reconnect
	s.mu.Unlock()
	if keptSDK != current {
		t.Error("stale commit changed the live session")
	}
	if inFlight != live {
		t.Error("stale commit ended the newer transition")
	}
	select {
	case <-live.done:
		t.Error("stale commit closed the newer attempt's future")
	default:
	}

	s.resolveReconnect(live, nil, ServerStatusConnected)
}
