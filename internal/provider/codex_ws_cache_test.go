package provider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type cachingCodexWSBlockingProvider struct {
	started chan struct{}
	release <-chan struct{}
	calls   int
}

func (p *cachingCodexWSBlockingProvider) ChatCompletion(context.Context, ChatRequest) (ChatResponse, error) {
	p.calls++
	if p.calls == 1 {
		close(p.started)
		<-p.release
	}
	return ChatResponse{}, nil
}

func (p *cachingCodexWSBlockingProvider) StreamChatCompletion(context.Context, ChatRequest) (<-chan ChatChunk, error) {
	return nil, errors.New("not implemented")
}

func (p *cachingCodexWSBlockingProvider) SupportsUsageStats() bool {
	return false
}

func TestCachingCodexWSEvictClosesConnection(t *testing.T) {
	connectionClosed := make(chan struct{})
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
		if !wsServerRead(t, conn) {
			return
		}
		wsServerWrite(t, conn, wsCompleted())
		if !wsServerRead(t, conn) {
			return
		}
		wsServerWrite(t, conn, map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"error": map[string]any{"message": "request rejected", "code": "invalid_request_error"},
			},
		})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, _, err := conn.Read(ctx); err != nil {
			close(connectionClosed)
		}
	})

	inner := newTestWSProvider(t, server.wsURL())
	cache := NewCodexWSCache()
	cached, err := NewCachingCodexWS(cache, "gen-1", "test", func() (Provider, error) {
		return inner, nil
	})
	if err != nil {
		t.Fatalf("NewCachingCodexWS: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cached.ChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	if _, err := cached.ChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "trigger eviction"}},
	}); err == nil {
		t.Fatal("ChatCompletion succeeded, want request failure")
	}

	select {
	case <-connectionClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for eviction to close the WebSocket connection")
	}

	inner.mu.Lock()
	defer inner.mu.Unlock()
	if inner.conn != nil || inner.connCancel != nil || inner.keepaliveDone != nil {
		t.Errorf("connection state not cleared: conn=%v cancel=%v done=%v", inner.conn != nil, inner.connCancel != nil, inner.keepaliveDone != nil)
	}
}

func TestCachingCodexWSCanceledQueuedCallReturnsBeforeActiveCompletes(t *testing.T) {
	release := make(chan struct{})
	inner := &cachingCodexWSBlockingProvider{
		started: make(chan struct{}),
		release: release,
	}
	cache := NewCodexWSCache()
	cached, err := NewCachingCodexWS(cache, "gen-1", "test", func() (Provider, error) {
		return inner, nil
	})
	if err != nil {
		t.Fatalf("NewCachingCodexWS: %v", err)
	}

	request := ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
	activeDone := make(chan error, 1)
	go func() {
		_, err := cached.ChatCompletion(context.Background(), request)
		activeDone <- err
	}()
	select {
	case <-inner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for active request")
	}

	released := false
	releaseActive := func() {
		if !released {
			close(release)
			released = true
		}
	}
	defer func() {
		releaseActive()
		select {
		case err := <-activeDone:
			if err != nil {
				t.Errorf("active ChatCompletion: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("timed out waiting for active request after release")
		}
	}()

	queuedCtx, cancel := context.WithCancel(context.Background())
	queuedDone := make(chan error, 1)
	go func() {
		_, err := cached.ChatCompletion(queuedCtx, request)
		queuedDone <- err
	}()
	cancel()

	select {
	case err := <-queuedDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued ChatCompletion error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued request did not return after context cancellation")
	}

	select {
	case err := <-activeDone:
		t.Fatalf("active request ended before release: %v", err)
	default:
	}
	if got := inner.calls; got != 1 {
		t.Fatalf("inner ChatCompletion calls before active release = %d, want 1", got)
	}
	cache.mu.Lock()
	if cache.instances["test"] != cached {
		cache.mu.Unlock()
		t.Fatal("canceled queued request evicted cache wrapper")
	}
	cache.mu.Unlock()

}

func TestCachingCodexWSEvictedWrapperCannotReconnect(t *testing.T) {
	connectionClosed := make(chan struct{})
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, connNum int) {
		if connNum == 1 {
			if !wsServerRead(t, conn) {
				return
			}
			wsServerWrite(t, conn, wsCompleted())
			if !wsServerRead(t, conn) {
				return
			}
			wsServerWrite(t, conn, map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"error": map[string]any{"message": "request rejected", "code": "invalid_request_error"},
				},
			})

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, _, err := conn.Read(ctx); err != nil {
				close(connectionClosed)
			}
			return
		}

		if !wsServerRead(t, conn) {
			return
		}
		wsServerWrite(t, conn, wsCompleted())
	})

	cache := NewCodexWSCache()
	build := func() (Provider, error) {
		return newTestWSProvider(t, server.wsURL()), nil
	}
	cached, err := NewCachingCodexWS(cache, "gen-1", "test", build)
	if err != nil {
		t.Fatalf("NewCachingCodexWS: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
	if _, err := cached.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("first ChatCompletion: %v", err)
	}
	if _, err := cached.ChatCompletion(ctx, request); err == nil {
		t.Fatal("second ChatCompletion succeeded, want request failure")
	}

	select {
	case <-connectionClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for eviction to close the WebSocket connection")
	}

	if _, err := cached.ChatCompletion(ctx, request); !errors.Is(err, errCachingCodexWSEvicted) {
		t.Fatalf("evicted ChatCompletion error = %v, want %v", err, errCachingCodexWSEvicted)
	}
	if got := server.connCount(); got != 1 {
		t.Fatalf("connections after evicted retry = %d, want 1", got)
	}

	fresh, err := NewCachingCodexWS(cache, "gen-1", "test", build)
	if err != nil {
		t.Fatalf("NewCachingCodexWS after eviction: %v", err)
	}
	if fresh == cached {
		t.Fatal("NewCachingCodexWS after eviction returned retired wrapper")
	}
	if _, err := fresh.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("fresh ChatCompletion: %v", err)
	}
	if got := server.connCount(); got != 2 {
		t.Errorf("connections after fresh wrapper = %d, want 2", got)
	}
}

