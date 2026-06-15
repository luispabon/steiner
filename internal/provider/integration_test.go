package provider

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIntegrationChatCompletionSendsCorrectRequest(t *testing.T) {
	t.Run("sends authorization header when API key is set", func(t *testing.T) {
		var authHeader string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader = r.Header.Get("Authorization")
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()

		provider, err := NewOpenAICompat(OpenAICompatConfig{
			BaseURL:   server.URL + "/v1",
			APIKey:    "sk-test-key",
			Model:     "gpt-4",
			Scheduler: mustTestScheduler(t, 1),
		})
		if err != nil {
			t.Fatalf("NewOpenAICompat() error = %v", err)
		}

		_, err = provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("ChatCompletion() error = %v", err)
		}
		if want := "Bearer sk-test-key"; authHeader != want {
			t.Fatalf("Authorization = %q, want %q", authHeader, want)
		}
	})

	t.Run("omits authorization header when API key is empty", func(t *testing.T) {
		var authHeader string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader = r.Header.Get("Authorization")
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()

		provider := mustTestOpenAICompat(t, server.URL)

		_, err := provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("ChatCompletion() error = %v", err)
		}
		if authHeader != "" {
			t.Fatalf("Authorization = %q, want empty", authHeader)
		}
	})

	t.Run("sends correct JSON body", func(t *testing.T) {
		var gotBody map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			if err := json.Unmarshal(body, &gotBody); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()

		provider, err := NewOpenAICompat(OpenAICompatConfig{
			BaseURL:   server.URL + "/v1",
			Model:     "gpt-4",
			Scheduler: mustTestScheduler(t, 1),
		})
		if err != nil {
			t.Fatalf("NewOpenAICompat() error = %v", err)
		}

		_, err = provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{
				{Role: MessageRoleUser, Content: "hello"},
				{Role: MessageRoleAssistant, Content: "world"},
			},
		})
		if err != nil {
			t.Fatalf("ChatCompletion() error = %v", err)
		}

		if got, want := gotBody["model"], "gpt-4"; got != want {
			t.Fatalf("model = %v, want %v", got, want)
		}
		msgs, ok := gotBody["messages"].([]any)
		if !ok {
			t.Fatal("messages should be an array")
		}
		if len(msgs) != 2 {
			t.Fatalf("got %d messages, want 2", len(msgs))
		}
		if _, ok := gotBody["stream"]; ok {
			t.Fatal("stream should not be present for non-stream request")
		}
	})

	t.Run("sends Content-Type header", func(t *testing.T) {
		var contentType string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			contentType = r.Header.Get("Content-Type")
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()

		provider := mustTestOpenAICompat(t, server.URL)

		_, err := provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("ChatCompletion() error = %v", err)
		}
		if want := "application/json"; contentType != want {
			t.Fatalf("Content-Type = %q, want %q", contentType, want)
		}
	})

	t.Run("anthropic native uses x-api-key instead of authorization", func(t *testing.T) {
		var (
			authHeader         string
			apiKeyHeader       string
			anthropicVersion   string
			requestPath        string
			requestContentType string
		)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader = r.Header.Get("Authorization")
			apiKeyHeader = r.Header.Get("x-api-key")
			anthropicVersion = r.Header.Get("anthropic-version")
			requestPath = r.URL.Path
			requestContentType = r.Header.Get("Content-Type")
			_, _ = fmt.Fprint(w, `{
				"role":"assistant",
				"content":[{"type":"text","text":"ok"}],
				"stop_reason":"end_turn",
				"usage":{"input_tokens":2,"output_tokens":1}
			}`)
		}))
		defer server.Close()

		provider, err := NewAnthropic(OpenAICompatConfig{
			BaseURL:   server.URL + "/v1",
			APIKey:    "sk-ant-test",
			Model:     "claude-3-7-sonnet",
			Scheduler: mustTestScheduler(t, 1),
		})
		if err != nil {
			t.Fatalf("NewAnthropic() error = %v", err)
		}

		resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("ChatCompletion() error = %v", err)
		}
		if got, want := apiKeyHeader, "sk-ant-test"; got != want {
			t.Fatalf("x-api-key = %q, want %q", got, want)
		}
		if authHeader != "" {
			t.Fatalf("Authorization = %q, want empty", authHeader)
		}
		if got, want := anthropicVersion, "2023-06-01"; got != want {
			t.Fatalf("anthropic-version = %q, want %q", got, want)
		}
		if got, want := requestPath, "/v1/messages"; got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
		if got, want := requestContentType, "application/json"; got != want {
			t.Fatalf("Content-Type = %q, want %q", got, want)
		}
		if got, want := resp.Message.Content, "ok"; got != want {
			t.Fatalf("content = %q, want %q", got, want)
		}
		if got, want := resp.FinishReason, "stop"; got != want {
			t.Fatalf("finish_reason = %q, want %q", got, want)
		}
		if resp.Usage == nil {
			t.Fatal("usage = nil, want non-nil")
		}
		if got, want := resp.Usage.TotalTokens, 3; got != want {
			t.Fatalf("total_tokens = %d, want %d", got, want)
		}
	})
}

