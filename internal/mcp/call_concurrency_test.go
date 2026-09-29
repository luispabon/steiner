package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const callTestTimeout = 5 * time.Second

type callTestArgs struct{}

// newBlockingCallSession connects a Session over in-memory transports to a
// server with a "block" tool (signals started, then waits for release or its
// request context) and a "fast" tool (returns immediately).
func newBlockingCallSession(t *testing.T) (s *Session, started chan struct{}, release chan struct{}) {
	t.Helper()
	started = make(chan struct{}, 8)
	release = make(chan struct{})

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "1.0"}, nil)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "block", InputSchema: map[string]any{"type": "object"}},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ callTestArgs) (*mcpsdk.CallToolResult, any, error) {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return &mcpsdk.CallToolResult{}, nil, nil
		})
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "fast", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcpsdk.CallToolRequest, callTestArgs) (*mcpsdk.CallToolResult, any, error) {
			return &mcpsdk.CallToolResult{}, nil, nil
		})

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "client", Version: "1.0"}, nil)
	sdk, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	s = &Session{name: "fake", sdk: sdk, managerCtx: context.Background()}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		_ = s.Close()
	})
	return s, started, release
}

func startBlockedCall(t *testing.T, s *Session, started <-chan struct{}) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := s.Call(context.Background(), "block", nil)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(callTestTimeout):
		t.Fatal("blocking call never reached the server")
	}
	return done
}

// TestCallDoesNotSerializeConcurrentCalls pins that a call in flight does not
// hold the session mutex: another call completes, and a call with a short
// context returns its context error promptly instead of waiting on the lock.
func TestCallDoesNotSerializeConcurrentCalls(t *testing.T) {
	s, started, release := newBlockingCallSession(t)
	blocked := startBlockedCall(t, s, started)

	// A second call to the same server completes while the first is blocked.
	fastCtx, cancelFast := context.WithTimeout(context.Background(), callTestTimeout)
	defer cancelFast()
	if _, err := s.Call(fastCtx, "fast", nil); err != nil {
		t.Fatalf("fast call while another is in flight: %v", err)
	}

	// A short-deadline call honours its context even while another call blocks.
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelShort()
	begin := time.Now()
	shortDone := make(chan error, 1)
	go func() {
		_, err := s.Call(shortCtx, "block", nil)
		shortDone <- err
	}()
	select {
	case err := <-shortDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("short call error = %v, want deadline exceeded", err)
		}
		if elapsed := time.Since(begin); elapsed > callTestTimeout/2 {
			t.Errorf("short call took %v, want prompt return", elapsed)
		}
	case <-time.After(callTestTimeout):
		t.Fatal("short-deadline call did not return while another call was in flight")
	}

	close(release)
	select {
	case err := <-blocked:
		if err != nil {
			t.Errorf("blocked call error = %v, want nil", err)
		}
	case <-time.After(callTestTimeout):
		t.Fatal("blocked call did not finish after release")
	}
}

// TestCloseDoesNotHoldLockWhileClosing pins that Close and an in-flight call do
// not queue on the session mutex. The in-memory server only hangs up once its
// blocked handler returns, so sdk.Close stays pending until release; meanwhile
// a new Call must return promptly (an error, since the session is closing)
// rather than waiting on the mutex, and the transport error must not start a
// reconnect on a closed session.
func TestCloseDoesNotHoldLockWhileClosing(t *testing.T) {
	s, started, release := newBlockingCallSession(t)
	blocked := startBlockedCall(t, s, started)

	closeDone := make(chan struct{})
	go func() {
		_ = s.Close()
		close(closeDone)
	}()
	deadline := time.Now().Add(callTestTimeout)
	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close never started")
		}
		time.Sleep(time.Millisecond)
	}

	callDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTestTimeout/2)
		defer cancel()
		_, err := s.Call(ctx, "fast", nil)
		callDone <- err
	}()
	select {
	case err := <-callDone:
		if err == nil {
			t.Error("call on a closing session succeeded, want an error")
		}
	case <-time.After(callTestTimeout):
		t.Fatal("Call blocked while Close was in progress")
	}

	close(release)
	select {
	case <-closeDone:
	case <-time.After(callTestTimeout):
		t.Fatal("Close did not finish after the in-flight call was released")
	}
	select {
	case <-blocked:
	case <-time.After(callTestTimeout):
		t.Fatal("in-flight call did not return after Close")
	}

	s.mu.Lock()
	reconnecting := s.reconnecting
	s.mu.Unlock()
	if reconnecting {
		t.Error("closed session started a reconnect")
	}
}