func TestCachingCodexWSGenerationChangeRetiresIdleEntries(t *testing.T) {
	connectionClosed := make(chan struct{})
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, connNum int) {
		if !wsServerRead(t, conn) {
			return
		}
		wsServerWrite(t, conn, wsCompleted())
		if connNum != 1 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, _, err := conn.Read(ctx); err != nil && ctx.Err() == nil {
			close(connectionClosed)
		}
	})

	builds := 0
	build := func() (Provider, error) {
		builds++
		return newTestWSProvider(t, server.wsURL()), nil
	}
	cache := NewCodexWSCache()

	first, err := NewCachingCodexWS(cache, "gen-1", "codex|gen-1", build)
	if err != nil {
		t.Fatalf("NewCachingCodexWS: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	}
	if _, err := first.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("first ChatCompletion: %v", err)
	}

	// Same generation: another alias gets its own entry, and the first key is
	// reused without a rebuild.
	second, err := NewCachingCodexWS(cache, "gen-1", "other|gen-1", build)
	if err != nil {
		t.Fatalf("NewCachingCodexWS for second alias: %v", err)
	}
	again, err := NewCachingCodexWS(cache, "gen-1", "codex|gen-1", build)
	if err != nil {
		t.Fatalf("NewCachingCodexWS for reused key: %v", err)
	}
	if again != first {
		t.Fatal("same-generation acquisition rebuilt a live entry")
	}
	if builds != 2 {
		t.Fatalf("builds within one generation = %d, want 2", builds)
	}

	third, err := NewCachingCodexWS(cache, "gen-2", "codex|gen-2", build)
	if err != nil {
		t.Fatalf("NewCachingCodexWS for new generation: %v", err)
	}
	if builds != 3 {
		t.Fatalf("builds after generation change = %d, want 3", builds)
	}

	select {
	case <-connectionClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the generation change to close the idle connection")
	}

	if _, err := first.ChatCompletion(ctx, request); !errors.Is(err, errCachingCodexWSEvicted) {
		t.Fatalf("retired entry error = %v, want %v", err, errCachingCodexWSEvicted)
	}
	if _, err := second.ChatCompletion(ctx, request); !errors.Is(err, errCachingCodexWSEvicted) {
		t.Fatalf("retired sibling entry error = %v, want %v", err, errCachingCodexWSEvicted)
	}
	if _, err := third.ChatCompletion(ctx, request); err != nil {
		t.Fatalf("current-generation ChatCompletion: %v", err)
	}
}

