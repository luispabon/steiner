package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCodexWSFrameStructure(t *testing.T) {
	request := ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: MessageRoleUser, Content: "test"},
		},
	}

	frameBytes, err := buildWSRequestFrame(request, "test-model")
	if err != nil {
		t.Fatalf("buildWSRequestFrame: %v", err)
	}

	var frame map[string]any
	if err := json.Unmarshal(frameBytes, &frame); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}

	if frame["type"] != "response.create" {
		t.Errorf("type: got %v, want response.create", frame["type"])
	}
	if frame["model"] != "test-model" {
		t.Errorf("model: got %v, want test-model", frame["model"])
	}
	if _, ok := frame["client_metadata"]; ok {
		t.Errorf("client_metadata: got %#v, want absent", frame["client_metadata"])
	}
}

func TestCodexWSFramePrecedence(t *testing.T) {
	request := ChatRequest{
		Model:    "request-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "test"}},
		Params: map[string]any{
			"type":  "params-type",
			"model": "params-model",
		},
		ExtraParams: map[string]any{
			"type":  "extra-type",
			"model": "extra-model",
		},
	}

	frameBytes, err := buildWSRequestFrame(request, "frame-model")
	if err != nil {
		t.Fatalf("buildWSRequestFrame: %v", err)
	}

	var frame map[string]any
	if err := json.Unmarshal(frameBytes, &frame); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if got, want := frame["type"], "extra-type"; got != want {
		t.Errorf("type: got %v, want %v", got, want)
	}
	if got, want := frame["model"], "frame-model"; got != want {
		t.Errorf("model: got %v, want %v", got, want)
	}
}

func TestCodexWSNoClientMetadataAcrossSequentialCalls(t *testing.T) {
	var mu sync.Mutex
	var framesWithMetadata int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			typ, data, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}

			var frame map[string]any
			if err := json.Unmarshal(data, &frame); err != nil {
				return
			}
			if _, ok := frame["client_metadata"]; ok {
				mu.Lock()
				framesWithMetadata++
				mu.Unlock()
			}

			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			_ = conn.Write(ctx, websocket.MessageText, mustJSON(t, map[string]any{
				"type":     "response.completed",
				"response": map[string]any{"output": []any{}, "status": "success"},
			}))
			cancel()
		}
	}))
	defer server.Close()

	provider := newTestWSProvider(t, "ws"+server.URL[4:])

	for callNum := 1; callNum <= 3; callNum++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := provider.ChatCompletion(ctx, ChatRequest{
			Model:    "test-model",
			Messages: []Message{{Role: MessageRoleUser, Content: fmt.Sprintf("call %d", callNum)}},
		})
		cancel()
		if err != nil {
			t.Fatalf("call %d: %v", callNum, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if framesWithMetadata != 0 {
		t.Errorf("frames carrying client_metadata: got %d, want 0", framesWithMetadata)
	}
}

// TestCodexWSDialFailureErrors pins that a dial failure surfaces as an error on
// both public methods. The transport has no HTTP fallback: codex.transport is an
// explicit opt-in, so a caller who asked for WebSocket is told when it does not
// work rather than being silently downgraded.
func TestCodexWSDialFailureErrors(t *testing.T) {
	cfg := ClientConfig{
		BaseURL:    "http://localhost:8080",
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: &http.Client{},
		Retry: RetryConfig{
			Enabled:        false,
			MaxAttempts:    1,
			InitialBackoff: time.Millisecond,
			MaxBackoff:     time.Millisecond,
		},
	}

	newUnreachable := func(t *testing.T) *codexWSProvider {
		t.Helper()
		p, err := NewCodexResponsesWS(cfg)
		if err != nil {
			t.Fatalf("create provider: %v", err)
		}
		wsProvider := p.(*codexWSProvider)
		wsProvider.wsURL = "ws://invalid-unreachable-host-that-will-not-dial:9999"
		return wsProvider
	}

	request := ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "test"}},
	}

	t.Run("unary path returns an error", func(t *testing.T) {
		wsProvider := newUnreachable(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := wsProvider.ChatCompletion(ctx, request); err == nil {
			t.Error("ChatCompletion returned nil error on an undialable endpoint")
		}
	})

	t.Run("stream path reports the error and no fallback diagnostic", func(t *testing.T) {
		wsProvider := newUnreachable(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		stream, err := wsProvider.StreamChatCompletion(ctx, request)
		if err != nil {
			t.Fatalf("StreamChatCompletion returned early error: %v", err)
		}

		var sawError bool
		for chunk := range stream {
			if chunk.Error != "" {
				sawError = true
			}
			if strings.Contains(chunk.Diagnostic, "falling back to HTTP") {
				t.Error("stream emitted an HTTP fallback diagnostic; the fallback path was removed")
			}
		}
		if !sawError {
			t.Error("stream completed without reporting the dial failure")
		}
	})
}

func TestCodexWSSupportsUsageStats(t *testing.T) {
	cfg := ClientConfig{
		BaseURL:    "http://localhost:8080",
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: &http.Client{},
	}

	provider, err := NewCodexResponsesWS(cfg)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	if !provider.SupportsUsageStats() {
		t.Error("SupportsUsageStats should return true")
	}
}

func TestCodexWSLargeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		typ, data, err := conn.Read(ctx)
		cancel()

		if err != nil || typ != websocket.MessageText {
			return
		}

		var frame map[string]any
		if err := json.Unmarshal(data, &frame); err != nil {
			return
		}

		largeContent := ""
		for i := 0; i < 50000; i++ {
			largeContent += "x"
		}

		deltaEvent := map[string]any{
			"type":  "response.output_text.delta",
			"delta": largeContent,
		}
		deltaJSON, _ := json.Marshal(deltaEvent)
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Write(ctx, websocket.MessageText, deltaJSON)
		cancel()

		itemDoneEvent := map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":  "text",
				"id":    "item-1",
				"index": 0,
			},
		}
		itemDoneJSON, _ := json.Marshal(itemDoneEvent)
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Write(ctx, websocket.MessageText, itemDoneJSON)
		cancel()

		completedEvent := map[string]any{
			"type":     "response.completed",
			"response": map[string]any{"status": "success"},
		}
		completedJSON, _ := json.Marshal(completedEvent)
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Write(ctx, websocket.MessageText, completedJSON)
		cancel()
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[4:]

	cfg := ClientConfig{
		BaseURL:    "http://localhost:8080",
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: &http.Client{},
	}

	provider, err := NewCodexResponsesWS(cfg)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	wsProvider := provider.(*codexWSProvider)
	wsProvider.wsURL = wsURL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	result, err := wsProvider.ChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "hey"}},
	})
	cancel()

	if err != nil {
		t.Fatalf("ChatCompletion failed: %v", err)
	}

	if len(result.Message.Content) == 0 {
		t.Error("expected non-empty response content")
	}

	if len(result.Message.Content) < 50000 {
		t.Errorf("expected response content >= 50000 chars, got %d", len(result.Message.Content))
	}
}

