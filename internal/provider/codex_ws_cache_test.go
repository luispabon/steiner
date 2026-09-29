package provider

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
)

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
