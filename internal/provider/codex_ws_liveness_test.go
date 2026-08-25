package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestCodexWSKeepalivePings proves the per-connection keepalive actually puts
// ping frames on the wire while the connection is otherwise idle, which is what
// stops the backend closing it after ~5 minutes of silence.
func TestCodexWSKeepalivePings(t *testing.T) {
	pings := make(chan struct{}, 16)

	opts := &websocket.AcceptOptions{
		OnPingReceived: func(_ context.Context, _ []byte) bool {
			select {
			case pings <- struct{}{}:
			default:
			}
			return true
		},
	}

	server := newWSTestServer(t, opts, func(conn *websocket.Conn, _ int) {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			typ, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			wsServerWrite(t, conn, wsCompleted())
		}
	})

	provider := newTestWSProvider(t, server.wsURL())
	provider.pingInterval = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := provider.ChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-pings:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for keepalive ping %d", i+1)
		}
	}
}

// TestCodexWSKeepaliveStopsWithConnection pins that the keepalive goroutine is
// bound to the connection it pings: closeConn must not return until it is gone.
func TestCodexWSKeepaliveStopsWithConnection(t *testing.T) {
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
		if !wsServerRead(t, conn) {
			return
		}
		wsServerWrite(t, conn, wsCompleted())
	})

	provider := newTestWSProvider(t, server.wsURL())
	provider.pingInterval = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := provider.ChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	provider.mu.Lock()
	done := provider.keepaliveDone
	if done == nil {
		provider.mu.Unlock()
		t.Fatal("no keepalive was started for the live connection")
	}
	provider.closeConn()
	provider.mu.Unlock()

	select {
	case <-done:
	default:
		t.Error("closeConn returned while the keepalive goroutine was still running")
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.conn != nil || provider.connCancel != nil || provider.keepaliveDone != nil {
		t.Errorf("connection state not cleared: conn=%v cancel=%v done=%v", provider.conn != nil, provider.connCancel != nil, provider.keepaliveDone != nil)
	}
}

// TestCodexWSInterFrameDeadline pins that the read deadline measures the gap
// between frames, not the length of the response: a stream that keeps producing
// deltas survives well past the deadline, while one that goes silent trips it.
func TestCodexWSInterFrameDeadline(t *testing.T) {
	const interFrame = 150 * time.Millisecond

	tests := []struct {
		name      string
		deltas    int
		gap       time.Duration
		stall     bool
		wantError string
	}{
		{
			name:      "stalled stream trips the deadline",
			deltas:    1,
			gap:       0,
			stall:     true,
			wantError: "read response",
		},
		{
			name:   "slow but progressing stream survives past the deadline",
			deltas: 6,
			gap:    50 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
				if !wsServerRead(t, conn) {
					return
				}
				for i := 0; i < tt.deltas; i++ {
					wsServerWrite(t, conn, wsTextDelta("x"))
					time.Sleep(tt.gap)
				}
				if tt.stall {
					// Hold the socket open and silent so only the inter-frame
					// deadline can end the read.
					time.Sleep(5 * time.Second)
					return
				}
				wsServerWrite(t, conn, wsCompleted())
			})

			provider := newTestWSProvider(t, server.wsURL())
			provider.interFrameTimeout = interFrame

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			result, err := provider.ChatCompletion(ctx, ChatRequest{
				Model:    "test-model",
				Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
			})

			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("expected the stalled stream to fail, got %#v", result)
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Errorf("error: got %q, want it to contain %q", err.Error(), tt.wantError)
				}
				return
			}

			if err != nil {
				t.Fatalf("ChatCompletion: %v", err)
			}
			if got, want := result.Message.Content, strings.Repeat("x", tt.deltas); got != want {
				t.Errorf("content: got %q, want %q", got, want)
			}
		})
	}
}

// TestCodexWSKeepaliveDuringActiveStream exercises the interaction the three
// fixes create together: pings written while the request loop is reading, pongs
// dispatched through that same read loop, and both writers contending the
// connection's write mutex. The first turn also leaves the socket idle long
// enough for several pongs to go stale, so the second turn's read loop receives
// pongs whose ping registrations are already gone.
func TestCodexWSKeepaliveDuringActiveStream(t *testing.T) {
	const (
		deltas = 8
		gap    = 50 * time.Millisecond
	)

	server := newWSTestServer(t, nil, func(conn *websocket.Conn, connNum int) {
		if connNum > 1 {
			t.Errorf("redialled during a healthy stream: connection %d", connNum)
			return
		}

		// One reader owns the socket: coder/websocket does not support
		// concurrent Reader calls, and this loop is what answers the client's
		// pings with pongs while a response is in flight.
		requests := make(chan struct{}, 2)
		reader := make(chan struct{})
		go func() {
			defer close(reader)
			for {
				typ, _, err := conn.Read(context.Background())
				if err != nil {
					return
				}
				if typ == websocket.MessageText {
					requests <- struct{}{}
				}
			}
		}()
		defer func() {
			_ = conn.CloseNow()
			<-reader
		}()

		awaitRequest := func() bool {
			select {
			case <-requests:
				return true
			case <-time.After(5 * time.Second):
				t.Error("server timed out waiting for a request frame")
				return false
			}
		}

		if !awaitRequest() {
			return
		}
		wsServerWrite(t, conn, wsCompleted())

		if !awaitRequest() {
			return
		}
		for i := 0; i < deltas; i++ {
			wsServerWrite(t, conn, wsTextDelta("x"))
			time.Sleep(gap)
		}
		wsServerWrite(t, conn, wsCompleted())
	})

	provider := newTestWSProvider(t, server.wsURL())
	provider.pingInterval = 20 * time.Millisecond
	provider.interFrameTimeout = 150 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	request := ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}

	if _, err := provider.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("priming ChatCompletion: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	result, err := provider.ChatCompletion(ctx, request)
	if err != nil {
		t.Fatalf("streaming ChatCompletion: %v", err)
	}
	if got, want := result.Message.Content, strings.Repeat("x", deltas); got != want {
		t.Errorf("content: got %q, want %q", got, want)
	}
	if got := server.connCount(); got != 1 {
		t.Errorf("connections: got %d, want 1", got)
	}
}
