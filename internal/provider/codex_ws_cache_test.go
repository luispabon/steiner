package provider

import (
	"context"
	"errors"
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
	cached, err := NewCachingCodexWS(cache, "test", func() (Provider, error) {
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
	cached, err := NewCachingCodexWS(cache, "test", func() (Provider, error) {
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
	cached, err := NewCachingCodexWS(cache, "test", build)
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

	fresh, err := NewCachingCodexWS(cache, "test", build)
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