func TestIntegrationChatCompletionParsesResponseCorrectly(t *testing.T) {
	t.Run("parses full response with content and usage", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `{
					"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
					"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
				}`)
		}))
		defer server.Close()

		provider := mustTestOpenAICompat(t, server.URL)

		resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("ChatCompletion() error = %v", err)
		}
		if got, want := resp.Message.Role, MessageRoleAssistant; got != want {
			t.Fatalf("role = %s, want %s", got, want)
		}
		if got, want := resp.Message.Content, "hello"; got != want {
			t.Fatalf("content = %q, want %q", got, want)
		}
		if got, want := resp.FinishReason, "stop"; got != want {
			t.Fatalf("finish_reason = %q, want %q", got, want)
		}
		if resp.Usage == nil {
			t.Fatal("usage = nil, want non-nil")
		}
		if got, want := resp.Usage.TotalTokens, 15; got != want {
			t.Fatalf("total_tokens = %d, want %d", got, want)
		}
	})

	t.Run("parses minimal response with empty content", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant"},"finish_reason":"stop"}]}`)
		}))
		defer server.Close()

		provider := mustTestOpenAICompat(t, server.URL)

		resp, err := provider.ChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("ChatCompletion() error = %v", err)
		}
		if got, want := resp.Message.Role, MessageRoleAssistant; got != want {
			t.Fatalf("role = %s, want %s", got, want)
		}
	})
}

func TestIntegrationChatCompletionHTTPError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "400 Bad Request", statusCode: http.StatusBadRequest, body: `{"error":"bad request"}`},
		{name: "401 Unauthorized", statusCode: http.StatusUnauthorized, body: `{"error":"invalid key"}`},
		{name: "429 Rate Limited", statusCode: http.StatusTooManyRequests, body: `{"error":"rate limited"}`},
		{name: "500 Internal Server Error", statusCode: http.StatusInternalServerError, body: `{"error":"server error"}`},
		{name: "502 Bad Gateway", statusCode: http.StatusBadGateway, body: `bad upstream`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			provider := mustTestOpenAICompat(t, server.URL)

			_, err := provider.ChatCompletion(context.Background(), ChatRequest{
				Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
			})
			if err == nil {
				t.Fatalf("expected error for HTTP %d", tt.statusCode)
			}
		})
	}
}

func TestIntegrationStreamChatCompletionSSEParsing(t *testing.T) {
	t.Run("parses single content chunk", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			_, _ = fmt.Fprint(w, "data: [DONE]\n")
		}))
		defer server.Close()

		provider := mustTestOpenAICompat(t, server.URL)

		ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("StreamChatCompletion() error = %v", err)
		}

		var contentChunks int
		var done bool
		for chunk := range ch {
			if chunk.Done {
				done = true
				if chunk.Usage == nil {
					t.Fatal("expected usage in done chunk")
				}
				if chunk.Usage.TotalTokens != 2 {
					t.Fatalf("total_tokens = %d, want 2", chunk.Usage.TotalTokens)
				}
				continue
			}
			if chunk.Delta.Content != "" {
				contentChunks++
			}
		}

		if contentChunks != 1 {
			t.Fatalf("got %d content chunks, want 1", contentChunks)
		}
		if !done {
			t.Fatal("stream did not emit done chunk")
		}
	})

	t.Run("parses multiple content chunks with usage", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"World\"}}]}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{}}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			_, _ = fmt.Fprint(w, "data: [DONE]\n")
		}))
		defer server.Close()

		provider := mustTestOpenAICompat(t, server.URL)

		ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
			Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
		})
		if err != nil {
			t.Fatalf("StreamChatCompletion() error = %v", err)
		}

		var contents []string
		var done *ChatChunk
		for chunk := range ch {
			if chunk.Done {
				c := chunk
				done = &c
				continue
			}
			if chunk.Delta.Content != "" {
				contents = append(contents, chunk.Delta.Content)
			}
		}

		if len(contents) != 2 {
			t.Fatalf("got %d content chunks, want 2", len(contents))
		}
		if contents[0] != "Hello " {
			t.Fatalf("content[0] = %q, want %q", contents[0], "Hello ")
		}
		if contents[1] != "World" {
			t.Fatalf("content[1] = %q, want %q", contents[1], "World")
		}
		if done == nil {
			t.Fatal("expected done chunk")
		}
		if done.FinishReason != "stop" {
			t.Fatalf("finish_reason = %q, want %q", done.FinishReason, "stop")
		}
		if done.Usage == nil {
			t.Fatal("expected usage in done chunk")
		}
		if done.Usage.TotalTokens != 7 {
			t.Fatalf("total_tokens = %d, want 7", done.Usage.TotalTokens)
		}
	})
}

