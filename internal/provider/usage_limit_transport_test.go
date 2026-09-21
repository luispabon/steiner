package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func newUsageLimitClient(t *testing.T, baseURL, providerType string) *Client {
	t.Helper()
	c, err := NewOpenAICompat(ClientConfig{
		BaseURL:      baseURL + "/v1",
		Model:        "test-model",
		ProviderType: providerType,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompat() error = %v", err)
	}
	c.retry = RetryConfig{Enabled: true, MaxAttempts: 5, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond, RetryAfterMax: time.Second}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	c.jitter = func(cap time.Duration) time.Duration { return cap }
	return c
}

func status429Server(body string, count *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(count, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(body))
	}))
}

func TestUsageLimitNotRetriedOverHTTP(t *testing.T) {
	codexBody := usageFixture(t, "codex_http_usage_limit.json")
	tests := []struct {
		name         string
		providerType string
		body         string
		wantKind     UsageLimitKind
	}{
		{"codex usage limit", "openai", codexBody, UsageLimitKindUsage},
		{"litellm budget", "litellm", usageFixture(t, "litellm_budget_exceeded.json"), UsageLimitKindBudget},
	}
	for _, tt := range tests {
		t.Run(tt.name+" non-stream", func(t *testing.T) {
			var n int32
			srv := status429Server(tt.body, &n)
			defer srv.Close()
			c := newUsageLimitClient(t, srv.URL, tt.providerType)
			_, err := c.ChatCompletion(context.Background(), ChatRequest{Messages: []Message{{Role: MessageRoleUser, Content: "hi"}}})
			ule, ok := AsUsageLimit(err)
			if !ok {
				t.Fatalf("error = %v, want *UsageLimitError", err)
			}
			if got := atomic.LoadInt32(&n); got != 1 {
				t.Fatalf("requests = %d, want 1", got)
			}
			if ule.Kind != tt.wantKind {
				t.Fatalf("Kind = %q, want %q", ule.Kind, tt.wantKind)
			}
			if tt.wantKind == UsageLimitKindUsage && !ule.ResetsAt.Equal(time.Unix(1789676095, 0)) {
				t.Fatalf("ResetsAt = %v", ule.ResetsAt)
			}
			if _, retry := RetryableProviderError(err); retry {
				t.Fatal("RetryableProviderError = true, want false")
			}
		})
		t.Run(tt.name+" stream", func(t *testing.T) {
			var n int32
			srv := status429Server(tt.body, &n)
			defer srv.Close()
			c := newUsageLimitClient(t, srv.URL, tt.providerType)
			ch, err := c.StreamChatCompletion(context.Background(), ChatRequest{Messages: []Message{{Role: MessageRoleUser, Content: "hi"}}})
			if err != nil {
				t.Fatalf("StreamChatCompletion() error = %v", err)
			}
			var last ChatChunk
			for chunk := range ch {
				last = chunk
			}
			if _, ok := AsUsageLimit(last.OriginalError); !ok {
				t.Fatalf("final OriginalError = %v, want *UsageLimitError", last.OriginalError)
			}
			if got := atomic.LoadInt32(&n); got != 1 {
				t.Fatalf("requests = %d, want 1", got)
			}
		})
	}
}

func TestPlainRateLimitStillRetried(t *testing.T) {
	var n int32
	srv := status429Server(`{"error":{"type":"rate_limit_error","message":"slow down"}}`, &n)
	defer srv.Close()
	c := newUsageLimitClient(t, srv.URL, "openai")
	_, err := c.ChatCompletion(context.Background(), ChatRequest{Messages: []Message{{Role: MessageRoleUser, Content: "hi"}}})
	if err == nil {
		t.Fatal("error = nil, want failure")
	}
	if _, ok := AsUsageLimit(err); ok {
		t.Fatal("plain rate limit classified as usage limit")
	}
	if got := atomic.LoadInt32(&n); got != 5 {
		t.Fatalf("requests = %d, want 5", got)
	}
	if _, retry := RetryableProviderError(err); !retry {
		t.Fatal("RetryableProviderError = false for plain 429, want true")
	}
}

