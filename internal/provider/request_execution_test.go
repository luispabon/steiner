package provider

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"
)

func TestBuildJSONPostRequest(t *testing.T) {
	body := []byte(`{"model":"gpt-4"}`)
	target := "http://localhost:11434/v1/chat/completions"

	req, err := buildJSONPostRequest(t.Context(), target, body, false, "test-key", nil)
	if err != nil {
		t.Fatalf("buildJSONPostRequest() error = %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want %q", req.Method, http.MethodPost)
	}
	if req.URL.String() != target {
		t.Fatalf("URL = %q, want %q", req.URL.String(), target)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want %q", req.Header.Get("Content-Type"), "application/json")
	}
	if req.Header.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want %q", req.Header.Get("Authorization"), "Bearer test-key")
	}
	if req.Header.Get("Accept") != "" {
		t.Fatal("Accept header should not be present for non-stream")
	}

	req, err = buildJSONPostRequest(t.Context(), target, body, true, "test-key", nil)
	if err != nil {
		t.Fatalf("buildJSONPostRequest() error = %v", err)
	}
	if req.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("Accept = %q, want %q", req.Header.Get("Accept"), "text/event-stream")
	}
}

func TestBuildJSONPostRequest_NoAuth(t *testing.T) {
	req, err := buildJSONPostRequest(t.Context(), "http://localhost:11434/v1/chat/completions", []byte(`{}`), false, "", nil)
	if err != nil {
		t.Fatalf("buildJSONPostRequest() error = %v", err)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("Authorization header should not be present when apiKey is empty")
	}
}

func TestShouldDisableRemoteStorage(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "openai platform", baseURL: "https://api.openai.com/v1", want: true},
		{name: "openai subdomain", baseURL: "https://eu.api.openai.com/v1", want: true},
		{name: "chatgpt codex backend", baseURL: "https://chatgpt.com/backend-api/codex", want: true},
		{name: "local compat host", baseURL: "http://localhost:11434/v1"},
		{name: "third-party host", baseURL: "https://openrouter.ai/api/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := url.Parse(tt.baseURL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			if got := shouldDisableRemoteStorage(parsed); got != tt.want {
				t.Fatalf("shouldDisableRemoteStorage(%q) = %v, want %v", tt.baseURL, got, tt.want)
			}
		})
	}

	if shouldDisableRemoteStorage(nil) {
		t.Fatal("shouldDisableRemoteStorage(nil) = true, want false")
	}
}

func TestReadErrorResponse(t *testing.T) {
	tests := []struct {
		name   string
		status string
		body   string
		want   string
	}{
		{
			name:   "empty body",
			status: "400 Bad Request",
			body:   "",
			want:   "chat completions request failed: 400 Bad Request",
		},
		{
			name:   "with body",
			status: "400 Bad Request",
			body:   `{"error":"bad"}`,
			want:   `chat completions request failed: 400 Bad Request: {"error":"bad"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{}
			resp := &http.Response{
				Status:     tt.status,
				StatusCode: 400,
				Body:       io.NopCloser(bytes.NewReader([]byte(tt.body))),
			}
			err := c.readErrorResponse(resp)
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.want {
				t.Fatalf("got %q, want %q", err.Error(), tt.want)
			}
		})
	}
}
