package mcp

import (
	"context"
	"errors"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// beginTestReconnect arms one reconnect transition without starting a worker,
// so tests can drive resolution and commit directly.
func beginTestReconnect(t *testing.T, s *Session) chan struct{} {
	t.Helper()
	s.mu.Lock()
	done := s.beginReconnectLocked()
	s.mu.Unlock()
	if done == nil {
		t.Fatal("beginReconnectLocked started no transition on an idle session")
	}
	return done
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
	armed := s.reconnecting && s.reconnectDone == first && s.reconnectOutcome == nil
	again := s.beginReconnectLocked()
	s.mu.Unlock()
	if !armed {
		t.Error("beginReconnectLocked did not arm the transition with a fresh future")
	}
	if again != nil {
		t.Fatal("beginReconnectLocked started a second transition while one was in flight")
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
		t.Error("beginReconnectLocked reused a published future")
	}
	s.resolveReconnect(second, nil, ServerStatusConnected)
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
		onStatus: func(status ServerStatus, _ error) {
			if !s.mu.TryLock() {
				t.Error("reportStatus ran with the session mutex held")
				return
			}
			s.mu.Unlock()
			statuses = append(statuses, status)
		},
	}

	done := beginTestReconnect(t, s)
	want := errors.New("reconnect ended unavailable")
	s.resolveReconnect(done, want, ServerStatusUnavailable)

	// Duplicate resolution must not panic on a closed future, must not overwrite
	// the published outcome, and must not report a second transition.
	s.resolveReconnect(done, errors.New("duplicate"), ServerStatusConnected)

	select {
	case <-done:
	default:
		t.Fatal("resolveReconnect did not close the outcome future")
	}
	s.mu.Lock()
	outcome := s.reconnectOutcome
	s.mu.Unlock()
	if !errors.Is(outcome, want) {
		t.Errorf("published outcome = %v, want %v", outcome, want)
	}
	if len(statuses) != 1 || statuses[0] != ServerStatusUnavailable {
		t.Errorf("reported statuses = %v, want [unavailable]", statuses)
	}

	// A stale token cannot resolve the next transition.
	next := beginTestReconnect(t, s)
	s.resolveReconnect(done, errors.New("stale"), ServerStatusConnected)
	s.mu.Lock()
	pending := s.reconnecting && s.reconnectDone == next
	s.mu.Unlock()
	if !pending {
		t.Fatal("a stale token ended the in-flight reconnect transition")
	}
	select {
	case <-next:
		t.Fatal("a stale token closed the current outcome future")
	default:
	}

	s.resolveReconnect(next, nil, ServerStatusConnected)
	s.mu.Lock()
	reconnecting := s.reconnecting
	outcome = s.reconnectOutcome
	s.mu.Unlock()
	if reconnecting {
		t.Error("successful resolution left the session reconnecting")
	}
	if outcome != nil {
		t.Errorf("successful resolution outcome = %v, want nil", outcome)
	}
}

// TestCommitReconnectSwapsSessionAtomically proves the success path publishes
// the SDK swap and the outcome in one transition: the replaced session is
// returned for closing, the failure cap resets, and the transition ends.
func TestCommitReconnectSwapsSessionAtomically(t *testing.T) {
	current := newInMemoryClientSession(t)
	next := newInMemoryClientSession(t)
	s := &Session{name: "fake", sdk: current, managerCtx: context.Background()}
	done := beginTestReconnect(t, s)
	s.mu.Lock()
	s.consecutiveFailures = maxReconnectAttempts - 1
	s.mu.Unlock()

	old, committed := s.commitReconnect(done, next, nil)
	if !committed {
		t.Fatal("commitReconnect refused a live session")
	}
	if old != current {
		t.Error("commitReconnect did not return the replaced session")
	}

	s.mu.Lock()
	gotSDK, gotCap, reconnecting := s.sdk, s.consecutiveFailures, s.reconnecting
	s.mu.Unlock()
	if gotSDK != next {
		t.Error("commitReconnect did not adopt the new session")
	}
	if gotCap != 0 {
		t.Errorf("consecutiveFailures after a successful reconnect = %d, want 0", gotCap)
	}
	if reconnecting {
		t.Error("commitReconnect left the session reconnecting")
	}
	select {
	case <-done:
	default:
		t.Error("commitReconnect did not publish the outcome future")
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
			done := beginTestReconnect(t, s)

			tc.shut(s, cancel)

			old, committed := s.commitReconnect(done, next, nil)
			if committed || old != nil {
				t.Fatalf("commitReconnect committed after shutdown (old = %v)", old)
			}

			s.mu.Lock()
			keptSDK, reconnecting := s.sdk, s.reconnecting
			s.mu.Unlock()
			if keptSDK != current {
				t.Error("refused commit changed the live session")
			}
			if !reconnecting {
				t.Error("refused commit ended the in-flight reconnect transition")
			}

			s.resolveReconnect(done, tc.abort, ServerStatusUnavailable)
			s.mu.Lock()
			outcome := s.reconnectOutcome
			s.mu.Unlock()
			if !errors.Is(outcome, tc.abort) {
				t.Errorf("published outcome = %v, want %v", outcome, tc.abort)
			}
			select {
			case <-done:
			default:
				t.Error("abort resolution did not publish the outcome future")
			}
		})
	}
}
