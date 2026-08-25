package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsTestServer is an in-process Codex WebSocket stand-in. It counts accepted
// connections so tests can assert whether a failure caused a redial.
type wsTestServer struct {
	*httptest.Server

	mu    sync.Mutex
	conns int
}

func newWSTestServer(t *testing.T, opts *websocket.AcceptOptions, handle func(conn *websocket.Conn, connNum int)) *wsTestServer {
	t.Helper()

	server := &wsTestServer{}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, opts)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		server.mu.Lock()
		server.conns++
		connNum := server.conns
		server.mu.Unlock()

		handle(conn, connNum)
	}))
	t.Cleanup(server.Close)
	return server
}

func (s *wsTestServer) wsURL() string {
	return "ws" + s.URL[4:]
}

func (s *wsTestServer) connCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

func wsTextDelta(text string) map[string]any {
	return map[string]any{"type": "response.output_text.delta", "delta": text}
}

func wsCompleted() map[string]any {
	return map[string]any{
		"type":     "response.completed",
		"response": map[string]any{"output": []any{}, "status": "success"},
	}
}

// wsServerRead consumes one client frame. Errors are reported rather than
// fatal: this runs on the server goroutine, where t.Fatalf is not allowed.
func wsServerRead(t *testing.T, conn *websocket.Conn) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err != nil {
		t.Errorf("server read request frame: %v", err)
		return false
	}
	return true
}

func wsServerWrite(t *testing.T, conn *websocket.Conn, event map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, mustJSON(t, event)); err != nil {
		t.Errorf("server write event: %v", err)
	}
}

func newTestWSProvider(t *testing.T, wsURL string) *codexWSProvider {
	t.Helper()

	cfg := ClientConfig{
		BaseURL:    "http://127.0.0.1:1",
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: &http.Client{Timeout: time.Second},
		Retry: RetryConfig{
			Enabled:        false,
			MaxAttempts:    1,
			InitialBackoff: time.Millisecond,
			MaxBackoff:     time.Millisecond,
		},
	}

	created, err := NewCodexResponsesWS(cfg)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	provider := created.(*codexWSProvider)
	provider.wsURL = wsURL
	t.Cleanup(func() {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		provider.closeConn()
	})
	return provider
}

func nextChunk(t *testing.T, stream <-chan ChatChunk, timeout time.Duration) ChatChunk {
	t.Helper()
	select {
	case chunk, ok := <-stream:
		if !ok {
			t.Fatal("stream closed before the expected chunk")
		}
		return chunk
	case <-time.After(timeout):
		t.Fatal("timed out waiting for the next chunk")
		return ChatChunk{}
	}
}

func drainChunks(stream <-chan ChatChunk) []ChatChunk {
	var chunks []ChatChunk
	for chunk := range stream {
		chunks = append(chunks, chunk)
	}
	return chunks
}

// TestCodexWSStreamsDeltasIncrementally proves the WebSocket transport forwards
// deltas as they arrive rather than buffering the turn. The server withholds
// the rest of the response until the test has already received the first delta,
// so a buffering transport deadlocks instead of passing.
func TestCodexWSStreamsDeltasIncrementally(t *testing.T) {
	release := make(chan struct{})

	server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
		if !wsServerRead(t, conn) {
			return
		}
		wsServerWrite(t, conn, wsTextDelta("first "))
		<-release
		wsServerWrite(t, conn, wsTextDelta("second"))
		wsServerWrite(t, conn, wsCompleted())
	})

	provider := newTestWSProvider(t, server.wsURL())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := provider.StreamChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}

	first := nextChunk(t, stream, 5*time.Second)
	if first.Done {
		t.Fatalf("first chunk is terminal: %#v", first)
	}
	if first.Delta.Content != "first " {
		t.Errorf("first delta: got %q, want %q", first.Delta.Content, "first ")
	}
	close(release)

	rest := drainChunks(stream)
	if len(rest) != 2 {
		t.Fatalf("chunks after the first delta: got %d, want 2 (%#v)", len(rest), rest)
	}
	if rest[0].Done {
		t.Errorf("second chunk is terminal: %#v", rest[0])
	}
	if rest[0].Delta.Content != "second" {
		t.Errorf("second delta: got %q, want %q", rest[0].Delta.Content, "second")
	}

	final := rest[1]
	if !final.Done {
		t.Fatalf("last chunk is not terminal: %#v", final)
	}
	if final.Delta.Content != "first second" {
		t.Errorf("terminal content: got %q, want %q", final.Delta.Content, "first second")
	}
}

