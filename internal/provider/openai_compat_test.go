package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// staticRoundTripper is a transport stub that fails if it is ever used.
type staticRoundTripper struct{}

func (staticRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("staticRoundTripper: unexpected request")
}

func TestOpenAICompatNewOpenAICompat_EmptyBaseURL(t *testing.T) {
	_, err := NewOpenAICompat(ClientConfig{
		BaseURL: "",
		Model:   "gpt-4",
	})
	if err == nil {
		t.Fatal("expected error for empty base URL")
	}
}

func TestOpenAICompatNewOpenAICompat_InvalidURL(t *testing.T) {
	_, err := NewOpenAICompat(ClientConfig{
		BaseURL: "://invalid",
		Model:   "gpt-4",
	})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestOpenAICompatNewOpenAICompat_EmptyModel(t *testing.T) {
	_, err := NewOpenAICompat(ClientConfig{
		BaseURL: "http://localhost:11434/v1",
		Model:   "",
	})
	if err == nil {
		t.Fatal("expected error for empty model")
	}
}

func TestOpenAICompatNewOpenAICompat_DefaultHTTPClient(t *testing.T) {
	p, err := NewOpenAICompat(ClientConfig{
		BaseURL: "http://localhost:11434/v1",
		Model:   "gpt-4",
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
	cfg := ClientConfig{
		BaseURL:    "http://localhost:11434/v1",
		APIKey:     "test-key",
		Headers:    map[string]string{"X-Test-Header": "configured"},
		Model:      "gpt-4",
		HTTPClient: customClient,
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
	p, err := NewOpenAICompat(ClientConfig{
		BaseURL:    "http://localhost:11434/v1",
		Model:      "gpt-4",
		Timeout:    45 * time.Second,
		HTTPClient: customClient,
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
	p, err := NewOpenAICompat(ClientConfig{
		BaseURL:    "http://localhost:11434/v1",
		Model:      "gpt-4",
		Timeout:    45 * time.Second,
		HTTPClient: customClient,
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

// TestOpenAICompatTimeoutTransportSharedPerBase ensures clients built from the
// same base transport share one derived transport (and therefore one connection
// pool), whether their timeouts differ or match, while keeping their own
// Client.Timeout.
func TestOpenAICompatTimeoutTransportSharedPerBase(t *testing.T) {
	base := &http.Transport{ResponseHeaderTimeout: 30 * time.Second}
	httpClient := &http.Client{Transport: base}
	build := func(timeout time.Duration) *Client {
		t.Helper()
		p, err := NewOpenAICompat(ClientConfig{
			BaseURL:    "http://localhost:11434/v1",
			Model:      "gpt-4",
			Timeout:    timeout,
			HTTPClient: httpClient,
		})
		if err != nil {
			t.Fatalf("NewOpenAICompat() error = %v", err)
		}
		return p
	}

	first := build(10 * time.Second)
	second := build(2 * time.Minute)
	sameTimeout := build(10 * time.Second)

	if first.httpClient == second.httpClient {
		t.Fatal("expected a distinct http.Client per timeout")
	}
	if got, want := first.httpClient.Timeout, 10*time.Second; got != want {
		t.Fatalf("first Timeout = %v, want %v", got, want)
	}
	if got, want := second.httpClient.Timeout, 2*time.Minute; got != want {
		t.Fatalf("second Timeout = %v, want %v", got, want)
	}
	if first.httpClient.Transport != second.httpClient.Transport {
		t.Fatal("clients sharing a base transport got distinct derived transports")
	}
	if sameTimeout.httpClient == first.httpClient {
		t.Fatal("expected a distinct http.Client per provider with the same timeout")
	}
	if got, want := sameTimeout.httpClient.Timeout, 10*time.Second; got != want {
		t.Fatalf("same-timeout Timeout = %v, want %v", got, want)
	}
	if sameTimeout.httpClient.Transport != first.httpClient.Transport {
		t.Fatal("clients with the same timeout and base transport got distinct derived transports")
	}
	derived, ok := first.httpClient.Transport.(*http.Transport)
	if !ok || derived == nil {
		t.Fatalf("Transport = %T, want *http.Transport", first.httpClient.Transport)
	}
	if derived == base {
		t.Fatal("expected a derived transport, got the base transport")
	}
	if derived.ResponseHeaderTimeout != 0 {
		t.Fatalf("derived ResponseHeaderTimeout = %v, want 0", derived.ResponseHeaderTimeout)
	}
	if base.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("base ResponseHeaderTimeout = %v, want 30s (must not be mutated)", base.ResponseHeaderTimeout)
	}
}

// TestOpenAICompatTimeoutTransportConcurrentConstruction checks that concurrent
// client construction for one base transport produces a single shared clone.
func TestOpenAICompatTimeoutTransportConcurrentConstruction(t *testing.T) {
	const builders = 16
	base := &http.Transport{ResponseHeaderTimeout: 30 * time.Second}
	httpClient := &http.Client{Transport: base}
	transports := make([]http.RoundTripper, builders)

	var wg sync.WaitGroup
	for i := 0; i < builders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := NewOpenAICompat(ClientConfig{
				BaseURL:    "http://localhost:11434/v1",
				Model:      "gpt-4",
				Timeout:    time.Duration(i+1) * time.Second,
				HTTPClient: httpClient,
			})
			if err != nil {
				t.Errorf("NewOpenAICompat() error = %v", err)
				return
			}
			transports[i] = p.httpClient.Transport
		}(i)
	}
	wg.Wait()

	if t.Failed() {
		t.FailNow()
	}
	for i, transport := range transports {
		if transport != transports[0] {
			t.Fatalf("build %d got a distinct derived transport; want one shared clone per base", i)
		}
	}
	derived, ok := transports[0].(*http.Transport)
	if !ok || derived == nil {
		t.Fatalf("Transport = %T, want *http.Transport", transports[0])
	}
	if derived.ResponseHeaderTimeout != 0 {
		t.Fatalf("derived ResponseHeaderTimeout = %v, want 0", derived.ResponseHeaderTimeout)
	}
	if base.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("base ResponseHeaderTimeout = %v, want 30s (must not be mutated)", base.ResponseHeaderTimeout)
	}
}

func TestOpenAICompatTimeoutKeepsCustomRoundTripper(t *testing.T) {
	roundTripper := http.RoundTripper(staticRoundTripper{})
	httpClient := &http.Client{Transport: roundTripper}
	p, err := NewOpenAICompat(ClientConfig{
		BaseURL:    "http://localhost:11434/v1",
		Model:      "gpt-4",
		Timeout:    5 * time.Second,
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompat() error = %v", err)
	}
	if p.httpClient == httpClient {
		t.Fatal("expected timeout configuration to clone HTTP client")
	}
	if p.httpClient.Transport != roundTripper {
		t.Fatalf("Transport = %T, want the custom RoundTripper preserved", p.httpClient.Transport)
	}
	if got, want := p.httpClient.Timeout, 5*time.Second; got != want {
		t.Fatalf("Timeout = %v, want %v", got, want)
	}
}

func TestOpenAICompatSupportsUsageStats_NonNil(t *testing.T) {
	p := &Client{}
	if !p.SupportsUsageStats() {
		t.Fatal("expected true for provider")
	}
}

func TestOpenAICompatSupportsUsageStats_Nil(t *testing.T) {
	var p *Client
	if !p.SupportsUsageStats() {
		t.Fatal("expected true for nil provider")
	}
}

func TestOpenAICompatReadErrorResponse_EmptyBody(t *testing.T) {
	p := &Client{}
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
	p := &Client{}
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

func TestOpenAICompatNormalizeChatResponse_Characterization(t *testing.T) {
	response, err := normalizeChatResponse(&openAIResponse{
		Choices: []openAIChoice{
			{
				Message: openAIMessage{
					Role:    "assistant",
					Content: "final answer",
				},
				FinishReason: "stop",
			},
		},
		Usage: &UsageStats{
			PromptTokens:     11,
			CompletionTokens: 7,
			TotalTokens:      18,
		},
	})
	if err != nil {
		t.Fatalf("normalizeChatResponse() error = %v", err)
	}
	if got, want := response.Message.Role, MessageRoleAssistant; got != want {
		t.Fatalf("role = %q, want %q", got, want)
	}
	if got, want := response.Message.Content, "final answer"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if got, want := response.FinishReason, "stop"; got != want {
		t.Fatalf("finish reason = %q, want %q", got, want)
	}
	if response.Usage == nil {
		t.Fatal("usage = nil, want usage stats")
	}
	if got, want := response.Usage.TotalTokens, 18; got != want {
		t.Fatalf("total tokens = %d, want %d", got, want)
	}
}

func TestOpenAICompatDecodeChatStream_Characterization(t *testing.T) {
	body := strings.NewReader(`data: {"choices":[{"delta":{"content":"hello"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}

data: [DONE]

`)

	var chunks []ChatChunk
	err := decodeChatStreamWithHandler(t.Context(), body, func(chunk ChatChunk) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("decodeChatStreamWithHandler() error = %v", err)
	}
	if got, want := len(chunks), 2; got != want {
		t.Fatalf("len(chunks) = %d, want %d", got, want)
	}
	if got, want := chunks[0].Delta.Content, "hello"; got != want {
		t.Fatalf("first chunk content = %q, want %q", got, want)
	}
	if !chunks[1].Done {
		t.Fatal("final chunk Done = false, want true")
	}
	if got, want := chunks[1].Delta.Content, "hello"; got != want {
		t.Fatalf("final chunk content = %q, want %q", got, want)
	}
	if got, want := chunks[1].FinishReason, "stop"; got != want {
		t.Fatalf("final chunk finish reason = %q, want %q", got, want)
	}
	if chunks[1].Usage == nil || chunks[1].Usage.TotalTokens != 18 {
		t.Fatalf("final chunk usage = %#v, want total tokens 18", chunks[1].Usage)
	}
}

func TestOpenAICompatClassifyRetryError_DecodeIsNotRetryable(t *testing.T) {
	p := &Client{}
	tests := []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{
			name:      "decode chat completion response is not retryable",
			err:       fmt.Errorf("%w: %w", errDecodeChatCompletionResponse, io.ErrUnexpectedEOF),
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
			d := p.classifyRetryError(context.Background(), tt.err)
			if d.retry != tt.wantRetry {
				t.Fatalf("retry = %v, want %v", d.retry, tt.wantRetry)
			}
		})
	}
}