func TestIntegrationStreamChatCompletionSendsCorrectRequest(t *testing.T) {
	var contentType, accept, authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		accept = r.Header.Get("Accept")
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n")
	}))
	defer server.Close()

	provider, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:   server.URL + "/v1",
		APIKey:    "sk-stream-key",
		Model:     "gpt-4",
		Scheduler: mustTestScheduler(t, 1),
	})
	if err != nil {
		t.Fatalf("NewOpenAICompat() error = %v", err)
	}

	ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
		Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion() error = %v", err)
	}
	for range ch {
	}

	if contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", contentType, "application/json")
	}
	if accept != "text/event-stream" {
		t.Fatalf("Accept = %q, want %q", accept, "text/event-stream")
	}
	if authHeader != "Bearer sk-stream-key" {
		t.Fatalf("Authorization = %q, want %q", authHeader, "Bearer sk-stream-key")
	}
}

func TestIntegrationStreamChatCompletionHTTPError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "400 error", statusCode: http.StatusBadRequest, body: `{"error":"bad"}`},
		{name: "500 error", statusCode: http.StatusInternalServerError, body: `internal error`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			provider := mustTestOpenAICompat(t, server.URL)

			ch, err := provider.StreamChatCompletion(context.Background(), ChatRequest{
				Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
			})
			if err != nil {
				t.Fatalf("StreamChatCompletion() error = %v, want channel", err)
			}

			chunk, ok := <-ch
			if !ok {
				t.Fatal("stream closed before error chunk")
			}
			if !chunk.Done {
				t.Fatal("error chunk Done = false, want true")
			}
			if chunk.Error == "" {
				t.Fatal("error chunk Error = empty, want error message")
			}
			if _, ok := <-ch; ok {
				t.Fatal("stream produced unexpected extra chunk after error")
			}
		})
	}
}

// TestIntegrationH2TimeoutDoesNotBreakTransport is a regression test for the
// bug where setting a provider timeout cloned the shared http.Transport, which
// dropped the unexported http2 wiring and produced
// "net/http: HTTP/1.x transport connection broken: malformed HTTP response"
// errors when the upstream negotiated h2. It runs the same chat request
// against an h2-capable server with and without Timeout set, and expects both
// to succeed without transport errors. The transport mirrors the shape of
// runtimeHTTPClient (plain *http.Transport, no http2.ConfigureTransport).
func TestIntegrationH2TimeoutDoesNotBreakTransport(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	// Mirror runtimeHTTPClient shape: plain *http.Transport, ResponseHeaderTimeout
	// set, no explicit http2.ConfigureTransport. The previous bug cloned this
	// transport and dropped the h2 wiring; the test now ensures we do not
	// touch the Transport at all when applying a provider timeout.
	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	httpClient := &http.Client{Transport: transport}

	cases := []struct {
		name    string
		timeout time.Duration
	}{
		{"no timeout", 0},
		{"with timeout", 30 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewOpenAICompat(OpenAICompatConfig{
				BaseURL:    srv.URL + "/v1",
				APIKey:     "sk-test-key",
				Model:      "gpt-4",
				Timeout:    tc.timeout,
				HTTPClient: httpClient,
				Scheduler:  mustTestScheduler(t, 1),
			})
			if err != nil {
				t.Fatalf("NewOpenAICompat() error = %v", err)
			}

			_, err = provider.ChatCompletion(context.Background(), ChatRequest{
				Messages: []Message{{Role: MessageRoleUser, Content: "hi"}},
			})
			if err != nil {
				t.Fatalf("ChatCompletion with %s failed: %v", tc.name, err)
			}
		})
	}
}