// TestCodexWSReconnectsBeforeFirstDelta pins the safe half of the retry rule:
// nothing has been emitted, so resending the whole request on a fresh
// connection cannot duplicate visible output.
func TestCodexWSReconnectsBeforeFirstDelta(t *testing.T) {
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, connNum int) {
		if !wsServerRead(t, conn) {
			return
		}
		if connNum == 1 {
			_ = conn.CloseNow()
			return
		}
		wsServerWrite(t, conn, wsTextDelta("recovered"))
		wsServerWrite(t, conn, wsCompleted())
	})

	provider := newTestWSProvider(t, server.wsURL())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := provider.StreamChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}

	chunks := drainChunks(stream)
	if len(chunks) == 0 {
		t.Fatal("no chunks received")
	}
	final := chunks[len(chunks)-1]
	if final.Error != "" {
		t.Fatalf("turn failed instead of reconnecting: %s", final.Error)
	}
	if final.Delta.Content != "recovered" {
		t.Errorf("terminal content: got %q, want %q", final.Delta.Content, "recovered")
	}
	if got := server.connCount(); got != 2 {
		t.Errorf("connections: got %d, want 2 (one failed, one reconnect)", got)
	}
}

// TestCodexWSDoesNotRetryAfterFirstDelta pins the unsafe half: once a delta has
// reached the consumer, a reconnect may not replay the turn, because the
// replayed text would be duplicated on screen.
func TestCodexWSDoesNotRetryAfterFirstDelta(t *testing.T) {
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, _ int) {
		if !wsServerRead(t, conn) {
			return
		}
		wsServerWrite(t, conn, wsTextDelta("partial"))
		_ = conn.CloseNow()
	})

	provider := newTestWSProvider(t, server.wsURL())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := provider.StreamChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}

	chunks := drainChunks(stream)
	if len(chunks) != 2 {
		t.Fatalf("chunks: got %d, want 2 (one delta, one terminal error) (%#v)", len(chunks), chunks)
	}
	if chunks[0].Delta.Content != "partial" {
		t.Errorf("first delta: got %q, want %q", chunks[0].Delta.Content, "partial")
	}

	final := chunks[1]
	if !final.Done {
		t.Errorf("last chunk is not terminal: %#v", final)
	}
	if final.Error == "" {
		t.Errorf("terminal chunk carries no error: %#v", final)
	}
	for _, chunk := range chunks {
		if strings.Contains(chunk.Diagnostic, "falling back to HTTP") {
			t.Errorf("fell back to HTTP after emitting a delta: %q", chunk.Diagnostic)
		}
	}

	if got := server.connCount(); got != 1 {
		t.Errorf("connections: got %d, want 1 (no retry after the first delta)", got)
	}
}

// TestCodexWSUnaryPathStillReconnects guards the other direction: ChatCompletion
// buffers, so nothing is visible mid-stream and a failure must still reconnect.
func TestCodexWSUnaryPathStillReconnects(t *testing.T) {
	server := newWSTestServer(t, nil, func(conn *websocket.Conn, connNum int) {
		if !wsServerRead(t, conn) {
			return
		}
		wsServerWrite(t, conn, wsTextDelta("partial"))
		if connNum == 1 {
			_ = conn.CloseNow()
			return
		}
		wsServerWrite(t, conn, wsCompleted())
	})

	provider := newTestWSProvider(t, server.wsURL())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := provider.ChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if result.Message.Content != "partial" {
		t.Errorf("content: got %q, want %q", result.Message.Content, "partial")
	}
	if got := server.connCount(); got != 2 {
		t.Errorf("connections: got %d, want 2 (buffered path reconnects)", got)
	}
}