func TestBuildWSHeadersCacheAffinity(t *testing.T) {
	tests := []struct {
		name     string
		cacheKey string
		wantSet  bool
	}{
		{name: "non-empty cache key sets affinity headers", cacheKey: "session-abc", wantSet: true},
		{name: "empty cache key omits affinity headers", cacheKey: "", wantSet: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := buildWSHeaders("test-key", nil, tt.cacheKey)

			sessionID := headers.Get("session-id")
			threadID := headers.Get("thread-id")
			originator := headers.Get("originator")

			if tt.wantSet {
				if sessionID != tt.cacheKey {
					t.Errorf("session-id: got %q, want %q", sessionID, tt.cacheKey)
				}
				if threadID != tt.cacheKey {
					t.Errorf("thread-id: got %q, want %q", threadID, tt.cacheKey)
				}
				if originator != "codex_cli_rs" {
					t.Errorf("originator: got %q, want codex_cli_rs", originator)
				}
			} else {
				if sessionID != "" {
					t.Errorf("session-id: got %q, want empty", sessionID)
				}
				if threadID != "" {
					t.Errorf("thread-id: got %q, want empty", threadID)
				}
				if originator != "" {
					t.Errorf("originator: got %q, want empty", originator)
				}
			}
		})
	}
}

func TestCodexWSDialCarriesCacheAffinityHeaders(t *testing.T) {
	var mu sync.Mutex
	var capturedSessionID, capturedThreadID, capturedOriginator string
	var dialCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		dialCount++
		capturedSessionID = r.Header.Get("session-id")
		capturedThreadID = r.Header.Get("thread-id")
		capturedOriginator = r.Header.Get("originator")
		mu.Unlock()

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			typ, data, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var frame map[string]any
			if err := json.Unmarshal(data, &frame); err != nil {
				return
			}

			response := map[string]any{
				"type":     "response.completed",
				"response": map[string]any{"output": []any{}, "status": "success"},
			}
			responseJSON, _ := json.Marshal(response)
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			_ = conn.Write(ctx, websocket.MessageText, responseJSON)
			cancel()
		}
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[4:]

	cfg := ClientConfig{
		BaseURL:    "http://localhost:8080",
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: &http.Client{},
	}

	provider, err := NewCodexResponsesWS(cfg)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	wsProvider := provider.(*codexWSProvider)
	wsProvider.wsURL = wsURL

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = wsProvider.ChatCompletion(ctx, ChatRequest{
		Model:          "test-model",
		Messages:       []Message{{Role: MessageRoleUser, Content: "first call"}},
		PromptCacheKey: "cache-key-one",
	})
	cancel()
	if err != nil {
		t.Fatalf("first ChatCompletion: %v", err)
	}

	mu.Lock()
	firstSessionID, firstThreadID, firstOriginator, firstDialCount := capturedSessionID, capturedThreadID, capturedOriginator, dialCount
	mu.Unlock()

	if firstSessionID != "cache-key-one" {
		t.Errorf("session-id: got %q, want cache-key-one", firstSessionID)
	}
	if firstThreadID != "cache-key-one" {
		t.Errorf("thread-id: got %q, want cache-key-one", firstThreadID)
	}
	if firstOriginator != "codex_cli_rs" {
		t.Errorf("originator: got %q, want codex_cli_rs", firstOriginator)
	}
	if firstDialCount != 1 {
		t.Fatalf("dial count after first call: got %d, want 1", firstDialCount)
	}

	if wsProvider.dialCacheKey != "cache-key-one" {
		t.Errorf("dialCacheKey: got %q, want cache-key-one", wsProvider.dialCacheKey)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	_, err = wsProvider.ChatCompletion(ctx, ChatRequest{
		Model:          "test-model",
		Messages:       []Message{{Role: MessageRoleUser, Content: "second call"}},
		PromptCacheKey: "cache-key-two",
	})
	cancel()
	if err != nil {
		t.Fatalf("second ChatCompletion: %v", err)
	}

	mu.Lock()
	secondDialCount := dialCount
	mu.Unlock()

	if secondDialCount != 1 {
		t.Errorf("dial count after second call: got %d, want 1 (connection should be reused, not redialed)", secondDialCount)
	}

	if wsProvider.dialCacheKey != "cache-key-one" {
		t.Errorf("dialCacheKey after second call: got %q, want cache-key-one (first-dial-wins)", wsProvider.dialCacheKey)
	}
}
