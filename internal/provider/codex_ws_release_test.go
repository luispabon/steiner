package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsConnectionState reads the connection and release-gate state of p under the
// same lock order the provider uses: gate, then mu.
func wsConnectionState(p *codexWSProvider) (connected, pending bool, inFlight int) {
	p.gateMu.Lock()
	defer p.gateMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn != nil, p.releasePending, p.inFlight
}

// nonReleasingProvider implements Provider without the release capability.
type nonReleasingProvider struct{}

func (p *nonReleasingProvider) ChatCompletion(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{}, errors.New("not implemented")
}

func (p *nonReleasingProvider) StreamChatCompletion(context.Context, ChatRequest) (<-chan ChatChunk, error) {
	return nil, errors.New("not implemented")
}

func (p *nonReleasingProvider) SupportsUsageStats() bool { return false }

func TestReleaseConnectionIgnoresProvidersWithoutTheCapability(t *testing.T) {
	if _, ok := any(&nonReleasingProvider{}).(ConnectionReleaser); ok {
		t.Fatal("nonReleasingProvider implements ConnectionReleaser, want it to be skipped")
	}
	ReleaseConnection(nil)
	ReleaseConnection(&nonReleasingProvider{})
}

// TestCodexWSReleaseConnectionClosesIdleConnection proves a released provider
// drops its socket and keepalive right away and stays usable afterwards.
func TestCodexWSReleaseConnectionClosesIdleConnection(t *testing.T) {
	serverClosed := make(chan struct{})
	var closeOnce sync.Once
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				closeOnce.Do(func() { close(serverClosed) })
				return
			}
			wsServerWrite(t, conn, wsCompleted())
		}
	})

	p := newTestWSProvider(t, server.wsURL())
	ReleaseConnection(p)
	if connected, _, inFlight := wsConnectionState(p); connected || inFlight != 0 {
		t.Fatalf("release before any request left state: connected=%v inFlight=%d", connected, inFlight)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := ChatRequest{
		Model:          "test-model",
		PromptCacheKey: "idle-key",
		Messages:       []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
	if _, err := p.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if connected, _, _ := wsConnectionState(p); !connected {
		t.Fatal("no connection after a successful request")
	}

	ReleaseConnection(p)

	connected, pending, inFlight := wsConnectionState(p)
	if connected || pending || inFlight != 0 {
		t.Errorf("after release: connected=%v pending=%v inFlight=%d, want all clear", connected, pending, inFlight)
	}
	p.mu.Lock()
	cancelKept, doneKept, dialKey := p.connCancel != nil, p.keepaliveDone != nil, p.dialCacheKey
	p.mu.Unlock()
	if cancelKept || doneKept {
		t.Errorf("keepalive not joined: cancel=%v done=%v", cancelKept, doneKept)
	}
	if dialKey != "idle-key" {
		t.Errorf("dialCacheKey = %q, want idle-key retained for the redial", dialKey)
	}

	select {
	case <-serverClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the server to see the connection close")
	}

	// A second release is a no-op, and the provider dials again on demand.
	ReleaseConnection(p)
	if _, err := p.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("ChatCompletion after release: %v", err)
	}
	if connected, _, _ := wsConnectionState(p); !connected {
		t.Fatal("no connection after the redial")
	}
	if got := server.connCount(); got != 2 {
		t.Errorf("connection count = %d, want 2 (one dial per request)", got)
	}
}

// TestCodexWSReleaseConnectionWaitsForActiveStream proves a release during a
// stream returns at once, leaves the socket alone, and closes it only once the
// stream has finished.
func TestCodexWSReleaseConnectionWaitsForActiveStream(t *testing.T) {
	finish := make(chan struct{})
	serverClosed := make(chan struct{})
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
		if !wsServerRead(t, conn) {
			return
		}
		wsServerWrite(t, conn, wsTextDelta("first"))
		<-finish
		wsServerWrite(t, conn, wsCompleted())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, _, err := conn.Read(ctx); err != nil {
			close(serverClosed)
		}
	})

	p := newTestWSProvider(t, server.wsURL())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := p.StreamChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}
	if first := nextChunk(t, stream, 5*time.Second); first.Delta.Content != "first" {
		t.Fatalf("first delta = %q, want first", first.Delta.Content)
	}
	gateState := func() (pending bool, inFlight int) {
		p.gateMu.Lock()
		defer p.gateMu.Unlock()
		return p.releasePending, p.inFlight
	}
	if pending, inFlight := gateState(); pending || inFlight != 1 {
		t.Fatalf("before release: pending=%v inFlight=%d, want one request in flight", pending, inFlight)
	}

	released := make(chan struct{})
	go func() {
		ReleaseConnection(p)
		close(released)
	}()
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("release blocked on an active stream")
	}

	if pending, inFlight := gateState(); !pending || inFlight != 1 {
		t.Errorf("pending=%v inFlight=%d, want pending with one request in flight", pending, inFlight)
	}
	select {
	case <-serverClosed:
		t.Fatal("connection closed under an active stream")
	default:
	}

	close(finish)

	terminal := nextChunk(t, stream, 10*time.Second)
	if terminal.Error != "" {
		t.Fatalf("stream failed after release: %s", terminal.Error)
	}
	if !terminal.Done || terminal.Delta.Content != "first" {
		t.Errorf("terminal chunk = %+v, want done with the streamed message", terminal)
	}
	for range stream {
	}

	if connected, pending, inFlight := wsConnectionState(p); connected || pending || inFlight != 0 {
		t.Errorf("after the stream ended: connected=%v pending=%v inFlight=%d, want all clear", connected, pending, inFlight)
	}
	select {
	case <-serverClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the deferred close")
	}
}

