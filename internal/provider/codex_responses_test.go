package provider

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestBuildResponsesHTTPRequestSetsAffinityHeaders(t *testing.T) {
	tests := []struct {
		name           string
		promptCacheKey string
		wantHeaders    map[string]string
	}{
		{
			name:           "headers set when key is non-empty",
			promptCacheKey: "session-123",
			wantHeaders: map[string]string{
				"session-id": "session-123",
				"thread-id":  "session-123",
				"originator": "codex_cli_rs",
			},
		},
		{
			name:           "headers not set when key is empty",
			promptCacheKey: "",
			wantHeaders:    map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewCodexResponses(OpenAICompatConfig{
				BaseURL:    "https://api.openai.com/v1",
				APIKey:     "test-key",
				Model:      "gpt-4",
				Scheduler:  &Scheduler{},
				HTTPClient: &http.Client{},
			})
			if err != nil {
				t.Fatalf("NewCodexResponses() error = %v", err)
			}

			req, err := c.buildResponsesHTTPRequest(context.Background(), ChatRequest{
				PromptCacheKey: tc.promptCacheKey,
			}, []byte("{}"), false)
			if err != nil {
				t.Fatalf("buildResponsesHTTPRequest() error = %v", err)
			}

			for header, expectedValue := range tc.wantHeaders {
				if got := req.Header.Get(header); got != expectedValue {
					t.Errorf("%s header = %q, want %q", header, got, expectedValue)
				}
			}

			// Verify headers are not set when empty
			if tc.promptCacheKey == "" {
				for header := range tc.wantHeaders {
					if got := req.Header.Get(header); got != "" {
						t.Errorf("%s header should be empty but got %q", header, got)
					}
				}
			}
		})
	}
}

func TestOpenAIBuildHTTPRequestIgnoresRequest(t *testing.T) {
	c, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:    "https://api.openai.com/v1",
		APIKey:     "test-key",
		Model:      "gpt-4",
		Scheduler:  &Scheduler{},
		HTTPClient: &http.Client{},
	})
	if err != nil {
		t.Fatalf("NewOpenAICompat() error = %v", err)
	}

	req, err := c.buildHTTPRequest(context.Background(), ChatRequest{
		PromptCacheKey: "ignored-key",
	}, []byte("{}"), false)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
	}

	// Verify that affinity headers are NOT set for OpenAI
	if got := req.Header.Get("session-id"); got != "" {
		t.Errorf("session-id header should be empty for OpenAI but got %q", got)
	}
	if got := req.Header.Get("thread-id"); got != "" {
		t.Errorf("thread-id header should be empty for OpenAI but got %q", got)
	}
	if got := req.Header.Get("originator"); got != "" {
		t.Errorf("originator header should be empty for OpenAI but got %q", got)
	}
}

func TestCodexRateLimiter(t *testing.T) {
	tests := []struct {
		name           string
		minInterval    time.Duration
		requestCount   int
		wantMinElapsed time.Duration
	}{
		{
			name:           "rate limiter enforces interval",
			minInterval:    50 * time.Millisecond,
			requestCount:   3,
			wantMinElapsed: 100 * time.Millisecond,
		},
		{
			name:           "no delay when interval is zero",
			minInterval:    0,
			requestCount:   2,
			wantMinElapsed: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewCodexResponses(OpenAICompatConfig{
				BaseURL:            "https://api.openai.com/v1",
				APIKey:             "test-key",
				Model:              "gpt-4",
				Scheduler:          &Scheduler{},
				HTTPClient:         &http.Client{},
				MinRequestInterval: tc.minInterval,
			})
			if err != nil {
				t.Fatalf("NewCodexResponses() error = %v", err)
			}

			start := time.Now()
			for i := 0; i < tc.requestCount; i++ {
				err := c.rateLimit(context.Background())
				if err != nil {
					t.Fatalf("rateLimit() error = %v", err)
				}
			}
			elapsed := time.Since(start)

			if elapsed < tc.wantMinElapsed {
				t.Errorf("elapsed time = %v, want >= %v", elapsed, tc.wantMinElapsed)
			}
		})
	}
}

func TestCodexRateLimiterContextCancellation(t *testing.T) {
	c, err := NewCodexResponses(OpenAICompatConfig{
		BaseURL:            "https://api.openai.com/v1",
		APIKey:             "test-key",
		Model:              "gpt-4",
		Scheduler:          &Scheduler{},
		HTTPClient:         &http.Client{},
		MinRequestInterval: 1 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewCodexResponses() error = %v", err)
	}

	// Make a first call to set lastRequest to now, so the next call will need to wait
	err = c.rateLimit(context.Background())
	if err != nil {
		t.Fatalf("first rateLimit() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err = c.rateLimit(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("rateLimit() error = %v, want context.Canceled", err)
	}
}

func TestBuildResponsesHTTPRequestBuildsValidRequest(t *testing.T) {
	c, err := NewCodexResponses(OpenAICompatConfig{
		BaseURL:    "https://chatgpt.com/backend-api/codex",
		APIKey:     "test-api-key",
		Model:      "gpt-4",
		Scheduler:  &Scheduler{},
		HTTPClient: &http.Client{},
	})
	if err != nil {
		t.Fatalf("NewCodexResponses() error = %v", err)
	}

	body := []byte(`{"messages":[]}`)
	req, err := c.buildResponsesHTTPRequest(context.Background(), ChatRequest{
		PromptCacheKey: "test-session",
	}, body, true)
	if err != nil {
		t.Fatalf("buildResponsesHTTPRequest() error = %v", err)
	}

	// Verify basic request properties
	if req.Method != http.MethodPost {
		t.Errorf("request method = %q, want %q", req.Method, http.MethodPost)
	}

	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type header = %q, want application/json", req.Header.Get("Content-Type"))
	}

	if req.Header.Get("Accept") != "text/event-stream" {
		t.Errorf("Accept header = %q, want text/event-stream", req.Header.Get("Accept"))
	}

	if req.Header.Get("Authorization") != "Bearer test-api-key" {
		t.Errorf("Authorization header not set correctly")
	}

	// Verify affinity headers are set
	if req.Header.Get("session-id") != "test-session" {
		t.Errorf("session-id header not set correctly")
	}
	if req.Header.Get("thread-id") != "test-session" {
		t.Errorf("thread-id header not set correctly")
	}
	if req.Header.Get("originator") != "codex_cli_rs" {
		t.Errorf("originator header not set correctly")
	}

	// Verify body is included
	readBody := new(bytes.Buffer)
	if _, err := readBody.ReadFrom(req.Body); err != nil {
		t.Fatalf("readBody.ReadFrom() error = %v", err)
	}
	if readBody.String() != string(body) {
		t.Errorf("request body = %s, want %s", readBody.String(), string(body))
	}
}
