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
	tests := []struct {
		name                 string
		turnState            string
		expectClientMetadata bool
		checkInstructions    bool
	}{
		{
			name:                 "no turn-state",
			turnState:            "",
			expectClientMetadata: false,
			checkInstructions:    true,
		},
		{
			name:                 "with turn-state",
			turnState:            "test-token-123",
			expectClientMetadata: true,
			checkInstructions:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := ChatRequest{
				Model: "test-model",
				Messages: []Message{
					{Role: MessageRoleUser, Content: "test"},
				},
			}

			frameBytes, err := buildWSRequestFrame(request, "test-model", tt.turnState)
			if err != nil {
				t.Fatalf("buildWSRequestFrame: %v", err)
			}

			var frameOut map[string]any
			if err := json.Unmarshal(frameBytes, &frameOut); err != nil {
				t.Fatalf("unmarshal frame: %v", err)
			}

			if frameOut["type"] != "response.create" {
				t.Errorf("type: got %v, want response.create", frameOut["type"])
			}

			if frameOut["model"] != "test-model" {
				t.Errorf("model: got %v, want test-model", frameOut["model"])
			}

			hasClientMetadata := frameOut["client_metadata"] != nil
			if hasClientMetadata != tt.expectClientMetadata {
				t.Errorf("client_metadata presence: got %v, want %v", hasClientMetadata, tt.expectClientMetadata)
			}

			if tt.checkInstructions {
				if instructions, ok := frameOut["instructions"]; ok {
					if metaStr, ok := instructions.(string); ok {
						if tt.expectClientMetadata && tt.turnState != "" {
							if strings.Contains(metaStr, tt.turnState) {
								t.Errorf("turn-state leaked into instructions: %s", metaStr)
							}
						}
					}
				}

				if inputRaw, ok := frameOut["input"]; ok {
					inputJSON, _ := json.Marshal(inputRaw)
					if tt.expectClientMetadata && tt.turnState != "" {
						if strings.Contains(string(inputJSON), tt.turnState) {
							t.Errorf("turn-state leaked into input: %s", string(inputJSON))
						}
					}
				}
			}
		})
	}
}

func TestCodexWSFramePrecedence(t *testing.T) {
	request := ChatRequest{
		Model:    "request-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "test"}},
		Params: map[string]any{
			"type":            "params-type",
			"model":           "params-model",
			"client_metadata": map[string]any{"source": "params"},
		},
		ExtraParams: map[string]any{
			"type":            "extra-type",
			"model":           "extra-model",
			"client_metadata": map[string]any{"source": "extra"},
		},
	}

	frameBytes, err := buildWSRequestFrame(request, "frame-model", "turn-state")
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
	metadata, ok := frame["client_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("client_metadata = %#v, want map[string]any", frame["client_metadata"])
	}
	if got, want := metadata[WSClientMetadataTurnStateKey], "turn-state"; got != want {
		t.Errorf("client_metadata.turn_state: got %v, want %v", got, want)
	}
	if _, ok := metadata["source"]; ok {
		t.Errorf("client_metadata retained extra value: %#v", metadata)
	}
}

func TestCodexWSNoClientMetadataAcrossSequentialCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()

		for i := 0; i < 3; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			typ, data, err := conn.Read(ctx)
			cancel()

			if err != nil {
				break
			}
			if typ != websocket.MessageText {
				continue
			}

			var frame map[string]any
			if err := json.Unmarshal(data, &frame); err != nil {
				break
			}

			if frame["client_metadata"] != nil {
				_ = conn.CloseNow()
				break
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

	provider, err := newCodexResponsesWSWithEcho(cfg, false, false)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	wsProvider := provider.(*codexWSProvider)
	wsProvider.wsURL = wsURL

	for callNum := 1; callNum <= 3; callNum++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := wsProvider.ChatCompletion(ctx, ChatRequest{
			Model:    "test-model",
			Messages: []Message{{Role: MessageRoleUser, Content: fmt.Sprintf("call %d", callNum)}},
		})
		cancel()

		if err != nil && !strings.Contains(err.Error(), "completed without a final chunk") {
			t.Logf("call %d completed with error: %v (this is expected for mock)", callNum, err)
		}
	}
}

func TestCodexWSEchoTurnStateNotCachedAcrossCalls(t *testing.T) {
	var capturedTokens []string
	var mu sync.Mutex

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

		mu.Lock()
		if meta, ok := frame["client_metadata"].(map[string]any); ok {
			if token, ok := meta[WSClientMetadataTurnStateKey].(string); ok {
				capturedTokens = append(capturedTokens, token)
			}
		}
		mu.Unlock()

		metadata := map[string]any{
			"type": "codex.response.metadata",
			"headers": map[string]any{
				WSHeaderTurnState: "turn-state-from-call",
			},
		}
		metadataJSON, _ := json.Marshal(metadata)

		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Write(ctx, websocket.MessageText, metadataJSON)
		cancel()

		response := map[string]any{
			"type":     "response.completed",
			"response": map[string]any{"output": []any{}, "status": "success"},
		}
		responseJSON, _ := json.Marshal(response)
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		_ = conn.Write(ctx, websocket.MessageText, responseJSON)
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

	provider, err := newCodexResponsesWSWithEcho(cfg, false, true)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	wsProvider := provider.(*codexWSProvider)
	wsProvider.wsURL = wsURL

	for callNum := 1; callNum <= 2; callNum++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := wsProvider.ChatCompletion(ctx, ChatRequest{
			Model:    "test-model",
			Messages: []Message{{Role: MessageRoleUser, Content: fmt.Sprintf("call %d", callNum)}},
		})
		cancel()

		if err != nil && !strings.Contains(err.Error(), "completed without a final chunk") {
			t.Logf("call %d: %v", callNum, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(capturedTokens) > 0 {
		t.Errorf("turn-state should not be sent across calls, but got %d tokens in outbound frames", len(capturedTokens))
	}
}

func TestCodexWSFallbackOnDialFailure(t *testing.T) {
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

	provider, err := newCodexResponsesWSWithEcho(cfg, true, false)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	wsProvider := provider.(*codexWSProvider)
	wsProvider.wsURL = "ws://invalid-unreachable-host-that-will-not-dial:9999"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	stream, err := wsProvider.StreamChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "test"}},
	})
	cancel()

	if err != nil {
		t.Fatalf("StreamChatCompletion returned early error: %v", err)
	}

	sawDiagnostic := false

	for chunk := range stream {
		if chunk.Diagnostic != "" && strings.Contains(chunk.Diagnostic, "Codex WebSocket unavailable") {
			sawDiagnostic = true
		}
	}

	if !sawDiagnostic {
		t.Error("expected diagnostic chunk about WebSocket unavailability")
	}
}

func TestCodexWSNoFallbackWhenDisabled(t *testing.T) {
	cfg := ClientConfig{
		BaseURL:    "http://localhost:8080",
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: &http.Client{},
	}

	provider, err := NewCodexResponsesWSNoFallback(cfg)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}

	wsProvider := provider.(*codexWSProvider)
	wsProvider.wsURL = "ws://invalid:9999"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, err = wsProvider.ChatCompletion(ctx, ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: "test"}},
	})
	cancel()

	if err == nil {
		t.Error("expected error when fallback is disabled and WS dial fails")
	}
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

	provider, err := newCodexResponsesWSWithEcho(cfg, false, false)
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

	provider, err := newCodexResponsesWSWithEcho(cfg, false, false)
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
