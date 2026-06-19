package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestOpenAICompatNewOpenAICompat_EmptyBaseURL(t *testing.T) {
	_, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:   "",
		Model:     "gpt-4",
		Scheduler: &Scheduler{},
	})
	if err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

func TestOpenAICompatNewOpenAICompat_InvalidURL(t *testing.T) {
	_, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:   "://invalid",
		Model:     "gpt-4",
		Scheduler: &Scheduler{},
	})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestOpenAICompatNewOpenAICompat_EmptyModel(t *testing.T) {
	_, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:   "http://localhost:11434/v1",
		Model:     "",
		Scheduler: &Scheduler{},
	})
	if err == nil {
		t.Fatal("expected error for empty model")
	}
}

func TestOpenAICompatNewOpenAICompat_NilScheduler(t *testing.T) {
	_, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL: "http://localhost:11434/v1",
		Model:   "gpt-4",
	})
	if err == nil {
		t.Fatal("expected error for nil scheduler")
	}
}

func TestOpenAICompatNewOpenAICompat_DefaultHTTPClient(t *testing.T) {
	p, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:   "http://localhost:11434/v1",
		Model:     "gpt-4",
		Scheduler: &Scheduler{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.httpClient != defaultHTTPClient {
		t.Fatal("expected default HTTP client when none provided")
	}
}

func TestOpenAICompatNewOpenAICompat_Success(t *testing.T) {
	customClient := &http.Client{}
	cfg := OpenAICompatConfig{
		BaseURL:    "http://localhost:11434/v1",
		APIKey:     "test-key",
		Headers:    map[string]string{"X-Test-Header": "configured"},
		Model:      "gpt-4",
		HTTPClient: customClient,
		Scheduler:  &Scheduler{},
	}
	p, err := NewOpenAICompat(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.baseURL.String() != "http://localhost:11434/v1" {
		t.Fatalf("baseURL = %q, want %q", p.baseURL.String(), "http://localhost:11434/v1")
	}
	if p.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want %q", p.apiKey, "test-key")
	}
	if p.model != "gpt-4" {
		t.Fatalf("model = %q, want %q", p.model, "gpt-4")
	}
	if p.httpClient != customClient {
		t.Fatal("expected custom HTTP client to be stored")
	}
	if got, want := p.headers["X-Test-Header"], "configured"; got != want {
		t.Fatalf("headers[X-Test-Header] = %q, want %q", got, want)
	}
}

func TestOpenAICompatNewOpenAICompat_TimeoutClonesHTTPClient(t *testing.T) {
	customClient := &http.Client{}
	p, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:    "http://localhost:11434/v1",
		Model:      "gpt-4",
		Timeout:    45 * time.Second,
		HTTPClient: customClient,
		Scheduler:  &Scheduler{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.httpClient == customClient {
		t.Fatal("expected timeout configuration to clone HTTP client")
	}
	if got, want := p.httpClient.Timeout, 45*time.Second; got != want {
		t.Fatalf("httpClient.Timeout = %v, want %v", got, want)
	}
	if customClient.Timeout != 0 {
		t.Fatalf("original client timeout = %v, want 0", customClient.Timeout)
	}
}

// TestOpenAICompatNewOpenAICompat_TimeoutDisablesResponseHeaderTimeout
// ensures the transport's ResponseHeaderTimeout does not shadow the
// provider's Client.Timeout. Without clearing it, a slow upstream that takes
// longer than the hardcoded 30s transport header timeout would still return
// `net/http: timeout awaiting response headers`, ignoring the user's
// configured provider timeout.
func TestOpenAICompatNewOpenAICompat_TimeoutDisablesResponseHeaderTimeout(t *testing.T) {
	originalTransport := &http.Transport{
		ResponseHeaderTimeout: 30 * time.Second,
	}
	customClient := &http.Client{Transport: originalTransport}
	p, err := NewOpenAICompat(OpenAICompatConfig{
		BaseURL:    "http://localhost:11434/v1",
		Model:      "gpt-4",
		Timeout:    45 * time.Second,
		HTTPClient: customClient,
		Scheduler:  &Scheduler{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.httpClient.Transport == originalTransport {
		t.Fatal("expected transport to be cloned, not shared with original client")
	}
	cloned, ok := p.httpClient.Transport.(*http.Transport)
	if !ok || cloned == nil {
		t.Fatalf("expected transport to be *http.Transport, got %T", p.httpClient.Transport)
	}
	if cloned.ResponseHeaderTimeout != 0 {
		t.Fatalf("cloned ResponseHeaderTimeout = %v, want 0 (Client.Timeout covers the whole request)", cloned.ResponseHeaderTimeout)
	}
	if originalTransport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("original transport ResponseHeaderTimeout = %v, want 30s (must not be mutated)", originalTransport.ResponseHeaderTimeout)
	}
}

func TestOpenAICompatSupportsUsageStats_NonNil(t *testing.T) {
	p := &OpenAICompat{}
	if !p.SupportsUsageStats() {
		t.Fatal("expected true for provider")
	}
}

func TestOpenAICompatSupportsUsageStats_Nil(t *testing.T) {
	var p *OpenAICompat
	if !p.SupportsUsageStats() {
		t.Fatal("expected true for nil provider")
	}
}

func TestOpenAICompatReadErrorResponse_EmptyBody(t *testing.T) {
	p := &OpenAICompat{}
	resp := &http.Response{
		Status:     "400 Bad Request",
		StatusCode: 400,
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
	err := p.readErrorResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}
	want := "chat completions request failed: 400 Bad Request"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestOpenAICompatReadErrorResponse_WithBody(t *testing.T) {
	p := &OpenAICompat{}
	resp := &http.Response{
		Status:     "400 Bad Request",
		StatusCode: 400,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"bad"}`))),
	}
	err := p.readErrorResponse(resp)
	if err == nil {
		t.Fatal("expected error")
	}
	want := `chat completions request failed: 400 Bad Request: {"error":"bad"}`
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

func TestOpenAICompatMarshalRequest_NoStream(t *testing.T) {
	p := &OpenAICompat{model: "gpt-4"}
	req := ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "hello"},
		},
	}
	data, err := p.marshalRequest(req, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got, want := m["model"], "gpt-4"; got != want {
		t.Fatalf("model = %v, want %v", got, want)
	}
	if _, ok := m["stream"]; ok {
		t.Fatal("stream key should not be present when false (omitempty)")
	}
	if _, ok := m["stream_options"]; ok {
		t.Fatal("stream_options should not be present when stream is false")
	}
}

func TestOpenAICompatMarshalRequest_WithStream(t *testing.T) {
	p := &OpenAICompat{model: "gpt-4"}
	req := ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "hello"},
		},
	}
	data, err := p.marshalRequest(req, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got, want := m["model"], "gpt-4"; got != want {
		t.Fatalf("model = %v, want %v", got, want)
	}
	if got, want := m["stream"], true; got != want {
		t.Fatalf("stream = %v, want %v", got, want)
	}
	so, ok := m["stream_options"]
	if !ok {
		t.Fatal("stream_options should be present when stream is true")
	}
	soMap, ok := so.(map[string]any)
	if !ok {
		t.Fatal("stream_options should be a map")
	}
	iu, ok := soMap["include_usage"]
	if !ok {
		t.Fatal("stream_options.include_usage should be present")
	}
	if iu != true {
		t.Fatalf("stream_options.include_usage = %v, want true", iu)
	}
}

func TestOpenAICompatMarshalRequest_MessagesAndExtraParams(t *testing.T) {
	p := &OpenAICompat{model: "gpt-4"}
	req := ChatRequest{
		Messages: []Message{
			{Role: MessageRoleUser, Content: "hello"},
			{Role: MessageRoleAssistant, Content: "hi there"},
		},
		ExtraParams: map[string]any{
			"temperature": 0.7,
			"top_p":       0.9,
		},
	}
	data, err := p.marshalRequest(req, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got, want := m["temperature"], 0.7; got != want {
		t.Fatalf("temperature = %v, want %v", got, want)
	}
	if got, want := m["top_p"], 0.9; got != want {
		t.Fatalf("top_p = %v, want %v", got, want)
	}
	msgs, ok := m["messages"].([]any)
	if !ok {
		t.Fatal("messages should be an array")
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
}

func TestOpenAICompatBuildHTTPRequestAppliesConfiguredHeaders(t *testing.T) {
	parsed, _ := url.Parse("http://localhost:11434/v1")
	p := &OpenAICompat{
		baseURL: parsed,
		apiKey:  "secret",
		headers: map[string]string{
			"X-Test-Header": "configured",
			"Authorization": "Bearer override",
		},
	}

	req, err := p.buildHTTPRequest(t.Context(), []byte(`{}`), true)
	if err != nil {
		t.Fatalf("buildHTTPRequest() error = %v", err)
	}
	if got, want := req.Header.Get("X-Test-Header"), "configured"; got != want {
		t.Fatalf("X-Test-Header = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Authorization"), "Bearer override"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := req.Header.Get("Accept"), "text/event-stream"; got != want {
		t.Fatalf("Accept = %q, want %q", got, want)
	}
}

func TestOpenAICompatChatCompletionsURL_NoTrailingSlash(t *testing.T) {
	parsed, _ := url.Parse("http://localhost:11434/v1")
	p := &OpenAICompat{baseURL: parsed}
	got := p.chatCompletionsURL()
	want := "http://localhost:11434/v1/chat/completions"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOpenAICompatChatCompletionsURL_TrailingSlash(t *testing.T) {
	parsed, _ := url.Parse("http://localhost:11434/v1/")
	p := &OpenAICompat{baseURL: parsed}
	got := p.chatCompletionsURL()
	want := "http://localhost:11434/v1/chat/completions"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOpenAICompatClassifyRetryError_ToolCallDecodeIsRetryable(t *testing.T) {
	p := &OpenAICompat{}
	tests := []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{
			name:      "decode tool call error is retryable",
			err:       fmt.Errorf("decode tool call %q arguments: invalid character '}' after array element", "mutate"),
			wantRetry: true,
		},
		{
			name:      "decode chat completion response is not retryable",
			err:       fmt.Errorf("decode chat completion response: unexpected EOF"),
			wantRetry: false,
		},
		{
			name:      "nil error",
			err:       nil,
			wantRetry: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := p.classifyRetryError(tt.err)
			if d.retry != tt.wantRetry {
				t.Fatalf("retry = %v, want %v", d.retry, tt.wantRetry)
			}
		})
	}
}