// TestCachingCodexWSRetireKeepsActiveStream proves retirement never cuts an
// active stream: the connection closes only after the request ends.
func TestCachingCodexWSRetireKeepsActiveStream(t *testing.T) {
	tests := []struct {
		name   string
		retire func(t *testing.T, cache *CodexWSCache)
	}{
		{
			name: "generation change",
			retire: func(t *testing.T, cache *CodexWSCache) {
				t.Helper()
				if _, err := NewCachingCodexWS(cache, "gen-2", "codex|gen-2", func() (Provider, error) {
					return stubProvider{}, nil
				}); err != nil {
					t.Fatalf("NewCachingCodexWS for new generation: %v", err)
				}
			},
		},
		{
			name: "close",
			retire: func(t *testing.T, cache *CodexWSCache) {
				t.Helper()
				if err := cache.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			releaseComplete := make(chan struct{})
			connectionClosed := make(chan struct{})
			server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
				if !wsServerRead(t, conn) {
					return
				}
				wsServerWrite(t, conn, wsTextDelta("hello"))
				<-releaseComplete
				wsServerWrite(t, conn, wsCompleted())

				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if _, _, err := conn.Read(ctx); err != nil && ctx.Err() == nil {
					close(connectionClosed)
				}
			})

			inner := newTestWSProvider(t, server.wsURL())
			cache := NewCodexWSCache()
			cached, err := NewCachingCodexWS(cache, "gen-1", "codex|gen-1", func() (Provider, error) {
				return inner, nil
			})
			if err != nil {
				t.Fatalf("NewCachingCodexWS: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			chunks, err := cached.StreamChatCompletion(ctx, ChatRequest{
				Model:    "test-model",
				Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
			})
			if err != nil {
				t.Fatalf("StreamChatCompletion: %v", err)
			}
			if first := nextChunk(t, chunks, 5*time.Second); first.Delta.Content != "hello" {
				t.Fatalf("first chunk delta = %q, want %q", first.Delta.Content, "hello")
			}

			test.retire(t, cache)

			// The server is still holding the response open, so a retirement
			// that closed the stream early would surface as an error chunk or
			// a stream that never completes. Because the connection is reused
			// on success, the server-side read below only fails once the
			// retirement's deferred close runs.
			close(releaseComplete)
			var last ChatChunk
			for chunk := range chunks {
				last = chunk
			}
			if last.Error != "" {
				t.Fatalf("retired stream reported error %q, want a completed response", last.Error)
			}
			if !last.Done {
				t.Fatal("retired stream ended without a terminal chunk")
			}

			select {
			case <-connectionClosed:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for the retired stream's connection to close")
			}
			inner.mu.Lock()
			defer inner.mu.Unlock()
			if inner.conn != nil || inner.connCancel != nil || inner.keepaliveDone != nil {
				t.Errorf("connection state not cleared after stream end: conn=%v cancel=%v done=%v", inner.conn != nil, inner.connCancel != nil, inner.keepaliveDone != nil)
			}
		})
	}
}

func TestCachingCodexWSCloseIsIdempotentAndRejectsAcquisition(t *testing.T) {
	cache := NewCodexWSCache()
	entry, err := NewCachingCodexWS(cache, "gen-1", "codex|gen-1", func() (Provider, error) {
		return stubProvider{}, nil
	})
	if err != nil {
		t.Fatalf("NewCachingCodexWS: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := cache.Close(); err != nil {
			t.Fatalf("Close call %d: %v", i, err)
		}
	}

	built := false
	if _, err := NewCachingCodexWS(cache, "gen-1", "codex|gen-1", func() (Provider, error) {
		built = true
		return stubProvider{}, nil
	}); !errors.Is(err, errCodexWSCacheClosed) {
		t.Fatalf("acquisition after Close error = %v, want %v", err, errCodexWSCacheClosed)
	}
	if built {
		t.Fatal("closed cache invoked build")
	}

	if _, err := entry.ChatCompletion(context.Background(), ChatRequest{}); !errors.Is(err, errCachingCodexWSEvicted) {
		t.Fatalf("retired entry error = %v, want %v", err, errCachingCodexWSEvicted)
	}
}