// TestCodexWSReleaseConnectionRedialKeepsAffinity proves the redial after a
// release sends the same affinity headers, so the follow-up lands on the same
// backend shard.
func TestCodexWSReleaseConnectionRedialKeepsAffinity(t *testing.T) {
	var mu sync.Mutex
	var sessions, threads, originators []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sessions = append(sessions, r.Header.Get("session-id"))
		threads = append(threads, r.Header.Get("thread-id"))
		originators = append(originators, r.Header.Get("originator"))
		mu.Unlock()

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			event := wsCompleted()
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			err = conn.Write(ctx, websocket.MessageText, mustJSON(t, event))
			cancel()
			if err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	p := newTestWSProvider(t, "ws"+server.URL[4:])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := ChatRequest{
		Model:          "test-model",
		PromptCacheKey: "affinity-key",
		Messages:       []Message{{Role: MessageRoleUser, Content: "hello"}},
	}

	for i := 0; i < 2; i++ {
		if _, err := p.ChatCompletion(ctx, request); err != nil {
			t.Fatalf("ChatCompletion %d: %v", i+1, err)
		}
		ReleaseConnection(p)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sessions) != 2 {
		t.Fatalf("dials = %d, want 2", len(sessions))
	}
	for i := range sessions {
		if sessions[i] != "affinity-key" {
			t.Errorf("dial %d session-id = %q, want affinity-key", i+1, sessions[i])
		}
		if threads[i] != "affinity-key" {
			t.Errorf("dial %d thread-id = %q, want affinity-key", i+1, threads[i])
		}
		if originators[i] != "codex_cli_rs" {
			t.Errorf("dial %d originator = %q, want codex_cli_rs", i+1, originators[i])
		}
	}
}

// TestCodexWSReleaseConnectionDuringConcurrentRequests proves concurrent use
// and release neither deadlock nor interrupt a request.
func TestCodexWSReleaseConnectionDuringConcurrentRequests(t *testing.T) {
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			wsServerWrite(t, conn, wsCompleted())
		}
	})

	p := newTestWSProvider(t, server.wsURL())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const callers = 4
	const callsPerCaller = 3

	requestsDone := make(chan struct{})
	errCh := make(chan error, callers*callsPerCaller)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsPerCaller; j++ {
				if _, err := p.ChatCompletion(ctx, ChatRequest{
					Model:    "test-model",
					Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
				}); err != nil {
					errCh <- err
				}
			}
		}()
	}

	var releasers sync.WaitGroup
	for i := 0; i < 2; i++ {
		releasers.Add(1)
		go func() {
			defer releasers.Done()
			for {
				select {
				case <-requestsDone:
					return
				default:
					ReleaseConnection(p)
				}
			}
		}()
	}

	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(requestsDone)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(20 * time.Second):
		t.Fatal("concurrent requests did not finish")
	}
	releasers.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent ChatCompletion: %v", err)
	}

	ReleaseConnection(p)
	if connected, pending, inFlight := wsConnectionState(p); connected || pending || inFlight != 0 {
		t.Errorf("after concurrent use: connected=%v pending=%v inFlight=%d, want all clear", connected, pending, inFlight)
	}
	p.mu.Lock()
	keepaliveKept := p.connCancel != nil || p.keepaliveDone != nil
	p.mu.Unlock()
	if keepaliveKept {
		t.Error("keepalive outlived the released connection")
	}
}

// TestCachingCodexWSDoesNotExposeRelease proves the cached wrapper leaves
// connection lifetime to the cache, so releasing a cached provider is a no-op.
func TestCachingCodexWSDoesNotExposeRelease(t *testing.T) {
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			wsServerWrite(t, conn, wsCompleted())
		}
	})

	inner := newTestWSProvider(t, server.wsURL())
	cache := NewCodexWSCache()
	t.Cleanup(func() { _ = cache.Close() })
	cached, err := NewCachingCodexWS(cache, "gen-1", "key", func() (Provider, error) {
		return inner, nil
	})
	if err != nil {
		t.Fatalf("NewCachingCodexWS: %v", err)
	}
	if _, ok := cached.(ConnectionReleaser); ok {
		t.Fatal("CachingCodexWS implements ConnectionReleaser; the cache owns its connection lifetime")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cached.ChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if connected, _, _ := wsConnectionState(inner); !connected {
		t.Fatal("no connection after a successful cached request")
	}

	ReleaseConnection(cached)

	if connected, _, _ := wsConnectionState(inner); !connected {
		t.Error("release through the cached wrapper closed a cache-owned connection")
	}
}
