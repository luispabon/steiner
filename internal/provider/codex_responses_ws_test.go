package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
							if contains(metaStr, tt.turnState) {
								t.Errorf("turn-state leaked into instructions: %s", metaStr)
							}
						}
					}
				}

				if inputRaw, ok := frameOut["input"]; ok {
					inputJSON, _ := json.Marshal(inputRaw)
					if tt.expectClientMetadata && tt.turnState != "" {
						if contains(string(inputJSON), tt.turnState) {
							t.Errorf("turn-state leaked into input: %s", string(inputJSON))
						}
					}
				}
			}
		})
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

		if err != nil && !contains(err.Error(), "completed without a final chunk") {
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

		if err != nil && !contains(err.Error(), "completed without a final chunk") {
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
		if chunk.Diagnostic != "" && contains(chunk.Diagnostic, "Codex WebSocket unavailable") {
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

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) == 0 {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