func TestCachingCodexWSBuildRacingCloseIsRejected(t *testing.T) {
	cache := NewCodexWSCache()
	built := newTestWSProvider(t, "ws://127.0.0.1:1/codex-ws")
	buildStarted := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := NewCachingCodexWS(cache, "gen-1", "codex|gen-1", func() (Provider, error) {
			close(buildStarted)
			<-release
			return built, nil
		})
		result <- err
	}()

	<-buildStarted
	if err := cache.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	close(release)

	select {
	case err := <-result:
		if !errors.Is(err, errCodexWSCacheClosed) {
			t.Fatalf("acquisition racing Close error = %v, want %v", err, errCodexWSCacheClosed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("acquisition racing Close did not return")
	}

	cache.mu.Lock()
	entries := len(cache.instances)
	cache.mu.Unlock()
	if entries != 0 {
		t.Fatalf("cache entries after rejected build = %d, want 0", entries)
	}

	built.mu.Lock()
	conn := built.conn
	built.mu.Unlock()
	if conn != nil {
		t.Fatal("rejected build dialled a connection")
	}
}

func TestCachingCodexWSBuildRacingGenerationChangeIsRejected(t *testing.T) {
	cache := NewCodexWSCache()
	stale := newTestWSProvider(t, "ws://127.0.0.1:1/codex-ws")
	buildStarted := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := NewCachingCodexWS(cache, "gen-1", "codex|gen-1", func() (Provider, error) {
			close(buildStarted)
			<-release
			return stale, nil
		})
		result <- err
	}()

	<-buildStarted
	current, err := NewCachingCodexWS(cache, "gen-2", "codex|gen-2", func() (Provider, error) {
		return stubProvider{}, nil
	})
	if err != nil {
		t.Fatalf("NewCachingCodexWS for new generation: %v", err)
	}
	close(release)

	select {
	case err := <-result:
		if !errors.Is(err, errCodexWSCacheGenerationChanged) {
			t.Fatalf("build racing a generation change error = %v, want %v", err, errCodexWSCacheGenerationChanged)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("build racing a generation change did not return")
	}

	again, err := NewCachingCodexWS(cache, "gen-2", "codex|gen-2", func() (Provider, error) {
		t.Error("current-generation entry was evicted by a stale build")
		return stubProvider{}, nil
	})
	if err != nil {
		t.Fatalf("NewCachingCodexWS for current generation: %v", err)
	}
	if again != current {
		t.Fatal("stale build flipped the current generation")
	}
}

func TestCachingCodexWSRetirementKeepsActiveRequestAndRejectsNewOnes(t *testing.T) {
	release := make(chan struct{})
	inner := &cachingCodexWSBlockingProvider{started: make(chan struct{}), release: release}
	cache := NewCodexWSCache()
	cached, err := NewCachingCodexWS(cache, "gen-1", "codex|gen-1", func() (Provider, error) {
		return inner, nil
	})
	if err != nil {
		t.Fatalf("NewCachingCodexWS: %v", err)
	}

	activeDone := make(chan error, 1)
	go func() {
		_, err := cached.ChatCompletion(context.Background(), ChatRequest{})
		activeDone <- err
	}()
	select {
	case <-inner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the active request")
	}

	if _, err := NewCachingCodexWS(cache, "gen-2", "codex|gen-2", func() (Provider, error) {
		return stubProvider{}, nil
	}); err != nil {
		t.Fatalf("NewCachingCodexWS for new generation: %v", err)
	}

	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queuedDone := make(chan error, 1)
	go func() {
		_, err := cached.ChatCompletion(queuedCtx, ChatRequest{})
		queuedDone <- err
	}()
	cancelQueued()
	select {
	case err := <-queuedDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued call on a retired entry error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued call did not return after cancellation")
	}

	select {
	case err := <-activeDone:
		t.Fatalf("active request ended before release: %v", err)
	default:
	}

	close(release)
	select {
	case err := <-activeDone:
		if err != nil {
			t.Fatalf("active ChatCompletion after retirement: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the active request")
	}

	if _, err := cached.ChatCompletion(context.Background(), ChatRequest{}); !errors.Is(err, errCachingCodexWSEvicted) {
		t.Fatalf("retired entry error = %v, want %v", err, errCachingCodexWSEvicted)
	}
	if inner.calls != 1 {
		t.Fatalf("inner ChatCompletion calls = %d, want 1", inner.calls)
	}
}

func TestCachingCodexWSConcurrentAcquireAndClose(t *testing.T) {
	cache := NewCodexWSCache()
	generations := []string{"gen-1", "gen-2"}

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				generation := generations[(i+j)%len(generations)]
				prov, err := NewCachingCodexWS(cache, generation, "codex|"+generation, func() (Provider, error) {
					return stubProvider{}, nil
				})
				if err != nil {
					if !errors.Is(err, errCodexWSCacheClosed) && !errors.Is(err, errCodexWSCacheGenerationChanged) {
						errCh <- err
						return
					}
					continue
				}
				// A concurrently retired entry rejects the call; the error is
				// expected and only here to exercise the retirement path.
				_, _ = prov.ChatCompletion(context.Background(), ChatRequest{})
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond)
		if err := cache.Close(); err != nil {
			errCh <- err
		}
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent acquisition error: %v", err)
	}

	if _, err := NewCachingCodexWS(cache, "gen-1", "codex|gen-1", func() (Provider, error) {
		return stubProvider{}, nil
	}); !errors.Is(err, errCodexWSCacheClosed) {
		t.Fatalf("acquisition after concurrent Close error = %v, want %v", err, errCodexWSCacheClosed)
	}
}