func TestResponsesStreamErrorFrames(t *testing.T) {
	frame := usageFixture(t, "codex_ws_usage_limit.json")
	noStatus := strings.Replace(frame, `"status":429,`, "", 1)
	retryable := strings.Replace(frame, `"type":"usage_limit_reached"`, `"type":"usage_limit_reached","code":"websocket_connection_limit_reached"`, 1)
	tests := []struct {
		name     string
		frame    string
		wantHTTP bool
	}{
		{"status-bearing frame becomes HTTPError", frame, true},
		{"frame without status stays plain", noStatus, false},
		{"websocket-retryable code stays plain", retryable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := processResponsesStreamEvent(&responsesStreamState{}, tt.frame, func(ChatChunk) error { return nil })
			if err == nil {
				t.Fatal("error = nil")
			}
			httpErr := asHTTPError(err)
			if (httpErr != nil) != tt.wantHTTP {
				t.Fatalf("HTTPError = %v, want present=%v (err %v)", httpErr, tt.wantHTTP, err)
			}
			if !tt.wantHTTP {
				if !strings.HasPrefix(err.Error(), "responses stream error: ") {
					t.Fatalf("err = %q, want plain responses stream error", err)
				}
				return
			}
			if httpErr.StatusCode != 429 {
				t.Fatalf("status = %d", httpErr.StatusCode)
			}
			if got := httpErr.Header.Get("X-Codex-Primary-Used-Percent"); got != "100.0" {
				t.Fatalf("header = %q, want 100.0", got)
			}
			if _, ok := wrapUsageLimit("codex", err, time.Unix(1738880000, 0)).(*UsageLimitError); !ok {
				t.Fatal("frame error not classified as usage limit")
			}
		})
	}
}

func TestRetryableProviderErrorUsageLimit(t *testing.T) {
	plain := &HTTPError{StatusCode: 429, Body: `{"error":{"type":"rate_limit_error"}}`}
	if _, ok := RetryableProviderError(plain); !ok {
		t.Fatal("plain 429 should be retryable")
	}
	ule := wrapUsageLimit("codex", &HTTPError{StatusCode: 429, Body: usageFixture(t, "codex_http_usage_limit.json")}, time.Unix(1789670000, 0))
	if d, ok := RetryableProviderError(ule); ok || d != 0 {
		t.Fatalf("RetryableProviderError(usage limit) = %v, %v; want 0, false", d, ok)
	}
}

func TestCodexWSUsageLimitSingleSendNoReconnect(t *testing.T) {
	var conns, reads int32
	frame := usageFixture(t, "codex_ws_usage_limit.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&conns, 1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
			atomic.AddInt32(&reads, 1)
			if err := conn.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	provider, err := NewCodexResponsesWS(ClientConfig{
		BaseURL: "http://localhost:8080", APIKey: "k", Model: "test-model", ProviderType: "codex", HTTPClient: &http.Client{},
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	ws := provider.(*codexWSProvider)
	ws.wsURL = "ws" + server.URL[4:]
	defer ws.closeConn()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = ws.ChatCompletion(ctx, ChatRequest{Model: "test-model", Messages: []Message{{Role: MessageRoleUser, Content: "hi"}}})
	if _, ok := AsUsageLimit(err); !ok {
		t.Fatalf("error = %v, want *UsageLimitError", err)
	}
	if got := atomic.LoadInt32(&reads); got != 1 {
		t.Fatalf("sends = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&conns); got != 1 {
		t.Fatalf("connections = %d, want 1", got)
	}
	ws.mu.Lock()
	open := ws.conn != nil
	ws.mu.Unlock()
	if !open {
		t.Fatal("connection was closed on usage limit, want it kept open")
	}
}
